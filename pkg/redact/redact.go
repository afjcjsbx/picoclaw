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
	"crypto/sha256"
	"encoding/json"
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
	// forms are the strings the replacer looks for, kept so secrets can tell
	// with allocation-free substring scans whether the replacer has any work.
	forms []string
	// maxLen is the longest form, used to size the streaming writer's carry
	// buffer. It is zero when no credentials are registered.
	maxLen int
}

var registered atomic.Pointer[secretSet]

func init() {
	registered.Store(&secretSet{replacer: strings.NewReplacer()})
}

// SetSecrets replaces the registered exact credential values. Empty and very
// short values are ignored: they would mask ordinary text without protecting
// anything meaningful.
//
// Each value is also registered in its JSON-escaped form, so a credential that
// contains `&`, `<`, `>`, `"` or `\` is still masked in text that was marshaled
// to JSON, such as composite log fields.
func SetSecrets(values []string) {
	cleaned := normalizeSecrets(values)
	forms := make([]string, 0, len(cleaned)*2)
	forms = append(forms, cleaned...)
	for _, value := range cleaned {
		if escaped := jsonEscaped(value); escaped != value {
			forms = append(forms, escaped)
		}
	}
	// Replacer picks the first matching pattern. Sorting longest first keeps
	// a registered key that contains another registered value intact.
	sort.Slice(forms, func(i, j int) bool {
		if len(forms[i]) != len(forms[j]) {
			return len(forms[i]) > len(forms[j])
		}
		return forms[i] < forms[j]
	})
	pairs := make([]string, 0, len(forms)*2)
	maxLen := 0
	for _, form := range forms {
		pairs = append(pairs, form, Marker)
		if len(form) > maxLen {
			maxLen = len(form)
		}
	}
	registered.Store(&secretSet{
		replacer: strings.NewReplacer(pairs...),
		forms:    forms,
		maxLen:   maxLen,
	})
}

