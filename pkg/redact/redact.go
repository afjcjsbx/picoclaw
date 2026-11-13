// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package redact masks credential-looking values in text that may reach logs,
// runtime events, or user-facing channels.
//
// Two layers work together:
//
//   - Exact values registered with SetSecrets are always replaced, regardless
//     of their format. Runtime registrations come from the loaded config, so
//     provider keys and channel tokens are masked even when they look like
//     ordinary identifiers.
//   - Well-known token formats and high-entropy machine-generated tokens are
//     detected heuristically, so a leak introduced by code we have not seen
//     yet is still masked.
package redact

import (
	"bytes"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// Marker is the placeholder substituted for masked values.
const Marker = "[FILTERED]"

// secretSet holds the exact credential values registered for the running
// process. It is replaced atomically on config load/reload, so log calls only
// pay for an atomic load plus, when configured, one linear replacement pass.
type secretSet struct {
	replacer *strings.Replacer
	count    int
	values   []string
	raw      [][]byte
	maxLen   int
}

var registered atomic.Pointer[secretSet]

func init() {
	registered.Store(&secretSet{replacer: strings.NewReplacer()})
}

// SetSecrets replaces the registered exact credential values. Empty and very
// short values are ignored: they would mask ordinary text without protecting
// anything meaningful.
func SetSecrets(values []string) {
	cleaned := normalizeSecrets(values)
	pairs := make([]string, 0, len(cleaned)*2)
	raw := make([][]byte, 0, len(cleaned))
	maxLen := 0
	for _, value := range cleaned {
		pairs = append(pairs, value, Marker)
		raw = append(raw, []byte(value))
		if len(value) > maxLen {
			maxLen = len(value)
		}
	}
	registered.Store(&secretSet{
		replacer: strings.NewReplacer(pairs...),
		count:    len(cleaned),
		values:   cleaned,
		raw:      raw,
		maxLen:   maxLen,
	})
}

// Registered returns the number of exact credential values currently
// registered.
func Registered() int {
	return registered.Load().count
}

func normalizeSecrets(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		// A three-character secret cannot be protected meaningfully and
		// would corrupt unrelated text.
		if len(value) <= 3 {
			continue
		}
		// Sensitive-looking names also cover boolean flags and enum settings
		// (SECRET_ENABLED=true). Registering those words would mask ordinary
		// log and chat text without protecting a credential.
		switch strings.ToLower(value) {
		case "true", "false", "yes", "no", "null", "none", "nil", "auto", "default", "oauth", "bearer":
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	// Replacer picks the first matching pattern. Sorting longest first keeps
	// a registered key that contains another registered value intact.
	sort.Slice(cleaned, func(i, j int) bool {
		if len(cleaned[i]) != len(cleaned[j]) {
			return len(cleaned[i]) > len(cleaned[j])
		}
		return cleaned[i] < cleaned[j]
	})
	return cleaned
}

// Secrets replaces only the exact credential values registered with
// SetSecrets, leaving heuristic detection off. Streaming paths call it on every
// incremental update, where running the pattern scan on the whole accumulated
// answer would be needlessly expensive.
func Secrets(s string) string {
	if s == "" {
		return s
	}
	set := registered.Load()
	if set.count == 0 {
		return s
	}
	return set.replacer.Replace(s)
}

// minPartialSecret is the shortest trailing fragment masked by a writer flush.
// Shorter fragments cannot meaningfully identify a credential and masking them
// would corrupt ordinary text that happens to share a one- or two-byte prefix.
const minPartialSecret = 4

// Writer returns an io.WriteCloser that replaces exact registered credentials
// in a byte stream as it is written. It is meant for data that is persisted
// incrementally (exec spill files, artifact windows) where materializing the
// whole payload to run Secrets would defeat the memory bound.
//
// At most one byte less than the longest registered credential is held back, so
// a credential split across Write calls is still detected. Close flushes that
// tail and masks a trailing partial credential; it does not close the
// underlying writer. Values are read from the atomically swapped registry on
// every Write, so a config reload applies to an in-flight stream.
func Writer(w io.Writer) io.WriteCloser {
	return &secretWriter{dst: w}
}

type secretWriter struct {
	dst     io.Writer
	pending []byte
	err     error
}

func (w *secretWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(p) == 0 {
		return 0, nil
	}

	set := registered.Load()
	if set.count == 0 {
		if err := w.flushPending(); err != nil {
			return 0, err
		}
		n, err := w.dst.Write(p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return n, err
		}
		return n, nil
	}

	buf := p
	if len(w.pending) > 0 {
		buf = append(w.pending, p...)
	}

	hold := set.maxLen - 1
	if hold < 0 {
		hold = 0
	}
	if len(buf) <= hold {
		w.pending = append(w.pending[:0], buf...)
		return len(p), nil
	}

	cut := set.safeCutBytes(buf, len(buf)-hold)
	if cut > 0 {
		if _, err := set.replacer.WriteString(w.dst, string(buf[:cut])); err != nil {
			w.err = err
			return 0, err
		}
	}
	w.pending = append(w.pending[:0], buf[cut:]...)
	return len(p), nil
}

// WriteString implements io.StringWriter so large artifact parts can be
// redacted straight from the source string without a []byte copy.
func (w *secretWriter) WriteString(p string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if p == "" {
		return 0, nil
	}

	set := registered.Load()
	if set.count == 0 {
		if err := w.flushPending(); err != nil {
			return 0, err
		}
		n, err := io.WriteString(w.dst, p)
		if err == nil && n != len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			w.err = err
			return n, err
		}
		return n, nil
	}

	hold := set.maxLen - 1
	if hold < 0 {
		hold = 0
	}
	if len(w.pending) == 0 {
		if len(p) <= hold {
			w.pending = append(w.pending[:0], p...)
			return len(p), nil
		}
		cut := set.safeCutString(p, len(p)-hold)
		if cut > 0 {
			if _, err := set.replacer.WriteString(w.dst, p[:cut]); err != nil {
				w.err = err
				return 0, err
			}
		}
		w.pending = append(w.pending[:0], p[cut:]...)
		return len(p), nil
	}

	buf := string(w.pending) + p
	cut := set.safeCutString(buf, len(buf)-hold)
	if cut > 0 {
		if _, err := set.replacer.WriteString(w.dst, buf[:cut]); err != nil {
			w.err = err
			return 0, err
		}
	}
	w.pending = append(w.pending[:0], buf[cut:]...)
	return len(p), nil
}

// Flush writes the held-back tail, masking a trailing partial credential.
// Artifact writers call it between non-contiguous head/notice/tail parts;
// Close calls it at the end of the stream.
func (w *secretWriter) Flush() error {
	if w.err != nil {
		return w.err
	}
	if len(w.pending) == 0 {
		return nil
	}
	set := registered.Load()
	if set.count == 0 {
		return w.flushPending()
	}
	_, err := io.WriteString(w.dst, set.replaceFinal(w.pending))
	w.pending = w.pending[:0]
	w.err = err
	return err
}

func (w *secretWriter) Close() error {
	return w.Flush()
}

func (w *secretWriter) flushPending() error {
	if len(w.pending) == 0 {
		return nil
	}
	n, err := w.dst.Write(w.pending)
	if err == nil && n != len(w.pending) {
		err = io.ErrShortWrite
	}
	w.pending = w.pending[:0]
	if err != nil {
		w.err = err
		return err
	}
	return nil
}

// SafeCut returns the largest byte offset at or before cut that does not split
// a registered credential. Callers that truncate text can use it to avoid
// leaving an unmatchable fragment of a secret at the kept boundary.
func SafeCut(s string, cut int) int {
	set := registered.Load()
	if set.count == 0 || cut <= 0 || cut >= len(s) {
		return cut
	}
	return set.safeCutString(s, cut)
}

// SafeStart returns the smallest byte offset at or after start that does not
// split a registered credential. It is the counterpart of SafeCut for the tail
// of a head+tail truncation.
func SafeStart(s string, start int) int {
	set := registered.Load()
	if set.count == 0 || start <= 0 || start >= len(s) {
		return start
	}
	return set.safeStartString(s, start)
}

func (s *secretSet) safeCutBytes(buf []byte, cut int) int {
	for cut > 0 {
		moved := false
		for _, secret := range s.raw {
			lo := cut - len(secret) + 1
			if lo < 0 {
				lo = 0
			}
			hi := cut + len(secret) - 1
			if hi > len(buf) {
				hi = len(buf)
			}
			search := lo
			for search < hi {
				idx := bytes.Index(buf[search:hi], secret)
				if idx < 0 {
					break
				}
				idx += search
				if idx >= cut {
					break
				}
				if idx+len(secret) > cut {
					cut = idx
					moved = true
					break
				}
				search = idx + 1
			}
			if moved {
				break
			}
		}
		if !moved {
			return cut
		}
	}
	return cut
}

func (s *secretSet) safeCutString(text string, cut int) int {
	for cut > 0 {
		moved := false
		for _, secret := range s.values {
			lo := cut - len(secret) + 1
			if lo < 0 {
				lo = 0
			}
			hi := cut + len(secret) - 1
			if hi > len(text) {
				hi = len(text)
			}
			search := lo
			for search < hi {
				idx := strings.Index(text[search:hi], secret)
				if idx < 0 {
					break
				}
				idx += search
				if idx >= cut {
					break
				}
				if idx+len(secret) > cut {
					cut = idx
					moved = true
					break
				}
				search = idx + 1
			}
			if moved {
				break
			}
		}
		if !moved {
			return cut
		}
	}
	return cut
}

func (s *secretSet) safeStartString(text string, start int) int {
	for start < len(text) {
		moved := false
		for _, secret := range s.values {
			lo := start - len(secret) + 1
			if lo < 0 {
				lo = 0
			}
			hi := start + len(secret)
			if hi > len(text) {
				hi = len(text)
			}
			search := lo
			for search < hi {
				idx := strings.Index(text[search:hi], secret)
				if idx < 0 {
					break
				}
				idx += search
				if idx >= start {
					break
				}
				if idx+len(secret) > start {
					start = idx + len(secret)
					moved = true
					break
				}
				search = idx + 1
			}
			if moved {
				break
			}
		}
		if !moved {
			return start
		}
	}
	return start
}

// replaceFinal replaces credentials in a final fragment and masks a trailing
// proper prefix of a registered credential, which would otherwise leak an
// unmatchable fragment when the stream ends mid-secret.
func (s *secretSet) replaceFinal(p []byte) string {
	if n := s.partialPrefixLen(p); n > 0 {
		return s.replacer.Replace(string(p[:len(p)-n])) + Marker
	}
	return s.replacer.Replace(string(p))
}

func (s *secretSet) partialPrefixLen(p []byte) int {
	best := 0
	for _, secret := range s.raw {
		limit := len(secret) - 1
		if len(p) < limit {
			limit = len(p)
		}
		for n := limit; n >= minPartialSecret; n-- {
			if bytes.HasSuffix(p, secret[:n]) {
				if n > best {
					best = n
				}
				break
			}
		}
	}
	return best
}

// Text masks registered credentials and credential-looking values in s.
func Text(s string) string {
	if s == "" {
		return s
	}
	s = Secrets(s)
	if !strings.Contains(s, "\n") {
		return line(s)
	}
	lines := strings.Split(s, "\n")
	for i, lineText := range lines {
		lines[i] = line(lineText)
	}
	return strings.Join(lines, "\n")
}

// secretPattern is a well-known credential format. Every match contains one
// of lits, so a plain substring test lets most lines skip the regexp: the
// patterns start with \b, which leaves Go's regexp without a literal prefix to
// search for and makes each one scan the whole line.
type secretPattern struct {
	re   *regexp.Regexp
	lits []string
	// fold compares lits case-insensitively; lits must then be lowercase
	// ASCII words.
	fold bool
	// replacement is used with ReplaceAllString. Patterns with capture groups
	// keep the key name and mask only the value; the default masks the whole
	// match.
	replacement string
}

func (p secretPattern) mayMatch(line string) bool {
	for _, lit := range p.lits {
		if p.fold && containsFold(line, lit) || !p.fold && strings.Contains(line, lit) {
			return true
		}
	}
	return false
}

// containsFold reports whether s contains lit, a lowercase ASCII word,
// ignoring case. It only compares at positions whose byte can start lit.
func containsFold(s, lit string) bool {
	for i := 0; i+len(lit) <= len(s); i++ {
		if s[i]|0x20 == lit[0] && strings.EqualFold(s[i:i+len(lit)], lit) {
			return true
		}
	}
	return false
}

// secretPatterns match well-known credential formats with high
// confidence, so they are always masked.
var secretPatterns = []secretPattern{
	// Authorization: Bearer <token>
	{
		re:   regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]{16,}=*`),
		lits: []string{"bearer"}, fold: true,
	},
	// JSON Web Tokens: three dot-separated base64url segments starting with eyJ.
	{
		re:   regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		lits: []string{"eyJ"},
	},
	// OpenAI / Stripe style keys: sk-..., sk_..., rk-..., rk_...
	{
		re:   regexp.MustCompile(`\b(?:sk|rk)[-_][A-Za-z0-9_-]{16,}`),
		lits: []string{"sk-", "sk_", "rk-", "rk_"},
	},
	// GitHub tokens: ghp_..., gho_..., ghu_..., ghs_..., ghr_..., github_pat_...
	{
		re:   regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
		lits: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_"},
	},
	// AWS access key IDs.
	{
		re:   regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		lits: []string{"AKIA", "ASIA"},
	},
	// Slack tokens: xoxb-..., xoxa-, xoxp-, xoxr-, xoxs-...
	{
		re:   regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
		lits: []string{"xox"},
	},
	// Google API keys.
	{
		re:   regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
		lits: []string{"AIza"},
	},
	// OpenCode Go keys and similar vendor-prefixed credentials.
	{
		re:   regexp.MustCompile(`\b[a-z]{2,8}_sk_[A-Za-z0-9_-]{16,}`),
		lits: []string{"_sk_"},
	},
	// Basic and Token authorization schemes, which the Bearer rule misses.
	{
		re:          regexp.MustCompile(`(?i)(\b(?:basic|token)\s+)[A-Za-z0-9+/=_-]{16,}`),
		lits:        []string{"basic", "token"},
		fold:        true,
		replacement: "${1}" + Marker,
	},
	// Explicit credential headers with values too short for the entropy rule.
	{
		re: regexp.MustCompile(
			`(?i)(\b(?:x-api-key|x-goog-api-key|api-key|x-auth-token)\s*:\s*)[A-Za-z0-9._~+/=-]{8,}`,
		),
		lits:        []string{"x-api-key", "x-goog-api-key", "api-key", "x-auth-token"},
		fold:        true,
		replacement: "${1}" + Marker,
	},
	// key=value / key: value assignments in JSON, env files and error bodies.
	{
		re: regexp.MustCompile(
			`(?i)(\b[a-z0-9_-]*(?:api[_-]?key|apikey|access[_-]?token|auth[_-]?token|token|client[_-]?secret|` +
				`password|passwd|secret)\s*["']?\s*[=:]\s*["']?)([^\s"'&,;]{8,})`,
		),
		lits: []string{
			"api_key", "apikey", "api-key", "access_token", "auth_token", "token",
			"client_secret", "password", "passwd", "secret",
		},
		fold:        true,
		replacement: "${1}" + Marker,
	},
}

// basicAuthURLPattern masks the password in scheme://user:password@host URLs,
// which transport errors often print in full.
var basicAuthURLPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^/\s:@]+:)([^/\s@]+)(@)`)

// sensitiveQueryParamPattern masks credentials carried in URL query strings,
// where the value may be short or not high-entropy (for example a signed URL).
var sensitiveQueryParamPattern = regexp.MustCompile(
	`(?i)([?&](?:api[_-]?key|apikey|key|token|access[_-]?token|refresh[_-]?token|` +
		`id[_-]?token|auth|authorization|password|passwd|secret|client[_-]?secret|` +
		`credential|sig|signature|code|awsaccesskeyid|x-amz-signature|x-amz-credential|` +
		`x-amz-security-token)=)([^&\s"'#]+)`,
)

// highEntropyRunPattern matches candidate machine-generated tokens: runs of
// token characters at least as long as the smallest minimum length below
// (secretHintMinLength and opaqueMinLength, both 32). Shorter runs can never
// be masked, so the repetition count must not exceed that smallest length.
var highEntropyRunPattern = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)

