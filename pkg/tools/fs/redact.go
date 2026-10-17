package fstools

import (
	"math"
	"regexp"
	"strings"
)

// redactedMarker is the placeholder used by the pipeline-wide sensitive data
// filter (see docs/security/sensitive_data_filtering.md); the built-in
// redaction (search results and file reads) uses the same marker for
// consistency.
const redactedMarker = "[FILTERED]"

// searchSecretPatterns match well-known credential formats with high
// confidence, so they are always masked.
var searchSecretPatterns = []*regexp.Regexp{
	// Authorization: Bearer <token>
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]{16,}=*`),
	// JSON Web Tokens: three dot-separated base64url segments starting with eyJ.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	// OpenAI / Stripe style keys: sk-..., sk_..., rk-..., rk_...
	regexp.MustCompile(`\b(?:sk|rk)[-_][A-Za-z0-9_-]{16,}`),
	// GitHub tokens: ghp_..., gho_..., ghu_..., ghs_..., ghr_..., github_pat_...
	regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
	// AWS access key IDs.
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	// Slack tokens: xoxb-..., xoxa-, xoxp-, xoxr-, xoxs-...
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	// Google API keys.
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
}

// searchHighEntropyRunPattern matches candidate machine-generated tokens:
// runs of token characters at least searchSecretHintMinLength long. Shorter
// runs can never be masked, so the regexp starts at the smallest threshold.
var searchHighEntropyRunPattern = regexp.MustCompile(`[A-Za-z0-9+/=_-]{20,}`)

// searchSecretHintPattern detects a secret-like identifier on the line
// (api_key, token, password, ...), which lowers the entropy threshold: on a
// line that talks about a key, a shorter or slightly less random token is
// worth masking.
var searchSecretHintPattern = regexp.MustCompile(
	`(?i)(?:^|[^A-Za-z0-9])(?:apikey|key|token|secret|password|passwd|credential|auth|bearer|signature|private)(?:$|[^A-Za-z0-9])`,
)

const (
	// Entropy thresholds in bits per character and minimum token lengths
	// for the generic high-entropy rule, calibrated so that ordinary code
	// identifiers and prose stay intact.
	searchSecretHintMinEntropy = 3.5
	searchSecretHintMinLength  = 20
	searchOpaqueMinEntropy     = 4.75
	searchOpaqueMinLength      = 32
)

// redactSecrets masks credential-looking values in content rendered to the
// model (search results and file reads), so that secrets found in files do not
// reach the model even when they are not part of the configured sensitive
// values covered by the pipeline filter. Well-known token formats are always
// masked; other tokens are masked when they look machine-generated (long and
// high-entropy, and not a plain hex hash such as a commit id or checksum).
//
// Heuristics are evaluated per line: a secret-like identifier on one line does
// not lower the bar for tokens on other lines, and the hex exemption stays
// line-scoped even when whole files are passed in.
func redactSecrets(text string) string {
	if !strings.Contains(text, "\n") {
		return redactSecretLine(text)
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = redactSecretLine(line)
	}
	return strings.Join(lines, "\n")
}

// redactSecretLine masks credential-looking values in a single line.
func redactSecretLine(line string) string {
	for _, pattern := range searchSecretPatterns {
		line = pattern.ReplaceAllString(line, redactedMarker)
	}
	return redactHighEntropyRuns(line)
}

func redactHighEntropyRuns(text string) string {
	hinted := searchSecretHintPattern.MatchString(text)
	return searchHighEntropyRunPattern.ReplaceAllStringFunc(text, func(token string) string {
		minEntropy, minLength := searchOpaqueMinEntropy, searchOpaqueMinLength
		if hinted {
			minEntropy, minLength = searchSecretHintMinEntropy, searchSecretHintMinLength
		}
		if len(token) < minLength {
			return token
		}
		if !hinted && (isHexToken(token) || !hasLetterAndDigit(token)) {
			// Without a secret-like word on the line, hex hashes and
			// pure letter or digit runs are far more likely to be commit
			// ids, checksums or identifiers than credentials.
			return token
		}
		if shannonEntropy(token) < minEntropy {
			return token
		}
		return redactedMarker
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
