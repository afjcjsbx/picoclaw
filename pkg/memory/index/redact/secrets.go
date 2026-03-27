package redact

import (
	"math"
	"regexp"
	"strings"
)

// SecretRedactor detects and masks common secret patterns.
type SecretRedactor struct{}

// NewSecretRedactor creates a new SecretRedactor.
func NewSecretRedactor() *SecretRedactor {
	return &SecretRedactor{}
}

// apiKeyPatterns matches known API key formats.
var apiKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[a-zA-Z0-9]{20,}\b`),      // OpenAI
	regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),         // AWS
	regexp.MustCompile(`\bgsk_[a-zA-Z0-9]{20,}\b`),     // Groq
	regexp.MustCompile(`\bxoxb-[0-9]+-[a-zA-Z0-9]+\b`), // Slack bot
	regexp.MustCompile(`\bxoxp-[0-9]+-[a-zA-Z0-9]+\b`), // Slack user
	regexp.MustCompile(`\bghp_[a-zA-Z0-9]{36,}\b`),     // GitHub PAT
	regexp.MustCompile(`\bgho_[a-zA-Z0-9]{36,}\b`),     // GitHub OAuth
	regexp.MustCompile(`\bglpat-[a-zA-Z0-9_-]{20,}\b`), // GitLab PAT
	regexp.MustCompile(`\bAIza[a-zA-Z0-9_-]{35}\b`),    // Google API key
	regexp.MustCompile(`\b[a-f0-9]{32}\b`),             // Generic 32-char hex (API keys)
}

// bearerPattern matches Bearer tokens in any context.
var bearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[a-zA-Z0-9._\-/+=]{10,}\b`)

// passwordPatterns matches password/secret assignments.
var passwordPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:password|passwd|pwd|secret|token|api_?key|auth)\s*[=:]\s*["']?([^\s"']{8,})["']?`),
}

// redactedAlready matches already-redacted tokens to ensure idempotency.
var redactedAlready = regexp.MustCompile(`\[REDACTED:[A-Z_]+\]`)

// Redact replaces sensitive patterns with [REDACTED:<TYPE>] tokens.
func (r *SecretRedactor) Redact(text string) string {
	// Skip already-redacted content for idempotency
	if redactedAlready.MatchString(text) {
		// Still process in case there are new secrets alongside old redactions
	}

	// Replace Bearer tokens
	text = bearerPattern.ReplaceAllString(text, "[REDACTED:BEARER_TOKEN]")

	// Replace API key patterns
	for _, pat := range apiKeyPatterns {
		text = pat.ReplaceAllStringFunc(text, func(match string) string {
			// Don't re-redact already redacted text
			if strings.HasPrefix(match, "[REDACTED:") {
				return match
			}
			return "[REDACTED:API_KEY]"
		})
	}

	// Replace password assignments
	for _, pat := range passwordPatterns {
		text = pat.ReplaceAllStringFunc(text, func(match string) string {
			if strings.Contains(match, "[REDACTED:") {
				return match
			}
			// Find the key part and redact only the value
			idx := strings.IndexAny(match, "=:")
			if idx < 0 {
				return match
			}
			key := match[:idx+1]
			return key + " [REDACTED:PASSWORD]"
		})
	}

	// Entropy-based detection for high-entropy strings near secret identifiers
	text = redactHighEntropySecrets(text)

	return text
}

// secretContextPattern identifies strings adjacent to secret-like identifiers.
var secretContextPattern = regexp.MustCompile(`(?i)(?:key|token|secret|credential|auth)[_\s]*[=:"']\s*([a-zA-Z0-9+/=_\-]{16,})`)

// redactHighEntropySecrets finds high-entropy strings near secret-like contexts.
func redactHighEntropySecrets(text string) string {
	return secretContextPattern.ReplaceAllStringFunc(text, func(match string) string {
		if strings.Contains(match, "[REDACTED:") {
			return match
		}
		// Extract the value part
		idx := strings.IndexAny(match, "=:\"'")
		if idx < 0 {
			return match
		}
		value := strings.TrimSpace(match[idx+1:])
		value = strings.Trim(value, "\"' ")

		if len(value) >= 16 && shannonEntropy(value) > 4.5 {
			key := match[:idx+1]
			return key + " [REDACTED:SECRET]"
		}
		return match
	})
}

// shannonEntropy computes the Shannon entropy of a string in bits per character.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	freq := make(map[rune]int)
	for _, r := range s {
		freq[r]++
	}
	length := float64(len([]rune(s)))
	var entropy float64
	for _, count := range freq {
		p := float64(count) / length
		if p > 0 {
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}