// secretHintPattern detects a secret-like identifier on the line
// (api_key, token, password, ...), which lowers the entropy threshold: on a
// line that talks about a key, a shorter or slightly less random token is
// worth masking.
var secretHintPattern = regexp.MustCompile(
	`(?i)(?:^|[^A-Za-z0-9])(?:apikey|key|token|secret|password|passwd|credential|auth|bearer|signature|private)(?:$|[^A-Za-z0-9])`,
)

const (
	// Entropy thresholds in bits per character and minimum token lengths
	// for the generic high-entropy rule, calibrated so that ordinary code
	// identifiers and prose stay intact. Hinted thresholds apply on lines
	// that mention a secret-like identifier; they stay high enough that
	// names such as token_refresh_interval_seconds are not masked.
	secretHintMinEntropy = 4.25
	secretHintMinLength  = 32
	opaqueMinEntropy     = 4.75
	opaqueMinLength      = 32
)

// line masks credential-looking values in a single line.
func line(text string) string {
	for _, p := range secretPatterns {
		if p.mayMatch(text) {
			replacement := p.replacement
			if replacement == "" {
				replacement = Marker
			}
			text = p.re.ReplaceAllString(text, replacement)
		}
	}
	text = sensitiveQueryParamPattern.ReplaceAllString(text, "${1}"+Marker)
	text = basicAuthURLPattern.ReplaceAllString(text, "${1}"+Marker+"${3}")
	return redactHighEntropyRuns(text)
}

