package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactSecretsMasksKnownTokenFormats(t *testing.T) {
	// Token samples are assembled from fragments on purpose: the repository
	// must not contain a literal string that secret scanners would flag,
	// while the assembled lines still exercise the redaction patterns.
	stripeKey := "rk_" + "live_" + "51H8xYzAbCdEfGhIjKlMnOpQr"
	slackToken := "xox" + "b-123456789012-abcdefghijklmnop"
	cases := []struct {
		name string
		line string
	}{
		{"bearer header", `Authorization: Bearer 8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo`},
		{"jwt", `token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U`},
		{"openai key", `OPENAI_API_KEY=sk-proj-4eC39HqLyjWDarjtT1zdp7dc`},
		{"stripe restricted key", "stripe " + stripeKey},
		{"github token", `export GH=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789`},
		{"aws access key", `aws_access_key_id = AKIAIOSFODNN7EXAMPLE`},
		{"slack token", "slack: " + slackToken},
		{"google api key", `key=AIzaSyA1234567890abcdefghijklmnopqrstuv`},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.line)
		if !strings.Contains(got, redactedMarker) {
			t.Errorf("%s: expected %s in %q", tc.name, redactedMarker, got)
		}
	}
}

func TestRedactSecretsMasksHighEntropyTokens(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"assignment", `api_token = "kZ8x3PqR7tYv2wBc5nMf9jLh4gD1sA6eUoQpXmWz"`},
		{"unhinted random token", `value 8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo2dF5gH7jK9lM`},
	}
	for _, tc := range cases {
		got := redactSecrets(tc.line)
		if !strings.Contains(got, redactedMarker) {
			t.Errorf("%s: expected %s in %q", tc.name, redactedMarker, got)
		}
	}
}

func TestRedactSecretsKeepsOrdinaryText(t *testing.T) {
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

func TestSearchFilesRedactsSecretsInResults(t *testing.T) {
	root := t.TempDir()
	content := "token = \"8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo\"\nnormal = \"hello world\"\n"
	if err := os.WriteFile(filepath.Join(root, "config.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)

	result := tool.Execute(context.Background(), map[string]any{"pattern": "="})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo") {
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
	if strings.Contains(result.ForLLM, "8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo") {
		t.Fatalf("secret token must be redacted in context lines:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, redactedMarker) {
		t.Fatalf("expected %s in context results:\n%s", redactedMarker, result.ForLLM)
	}
}

func TestReadFileToolRedactsSecretsInBytesMode(t *testing.T) {
	dir := t.TempDir()
	content := "OPENAI_API_KEY=sk-proj-4eC39HqLyjWDarjtT1zdp7dc\n" +
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
	if strings.Contains(result.ForLLM, "sk-proj-4eC39HqLyjWDarjtT1zdp7dc") {
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
	token := "kZ8x3PqR7tYv2wBc5nMf9jLh4gD1sA6eUoQpXmWz"
	content := "api_token = \"" + token + "\"\nplain = \"hello world\"\n"
	path := filepath.Join(dir, "config.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileLinesTool(dir, true, MaxReadFileSize)

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
