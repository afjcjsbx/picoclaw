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
	"encoding/json"
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
	// forms are the strings the replacer looks for, kept so Secrets can tell
	// with allocation-free substring scans whether the replacer has any work.
	forms []string
	count int
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
	for _, form := range forms {
		pairs = append(pairs, form, Marker)
	}
	registered.Store(&secretSet{
		replacer: strings.NewReplacer(pairs...),
		forms:    forms,
		count:    len(cleaned),
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

// Text masks registered credentials and credential-looking values in s,
// including key=value assignments, credential headers and URL credentials.
// Those looser rules also match ordinary prose and code, so use Formats for
// text that must stay readable as written.
func Text(s string) string {
	if s == "" {
		return s
	}
	return maskLines(Secrets(s), true)
}

// Formats masks only well-known credential formats and high-entropy tokens.
// It skips registered values and the assignment, header and URL rules of Text,
// which would turn `password = getPassword()` into `password = [FILTERED]`. It
// suits content the model reads and may edit, such as file contents.
func Formats(s string) string {
	return maskLines(s, false)
}

func maskLines(s string, loose bool) string {
	if !strings.Contains(s, "\n") {
		return line(s, loose)
	}
	lines := strings.Split(s, "\n")
	for i, lineText := range lines {
		lines[i] = line(lineText, loose)
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
		if p.mayMatch(text) {
			replacement := p.replacement
			if replacement == "" {
				replacement = Marker
			}
			text = p.re.ReplaceAllString(text, replacement)
		}
	}
	return text
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
