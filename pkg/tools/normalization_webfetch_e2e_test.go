package tools_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/tools"
	integrationtools "github.com/sipeed/picoclaw/pkg/tools/integration"
)

// TestExecuteWithContext_WebFetchTruncatedPreviewPersistsFullArtifact is the
// end-to-end regression for the web-research failure: a 1,500-character
// maxChars preview on a page under the inline budget must still leave the full
// page readable in a workspace artifact, with a front-loaded warning.
func TestExecuteWithContext_WebFetchTruncatedPreviewPersistsFullArtifact(t *testing.T) {
	workspace := t.TempDir()
	reg := tools.NewToolRegistry()
	reg.SetWorkspace(workspace)
	// The full page fits the inline budget; only the requested preview is cut.
	reg.SetMaxInlineResultRunes(128 * 1024)

	page := strings.Repeat("release note line\n", 4000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	tool, err := integrationtools.NewWebFetchToolWithConfig(
		128*1024, "", "plaintext", 1<<20, []string{"127.0.0.1"},
	)
	if err != nil {
		t.Fatalf("NewWebFetchToolWithConfig: %v", err)
	}
	reg.Register(tool)

	result := reg.Execute(context.Background(), "web_fetch", map[string]any{
		"url":      server.URL,
		"maxChars": float64(1500),
	})
	if result.IsError {
		t.Fatalf("web_fetch failed: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForLLM, "WARNING: web_fetch preview truncated;") {
		t.Fatalf("expected the preview warning, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "was saved as a local artifact") {
		t.Fatalf("expected the artifact guidance, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, `"truncated": true`) {
		t.Fatalf("expected the tool envelope to keep truncated=true, got %q", result.ForLLM)
	}
	if result.FullText != "" || result.ForceArtifact || result.ArtifactMaxRunes != 0 {
		t.Fatalf("expected one-shot artifact fields consumed, got %+v", result)
	}
	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected one artifact tag, got %+v", result.ArtifactTags)
	}

	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact readable: %v", err)
	}
	if string(data) != strings.TrimSpace(page) {
		t.Fatalf(
			"expected the full %d-char page in the artifact, got %d chars",
			utf8.RuneCountInString(strings.TrimSpace(page)),
			utf8.RuneCount(data),
		)
	}
}

// TestExecuteWithContext_WebFetchSmallPreviewAboveInlineBudget is the
// regression for a per-call maxChars on a page larger than the inline budget:
// the tool's cut preview must stay inline instead of being replaced by the
// generic head+tail preview, while the full page is still persisted.
func TestExecuteWithContext_WebFetchSmallPreviewAboveInlineBudget(t *testing.T) {
	const inlineRunes = 16 * 1024
	workspace := t.TempDir()
	reg := tools.NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(inlineRunes)

	// ~108 KB: larger than the inline budget, so the generic spill path used to
	// discard the requested 1,500-character preview.
	page := strings.Repeat("release note line\n", 6000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	tool, err := integrationtools.NewWebFetchToolWithConfig(
		inlineRunes, "", "plaintext", 1<<20, []string{"127.0.0.1"},
	)
	if err != nil {
		t.Fatalf("NewWebFetchToolWithConfig: %v", err)
	}
	reg.Register(tool)

	result := reg.Execute(context.Background(), "web_fetch", map[string]any{
		"url":      server.URL,
		"maxChars": float64(1500),
	})
	if result.IsError {
		t.Fatalf("web_fetch failed: %s", result.ForLLM)
	}

	if !strings.HasPrefix(result.ForLLM, "WARNING: web_fetch preview truncated;") {
		t.Fatalf("expected the preview warning, got %q", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "[picoclaw-large-tool-result]") {
		t.Fatalf("expected the requested preview, got the generic spill preview: %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "[CONTENT TRUNCATED - ") {
		t.Fatalf("expected the tool's own truncation notice, got %q", result.ForLLM)
	}
	if n := utf8.RuneCountInString(result.ForLLM); n > 2500 {
		t.Fatalf("expected the 1,500-char preview to stay bounded, got %d runes", n)
	}
	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected one artifact tag, got %+v", result.ArtifactTags)
	}

	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact readable: %v", err)
	}
	if string(data) != strings.TrimSpace(page) {
		t.Fatalf(
			"expected the full %d-char page in the artifact, got %d chars",
			utf8.RuneCountInString(strings.TrimSpace(page)),
			utf8.RuneCount(data),
		)
	}
}

// TestExecuteWithContext_WebFetchDefaultsToInlineBudget verifies that when the
// model omits maxChars, the tool's preview cap is the same inline budget used
// by the spill layer, so a page above it is spilled with a full artifact.
func TestExecuteWithContext_WebFetchDefaultsToInlineBudget(t *testing.T) {
	const inlineRunes = 16 * 1024
	workspace := t.TempDir()
	reg := tools.NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(inlineRunes)

	page := strings.Repeat("release note line\n", 4000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
	}))
	defer server.Close()

	tool, err := integrationtools.NewWebFetchToolWithConfig(
		inlineRunes, "", "plaintext", 1<<20, []string{"127.0.0.1"},
	)
	if err != nil {
		t.Fatalf("NewWebFetchToolWithConfig: %v", err)
	}
	reg.Register(tool)

	result := reg.Execute(context.Background(), "web_fetch", map[string]any{
		"url": server.URL,
	})
	if result.IsError {
		t.Fatalf("web_fetch failed: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForLLM, "WARNING: web_fetch preview truncated;") {
		t.Fatalf("expected the preview warning, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "[picoclaw-large-tool-result]") {
		t.Fatalf("expected the generic spill preview, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected one artifact tag, got %+v", result.ArtifactTags)
	}

	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact readable: %v", err)
	}
	if string(data) != strings.TrimSpace(page) {
		t.Fatalf(
			"expected the full %d-char page in the artifact, got %d chars",
			utf8.RuneCountInString(strings.TrimSpace(page)),
			utf8.RuneCount(data),
		)
	}
}
