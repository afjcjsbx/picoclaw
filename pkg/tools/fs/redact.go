package fstools

import (
	"math"
	"regexp"
	"strings"
)

// redactedMarker is the placeholder used by the pipeline-wide sensitive data
// filter (see docs/security/sensitive_data_filtering.md); search results use
// the same marker for consistency.
const redactedMarker = "[FILTERED]"

// secretPatterns match well-known credential formats with high
// confidence, so they are always masked.
var secretPatterns = []*regexp.Regexp{
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

// highEntropyRunPattern matches candidate machine-generated tokens:
// runs of token characters at least secretHintMinLength long. Shorter
// runs can never be masked, so the regexp starts at the smallest threshold.
var highEntropyRunPattern = regexp.MustCompile(`[A-Za-z0-9+/=_-]{20,}`)

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

// redactSecrets masks credential-looking values in content rendered to the
// model (search results and file reads), so that secrets found in files do not
// reach the model even when they are not part of the configured sensitive
// values covered by the pipeline filter. Well-known token formats are always masked; other tokens are masked
// when they look machine-generated (long and high-entropy, and not a plain
// hex hash such as a commit id or checksum).
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
	for _, pattern := range secretPatterns {
		line = pattern.ReplaceAllString(line, redactedMarker)
	}
	return redactHighEntropyRuns(line)
}

func redactHighEntropyRuns(line string) string {
	hinted := secretHintPattern.MatchString(line)
	return highEntropyRunPattern.ReplaceAllStringFunc(line, func(token string) string {
		minEntropy, minLength := opaqueMinEntropy, opaqueMinLength
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
