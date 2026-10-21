package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken assembles a credential-looking sample at runtime. Splitting every
// high-entropy run keeps secret scanners from flagging the fixtures while the
// concatenated value still exercises the redaction patterns.
func fakeToken(parts ...string) string { return strings.Join(parts, "") }

func TestRedactSearchSecretsMasksKnownTokenFormats(t *testing.T) {
	stripeKey := fakeToken("rk_", "live_", "51H8xYzA", "bCdEfGhI", "jKlMnOpQr")
	slackToken := fakeToken("xox", "b-", "123456789012", "-", "abcdefghij", "klmnop")
	cases := []struct {
		name string
		line string
	}{
		{"bearer header", "Authorization: Bearer " + fakeToken("8fJd92kL", "mQ73xPqR", "5tNvBw6Y", "cAeZ1sUo")},
		{"jwt", "token " + fakeToken(
			"eyJhbGciOi", "JIUzI1NiJ9", ".",
			"eyJzdWIiOi", "IxMjM0NTY3", "ODkwIn0", ".",
			"dozjgNryP4", "J3jVmNHl0w", "5N_XgL0n3I", "9PlFUP0THs", "R8U",
		)},
		{"openai key", "OPENAI_API_KEY=" + fakeToken("sk-proj-", "4eC39HqL", "yjWDarjt", "T1zdp7dc")},
		{"stripe restricted key", "stripe " + stripeKey},
		{"github token", "export GH=" + fakeToken("ghp_", "aBcDeFgHiJ", "kLmNoPqRsT", "uVwXyZ0123", "456789")},
		{"aws access key", "aws_access_key_id = " + fakeToken("AKIA", "IOSFODNN7", "EXAMPLE")},
		{"slack token", "slack: " + slackToken},
		{"google api key", "key=" + fakeToken("AIza", "SyA12345", "67890abc", "defghijkl", "mnopqrstuv")},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.line)
		if !strings.Contains(got, redactedMarker) {
			t.Errorf("%s: expected %s in %q", tc.name, redactedMarker, got)
		}
	}
}

func TestRedactSearchSecretsMasksHighEntropyTokens(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"assignment", "api_token = \"" + fakeToken("kZ8x3PqR7", "tYv2wBc5n", "Mf9jLh4gD", "1sA6eUoQp", "XmWz") + "\""},
		{"unhinted random token", "value " + fakeToken(
			"8fJd92kL", "mQ73xPqR", "5tNvBw6Y", "cAeZ1sUo", "2dF5gH7j", "K9lM",
		)},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.line)
		if !strings.Contains(got, redactedMarker) {
			t.Errorf("%s: expected %s in %q", tc.name, redactedMarker, got)
		}
	}
}

func TestRedactSearchSecretsKeepsOrdinaryText(t *testing.T) {
	cases := []string{
		`SomeVeryLongCamelCaseIdentifierNameXYZ1234`,
		`func getUserProfileByAccountIdentifierAndTenantID(ctx context.Context) {`,
		`commit e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`,
		`// lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod`,
		`https://github.com/sipeed/picoclaw/blob/main/README.md`,
		`const maxSearchHunkLines = 50`,
		`retry_policy: exponential-backoff-with-jitter-enabled-by-default`,
	}
	for _, line := range cases {
		if got := redactSecrets(line); got != line {
			t.Errorf("line must be left unchanged:\n in: %q\nout: %q", line, got)
		}
	}
}

// TestRedactSearchSecretsKeepsHintedIdentifiers guards the calibration of the
// generic rule: a line that mentions a secret-like word must not mask ordinary
// configuration keys or hex checksums.
func TestRedactSearchSecretsKeepsHintedIdentifiers(t *testing.T) {
	cases := []string{
		`token_refresh_interval_seconds = 3600`,
		`password_confirmation_required_for_account_creation = true`,
		`api_key_rotation_policy: monthly`,
		`signature: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08`,
	}
	for _, line := range cases {
		if got := redactSecrets(line); got != line {
			t.Errorf("line must be left unchanged:\n in: %q\nout: %q", line, got)
		}
	}
}

