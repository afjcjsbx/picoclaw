package redact

import (
	"strings"
	"testing"
)

func TestSecretRedactor_APIKeys(t *testing.T) {
	r := NewSecretRedactor()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"openai", "key: sk-abc123def456ghi789jkl012mno345", "[REDACTED:API_KEY]"},
		{"aws", "AKIAIOSFODNN7EXAMPLE", "[REDACTED:API_KEY]"},
		{"github_pat", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", "[REDACTED:API_KEY]"},
		{"groq", "gsk_abcdefghijklmnopqrstuvwx", "[REDACTED:API_KEY]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if !strings.Contains(got, "[REDACTED:") {
				t.Errorf("expected redaction for %q, got %q", tt.input, got)
			}
		})
	}
}

func TestSecretRedactor_BearerTokens(t *testing.T) {
	r := NewSecretRedactor()

	tests := []struct {
		input string
	}{
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ"},
		{"bearer abc123def456ghi789"},
	}

	for _, tt := range tests {
		got := r.Redact(tt.input)
		if !strings.Contains(got, "[REDACTED:BEARER_TOKEN]") {
			t.Errorf("expected BEARER_TOKEN redaction for %q, got %q", tt.input, got)
		}
	}
}

func TestSecretRedactor_Passwords(t *testing.T) {
	r := NewSecretRedactor()

	tests := []struct {
		name  string
		input string
	}{
		{"password_eq", `password=mysecretpassword123`},
		{"secret_colon", `secret: "verysecretvalue123"`},
		{"api_key_eq", `api_key=sk_live_abcdefghij`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if !strings.Contains(got, "[REDACTED:") {
				t.Errorf("expected redaction for %q, got %q", tt.input, got)
			}
		})
	}
}

func TestSecretRedactor_NoFalsePositives(t *testing.T) {
	r := NewSecretRedactor()

	tests := []struct {
		name  string
		input string
	}{
		{"normal_text", "This is a normal sentence about programming."},
		{"short_word", "The key is simplicity."},
		{"code", "func main() { fmt.Println(\"hello\") }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.input)
			if strings.Contains(got, "[REDACTED:") {
				t.Errorf("false positive redaction for %q: %q", tt.input, got)
			}
		})
	}
}

func TestSecretRedactor_Idempotent(t *testing.T) {
	r := NewSecretRedactor()

	input := "Bearer eyJhbGciOiJIUzI1NiJ9.eyJ0ZXN0IjoiMTIzIn0"
	first := r.Redact(input)
	second := r.Redact(first)

	if first != second {
		t.Errorf("not idempotent:\n  first:  %q\n  second: %q", first, second)
	}
}

func TestShannonEntropy(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		minBits float64
	}{
		{"empty", "", 0},
		{"single_char", "aaaa", 0},
		{"low_entropy", "aabb", 1.0},
		{"high_entropy", "aB3$xY9!kL2@mN5#", 4.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shannonEntropy(tt.input)
			if got < tt.minBits {
				t.Errorf("entropy(%q) = %.2f, want >= %.2f", tt.input, got, tt.minBits)
			}
		})
	}
}

func TestChainRedactor(t *testing.T) {
	chain := &ChainRedactor{
		Redactors: []Redactor{
			NewSecretRedactor(),
		},
	}

	input := "Bearer abc123def456ghi789"
	got := chain.Redact(input)
	if !strings.Contains(got, "[REDACTED:BEARER_TOKEN]") {
		t.Errorf("chain should apply SecretRedactor, got %q", got)
	}
}