func redactHighEntropyRuns(line string) string {
	// The hint regexp is costly and most lines hold no candidate run, so it
	// runs lazily, once, on the first candidate.
	var hinted, hintChecked bool
	return highEntropyRunPattern.ReplaceAllStringFunc(line, func(token string) string {
		minEntropy, minLength := opaqueMinEntropy, opaqueMinLength
		if !hintChecked {
			hinted, hintChecked = secretHintPattern.MatchString(line), true
		}
		if hinted {
			minEntropy, minLength = secretHintMinEntropy, secretHintMinLength
		}
		if len(token) < minLength {
			return token
		}
		if isHexToken(token) || !hasLetterAndDigit(token) {
			// Hex hashes and pure letter or digit runs are far more likely
			// to be commit ids, checksums or identifiers than credentials,
			// even on a line that mentions a secret-like word.
			return token
		}
		if strings.ContainsAny(token, `/\`) {
			// Slashes make the run a filesystem path or a URL far more often
			// than a credential. Masking paths would corrupt ordinary log
			// lines and chat messages without protecting a secret.
			return token
		}
		if shannonEntropy(token) < minEntropy {
			return token
		}
		return Marker
	})
}

func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	total := float64(len(s))
	var entropy float64
	for _, count := range counts {
		if count == 0 {
			continue
		}
		p := float64(count) / total
		entropy -= p * math.Log2(p)
	}
	return entropy
}

func isHexToken(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return s != ""
}

func hasLetterAndDigit(s string) bool {
	var letter, digit bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letter = true
		}
	}
	return letter && digit
}