// jsonEscaped returns s as it appears inside a JSON string literal.
func jsonEscaped(s string) string {
	b, err := json.Marshal(s)
	if err != nil || len(b) < 2 {
		return s
	}
	return string(b[1 : len(b)-1])
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
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

// secrets replaces only the exact credential values registered with
// SetSecrets, leaving heuristic detection off.
func secrets(s string) string {
	if s == "" {
		return s
	}
	set := registered.Load()
	// strings.Replacer copies its input even when nothing matches, which costs
	// about two bytes of garbage per byte of text on every streamed update.
	// Substring scans allocate nothing, so only hand over text that has a hit.
	for _, form := range set.forms {
		if strings.Contains(s, form) {
			return set.replacer.Replace(s)
		}
	}
	return s
}

// minPartialSecret is the shortest trailing fragment masked by a writer flush.
// Shorter fragments cannot meaningfully identify a credential and masking them
// would corrupt ordinary text that happens to share a one- or two-byte prefix.
const minPartialSecret = 4

// Writer returns an io.WriteCloser that replaces exact registered credentials
// in a byte stream as it is written. It is meant for data that is persisted
// incrementally (exec spill files, artifact windows) where materializing the
// whole payload to run the filter would defeat the memory bound.
//
// At most one byte less than the longest registered credential is held back, so
// a credential split across Write calls is still detected. Flush/Close write
// that tail and mask a trailing partial credential; Close does not close the
// underlying writer. Values are read from the atomically swapped registry on
// every write, so a config reload applies to an in-flight stream.
func Writer(w io.Writer) io.WriteCloser {
	return &secretWriter{dst: w}
}

type secretWriter struct {
	dst     io.Writer
	pending []byte
	err     error
}

// Write converts the chunk to a string and reuses the string path. io.Copy
// feeds this writer in small chunks, so the transient copy is chunk-sized, not
// artifact-sized.
func (w *secretWriter) Write(p []byte) (int, error) {
	return w.WriteString(string(p))
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
	if len(set.forms) == 0 {
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
	if len(buf) <= hold {
		w.pending = append(w.pending[:0], buf...)
		return len(p), nil
	}
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
	if len(set.forms) == 0 {
		return w.flushPending()
	}
	_, err := io.WriteString(w.dst, set.replaceFinal(string(w.pending)))
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
	if len(set.forms) == 0 || cut <= 0 || cut >= len(s) {
		return cut
	}
	return set.safeCutString(s, cut)
}

// SafeStart returns the smallest byte offset at or after start that does not
// split a registered credential. It is the counterpart of SafeCut for the tail
// of a head+tail truncation.
func SafeStart(s string, start int) int {
	set := registered.Load()
	if len(set.forms) == 0 || start <= 0 || start >= len(s) {
		return start
	}
	return set.safeStartString(s, start)
}

func (s *secretSet) safeCutString(text string, cut int) int {
	for cut > 0 {
		moved := false
		for _, secret := range s.forms {
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
		for _, secret := range s.forms {
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
func (s *secretSet) replaceFinal(p string) string {
	if n := s.partialPrefixLen(p); n > 0 {
		return s.replacer.Replace(p[:len(p)-n]) + Marker
	}
	return s.replacer.Replace(p)
}

func (s *secretSet) partialPrefixLen(p string) int {
	best := 0
	for _, secret := range s.forms {
		limit := len(secret) - 1
		if len(p) < limit {
			limit = len(p)
		}
		for n := limit; n >= minPartialSecret; n-- {
			if strings.HasSuffix(p, secret[:n]) {
				if n > best {
					best = n
				}
				break
			}
		}
	}
	return best
}

// Text masks registered credentials and credential-looking values in s,
// including key=value assignments, credential headers and URL credentials.
// Those looser rules also match ordinary prose and code, so use it only on
// machine-written text such as logs, errors and event payloads; use Content
// for text that must stay readable as written.
func Text(s string) string {
	if s == "" {
		return s
	}
	return maskLines(secrets(s), true)
}

// Content masks registered credentials and well-known credential formats in
// text the model wrote or a person reads, such as chat replies and streamed
// updates. It skips the assignment, header and URL rules of Text, which would
// turn `password = getPassword()` in a code block into `password = [FILTERED]`.
// It allocates nothing when there is nothing to mask, so streaming paths can
// call it on every update.
func Content(s string) string {
	return Formats(secrets(s))
}

// Formats masks only well-known credential formats and high-entropy tokens.
// It skips registered values and the assignment, header and URL rules of Text,
// which would turn `password = getPassword()` into `password = [FILTERED]`. It
// suits content the model reads and may edit, such as file contents.
func Formats(s string) string {
	return maskLines(s, false)
}

// maskLines masks s one line at a time, because the rules assume a single
// line. It copies only when a line changes, so text with nothing to mask costs
// no allocation.
func maskLines(s string, loose bool) string {
	var out strings.Builder
	copied := 0 // s[:copied] is already in out
	for start := 0; start <= len(s); {
		end := len(s)
		if i := strings.IndexByte(s[start:], '\n'); i >= 0 {
			end = start + i
		}
		if masked := line(s[start:end], loose); masked != s[start:end] {
			out.WriteString(s[copied:start])
			out.WriteString(masked)
			copied = end
		}
		start = end + 1
	}
	if copied == 0 && out.Len() == 0 {
		return s
	}
	out.WriteString(s[copied:])
	return out.String()
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
	// after, when set, is asked about the text following each literal hit and
	// must accept it for the regexp to run. Words like token also name
	// prompt_tokens and max_tokens; this rejects them without a regexp scan.
	after func(rest string) bool
	// replacement is used with ReplaceAllString. Patterns with capture groups
	// keep the key name and mask only the value; the default masks the whole
	// match.
	replacement string
	// keep, when set, is asked about every whole match and leaves it unmasked
	// if it returns true. It carves known non-secrets out of a format that the
	// regexp alone cannot exclude (RE2 has no lookahead).
	keep func(match string) bool
}

func (p secretPattern) mayMatch(line string) bool {
	for _, lit := range p.lits {
		for from := 0; ; {
			i := indexLit(line[from:], lit, p.fold)
			if i < 0 {
				break
			}
			end := from + i + len(lit)
			if p.after == nil || p.after(line[end:]) {
				return true
			}
			from += i + 1
		}
	}
	return false
}

// indexLit returns the index of lit in s, or -1. With fold, lit must be a
// lowercase ASCII word and case is ignored; only positions whose byte can
// start lit are compared.
func indexLit(s, lit string, fold bool) int {
	if !fold {
		return strings.Index(s, lit)
	}
	for i := 0; i+len(lit) <= len(s); i++ {
		if s[i]|0x20 == lit[0] && strings.EqualFold(s[i:i+len(lit)], lit) {
			return i
		}
	}
	return -1
}

// spaceAfter accepts text that starts with whitespace: a scheme word such as
// "token" followed by its credential.
func spaceAfter(rest string) bool {
	return rest != "" && strings.ContainsRune(" \t\n\f\r", rune(rest[0]))
}

// assignAfter accepts text that continues like an assignment: optional
// whitespace and quote, then = or :.
func assignAfter(rest string) bool {
	const space = " \t\n\f\r"
	rest = strings.TrimLeft(rest, space)
	if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
		rest = strings.TrimLeft(rest[1:], space)
	}
	return rest != "" && (rest[0] == '=' || rest[0] == ':')
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
		keep: isSessionKey,
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
}

// loosePatterns match credential-looking assignments and headers. They are
// useful on logs and error text but also hit ordinary prose and source code, so
// Formats leaves them out.
var loosePatterns = []secretPattern{
	// Basic and Token authorization schemes, which the Bearer rule misses.
	{
		re:          regexp.MustCompile(`(?i)(\b(?:basic|token)\s+)[A-Za-z0-9+/=_-]{16,}`),
		lits:        []string{"basic", "token"},
		fold:        true,
		after:       spaceAfter,
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
		after:       assignAfter,
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

// line masks credential-looking values in a single line. The loose rules
// (assignments, headers, URL credentials) only run when loose is set.
func line(text string, loose bool) string {
	text = applyPatterns(text, secretPatterns)
	if loose {
		text = applyPatterns(text, loosePatterns)
		// regexp.ReplaceAllString copies its input even without a match, so
		// these rules need the same cheap prefilter as the pattern lists.
		if strings.ContainsAny(text, "?&") && strings.Contains(text, "=") {
			text = sensitiveQueryParamPattern.ReplaceAllString(text, "${1}"+Marker)
		}
		if strings.Contains(text, "://") && strings.Contains(text, "@") {
			text = basicAuthURLPattern.ReplaceAllString(text, "${1}"+Marker+"${3}")
		}
	}
	return redactHighEntropyRuns(text)
}

func applyPatterns(text string, patterns []secretPattern) string {
	for _, p := range patterns {
		if !p.mayMatch(text) {
			continue
		}
		if p.keep != nil {
			text = p.re.ReplaceAllStringFunc(text, func(match string) string {
				if p.keep(match) {
					return match
				}
				return Marker
			})
			continue
		}
		replacement := p.replacement
		if replacement == "" {
			replacement = Marker
		}
		text = p.re.ReplaceAllString(text, replacement)
	}
	return text
}

// sessionKeyPrefix starts every canonical session key: "sk_v1_" followed by the
// 64 hex digits of a SHA-256 (see pkg/session).
const sessionKeyPrefix = "sk_v1_"

// isSessionKey reports whether s is exactly a canonical session key. Session
// keys are opaque identifiers derived from public routing data, not
// credentials, but their "sk_" prefix matches the API-key format. Masking them
// blanks the session_key field of log lines and makes sessions impossible to
// follow.
func isSessionKey(s string) bool {
	return len(s) == len(sessionKeyPrefix)+sha256.Size*2 &&
		strings.HasPrefix(s, sessionKeyPrefix) &&
		isHexToken(s[len(sessionKeyPrefix):])
}

// mayHaveOpaqueRun is a prefilter for highEntropyRunPattern. It reports
// whether line holds a run of token characters that redactHighEntropyRuns could
// still mask: long enough, mixing letters and digits, and without a slash.
// Most log lines and chat text have none, and skipping the regexp for them
// avoids both its scan and the copy ReplaceAllStringFunc makes of its input.
func mayHaveOpaqueRun(line string) bool {
	const minRun = min(secretHintMinLength, opaqueMinLength)
	run := 0
	var letter, digit, slash bool
	for i := 0; i <= len(line); i++ {
		var c byte // 0 past the end: not a token character, closes the last run
		if i < len(line) {
			c = line[i]
		}
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			letter = true
		case c == '/':
			slash = true
		case c == '+' || c == '=' || c == '_' || c == '-':
		default:
			if run >= minRun && letter && digit && !slash {
				return true
			}
			run, letter, digit, slash = 0, false, false, false
			continue
		}
		run++
	}
	return false
}

func redactHighEntropyRuns(line string) string {
	if !mayHaveOpaqueRun(line) {
		return line
	}
	return maskOpaqueRuns(line)
}

func maskOpaqueRuns(line string) string {
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
		// A session key at the end of the run is an identifier, not a
		// credential. Judge only what precedes it, so "session_key=sk_v1_<hex>"
		// does not get masked as one opaque 80-character token.
		candidate, sessionKey := token, ""
		if i := strings.Index(token, sessionKeyPrefix); i >= 0 && isSessionKey(token[i:]) {
			candidate, sessionKey = token[:i], token[i:]
		}
		if len(candidate) < minLength {
			return token
		}
		if isHexToken(candidate) || !hasLetterAndDigit(candidate) {
			// Hex hashes and pure letter or digit runs are far more likely
			// to be commit ids, checksums or identifiers than credentials,
			// even on a line that mentions a secret-like word.
			return token
		}
		if strings.ContainsAny(candidate, `/\`) {
			// Slashes make the run a filesystem path or a URL far more often
			// than a credential. Masking paths would corrupt ordinary log
			// lines and chat messages without protecting a secret.
			return token
		}
		if shannonEntropy(candidate) < minEntropy {
			return token
		}
		return Marker + sessionKey
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