func TestSearchFilesRedactsSecretsInResults(t *testing.T) {
	token := fakeToken("8fJd92kL", "mQ73xPqR", "5tNvBw6Y", "cAeZ1sUo")
	root := t.TempDir()
	content := "token = \"" + token + "\"\nnormal = \"hello world\"\n"
	if err := os.WriteFile(filepath.Join(root, "config.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)

	result := tool.Execute(context.Background(), map[string]any{"pattern": "="})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, token) {
		t.Fatalf("secret token must be redacted:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, redactedMarker) {
		t.Fatalf("expected %s in results:\n%s", redactedMarker, result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "hello world") {
		t.Fatalf("ordinary content must stay visible:\n%s", result.ForLLM)
	}

	// Context lines are rendered through a different code path and must be
	// redacted as well.
	result = tool.Execute(context.Background(), map[string]any{"pattern": "normal", "context": 1})
	if result.IsError {
		t.Fatalf("context search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, token) {
		t.Fatalf("secret token must be redacted in context lines:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, redactedMarker) {
		t.Fatalf("expected %s in context results:\n%s", redactedMarker, result.ForLLM)
	}
}

// TestSearchFilesRedactsBeforeTruncating guards the order of the two steps: a
// secret that would be cut by the 240-byte window must be masked in full, not
// left as a partial token that no detector can match.
func TestSearchFilesRedactsBeforeTruncating(t *testing.T) {
	token := fakeToken("AIza", "SyA12345", "67890abc", "defghijkl", "mnopqrstuv")
	root := t.TempDir()
	line := strings.Repeat("x", 210) + " " + token + " " + strings.Repeat("y", 100)
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)

	result := tool.Execute(context.Background(), map[string]any{"pattern": "x"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, token[:20]) {
		t.Fatalf("partial token leaked through the truncation window:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, redactedMarker) {
		t.Fatalf("expected %s in results:\n%s", redactedMarker, result.ForLLM)
	}
}

func TestReadFileToolRedactsSecretsInBytesMode(t *testing.T) {
	dir := t.TempDir()
	key := fakeToken("sk-proj-", "4eC39HqL", "yjWDarjt", "T1zdp7dc")
	content := "OPENAI_API_KEY=" + key + "\n" +
		"commit e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n"
	path := filepath.Join(dir, "config.env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir, true, MaxReadFileSize)

	result := tool.Execute(context.Background(), map[string]any{"path": path})
	if result.IsError {
		t.Fatalf("read failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, key) {
		t.Fatalf("secret key must be redacted:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, redactedMarker) {
		t.Fatalf("expected %s in output:\n%s", redactedMarker, result.ForLLM)
	}
	// The hex exemption stays line-scoped: the hint on the key line must not
	// cause the commit hash on the next line to be masked.
	if !strings.Contains(result.ForLLM, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") {
		t.Fatalf("hex hash must stay visible:\n%s", result.ForLLM)
	}
	// The header keeps describing the raw byte range of the file, so the
	// pagination offsets remain valid even when redaction shortens content.
	if !strings.Contains(result.ForLLM, "bytes 0-") {
		t.Fatalf("expected raw byte range in header:\n%s", result.ForLLM)
	}
}

func TestReadFileLinesToolRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	token := fakeToken("kZ8x3PqR7", "tYv2wBc5n", "Mf9jLh4gD", "1sA6eUoQp", "XmWz")
	content := "api_token = \"" + token + "\"\nplain = \"hello world\"\n"
	path := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileLinesTool(dir, true, MaxReadFileSize, DefaultReadFileMaxLines, DefaultReadFileMaxLineLength)

	result := tool.Execute(context.Background(), map[string]any{"path": path})
	if result.IsError {
		t.Fatalf("read failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, token) {
		t.Fatalf("secret token must be redacted:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, redactedMarker) {
		t.Fatalf("expected %s in output:\n%s", redactedMarker, result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, `plain = "hello world"`) {
		t.Fatalf("ordinary content must stay visible:\n%s", result.ForLLM)
	}
}
