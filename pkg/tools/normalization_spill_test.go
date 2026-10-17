package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func newSpillTool(name string, result *ToolResult) *mockRegistryTool {
	tool := newMockTool(name, "spill test tool")
	tool.result = result
	return tool
}

func TestExecuteWithContext_SpillsLargeResultToArtifact(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(32)

	large := strings.Repeat("tool output line\n", 50)
	reg.Register(newSpillTool("big_tool", SilentResult(large)))

	result := reg.Execute(context.Background(), "big_tool", nil)

	// The full payload must not be inline, but a bounded preview must be.
	if utf8.RuneCountInString(result.ForLLM) >= utf8.RuneCountInString(strings.TrimSpace(large)) {
		t.Fatalf("expected ForLLM reduced, got %d runes", utf8.RuneCountInString(result.ForLLM))
	}
	if !strings.Contains(result.ForLLM, largeResultSpillSentinel) {
		t.Fatalf("expected spill sentinel, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "characters truncated") {
		t.Fatalf("expected honest omission marker, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Full output saved as a local artifact") {
		t.Fatalf("expected artifact pointer, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected 1 artifact tag, got %+v", result.ArtifactTags)
	}
	tag := result.ArtifactTags[0]
	const prefix = "[file:"
	if !strings.HasPrefix(tag, prefix) || !strings.HasSuffix(tag, "]") {
		t.Fatalf("expected file artifact tag, got %q", tag)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(tag, prefix), "]")
	wantDir := filepath.Join(workspace, ".artifacts", "tool")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("expected artifact in %q, got %q", wantDir, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact file to be readable: %v", err)
	}
	if string(data) != strings.TrimSpace(large) {
		t.Fatalf("expected artifact file contents to match source text")
	}
	content := result.ContentForLLM()
	if !strings.Contains(content, "Local artifact paths: "+tag) {
		t.Fatalf("expected artifact path exposed to the model, got %q", content)
	}
	// The path must reach the model exactly once, not in both prose and tag.
	if n := strings.Count(content, path); n != 1 {
		t.Fatalf("expected artifact path once in model content, got %d times: %q", n, content)
	}
}

func TestExecuteWithContext_SpillKeepsHeadAndTailPreview(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(4096)

	large := "HEAD_MARKER_ALPHA " +
		strings.Repeat("the quick brown fox jumps over the lazy dog\n", 500) +
		"TAIL_MARKER_OMEGA"
	reg.Register(newSpillTool("big_tool", SilentResult(large)))

	result := reg.Execute(context.Background(), "big_tool", nil)

	if !strings.Contains(result.ForLLM, "HEAD_MARKER_ALPHA") {
		t.Fatalf("expected head preview retained, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "TAIL_MARKER_OMEGA") {
		t.Fatalf("expected tail preview retained, got %q", result.ForLLM)
	}
	if utf8.RuneCountInString(result.ForLLM) >= utf8.RuneCountInString(strings.TrimSpace(large)) {
		t.Fatalf("expected a bounded preview, got %d runes", utf8.RuneCountInString(result.ForLLM))
	}
	if !strings.Contains(result.ForLLM, "characters truncated") {
		t.Fatalf("expected omission marker, got %q", result.ForLLM)
	}
}

func TestExecuteWithContext_SpillIsIdempotent(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(32)

	large := strings.Repeat("tool output line\n", 50)
	reg.Register(newSpillTool("big_tool", SilentResult(large)))

	result := reg.Execute(context.Background(), "big_tool", nil)
	first := result.ForLLM
	firstTag := result.ArtifactTags[0]

	// Simulate a second normalization pass (for example after an AfterTool hook).
	reg.SpillLargeResult(result, "big_tool")

	if result.ForLLM != first {
		t.Fatalf("expected idempotent spill, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 1 || result.ArtifactTags[0] != firstTag {
		t.Fatalf("expected no second artifact, got %+v", result.ArtifactTags)
	}
}

func TestExecuteWithContext_SpillKeepsReadFileTruncationGuidance(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(4096)

	// Mimic read_file bytes-mode output: the native truncation/offset hint sits
	// at the start, followed by a large body. The head preview must preserve it.
	large := "[file: big.txt | total: 100000 bytes | read: bytes 0-65535]\n" +
		"[TRUNCATED - file has more content. Call read_file again with offset=1234 to continue.]\n\n" +
		strings.Repeat("body line\n", 5000)

	reg.Register(newSpillTool("read_file", SilentResult(large)))
	result := reg.Execute(context.Background(), "read_file", nil)

	if !strings.Contains(result.ForLLM, "offset=1234") {
		t.Fatalf("expected native read_file offset guidance preserved, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, largeResultSpillSentinel) {
		t.Fatalf("expected spill sentinel, got %q", result.ForLLM)
	}
}

func TestSpillLargeResult_AppliesToPostHookResult(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(64)

	// A hook-provided result that never went through tool execution.
	hookResult := SilentResult(strings.Repeat("hook payload line\n", 100))
	reg.SpillLargeResult(hookResult, "hooked_tool")

	if !strings.Contains(hookResult.ForLLM, largeResultSpillSentinel) {
		t.Fatalf("expected post-hook result spilled, got %q", hookResult.ForLLM)
	}
	if len(hookResult.ArtifactTags) != 1 {
		t.Fatalf("expected one artifact tag, got %+v", hookResult.ArtifactTags)
	}
	source := strings.TrimSpace(strings.Repeat("hook payload line\n", 100))
	if utf8.RuneCountInString(hookResult.ForLLM) >= utf8.RuneCountInString(source) {
		t.Fatalf("expected post-hook result reduced")
	}
}

func TestExecuteWithContext_KeepsSmallResultInline(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(4096)

	reg.Register(newSpillTool("small_tool", SilentResult("short output")))

	result := reg.Execute(context.Background(), "small_tool", nil)

	if result.ForLLM != "short output" {
		t.Fatalf("expected small result untouched, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 0 {
		t.Fatalf("expected no artifact tags, got %+v", result.ArtifactTags)
	}
}

func TestExecuteWithContext_SpillDisabledWithoutWorkspace(t *testing.T) {
	// SetWorkspace trims, so a blank workspace must behave like an unset one.
	for name, workspace := range map[string]string{"unset": "", "blank": "  \t "} {
		t.Run(name, func(t *testing.T) {
			reg := NewToolRegistry()
			reg.SetWorkspace(workspace)
			reg.SetMaxInlineResultRunes(8)

			large := strings.Repeat("tool output line\n", 50)
			reg.Register(newSpillTool("big_tool", SilentResult(large)))

			result := reg.Execute(context.Background(), "big_tool", nil)

			if !strings.Contains(result.ForLLM, "tool output line") {
				t.Fatalf("expected large result inline when workspace is unset, got %q", result.ForLLM)
			}
			if len(result.ArtifactTags) != 0 {
				t.Fatalf("expected no artifact tags, got %+v", result.ArtifactTags)
			}
		})
	}
}

func TestExecuteWithContext_SpillDisabledByNonPositiveThreshold(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(0)

	large := strings.Repeat("tool output line\n", 50)
	reg.Register(newSpillTool("big_tool", SilentResult(large)))

	result := reg.Execute(context.Background(), "big_tool", nil)

	if !strings.Contains(result.ForLLM, "tool output line") {
		t.Fatalf("expected large result inline when spill disabled, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 0 {
		t.Fatalf("expected no artifact tags, got %+v", result.ArtifactTags)
	}
}

func TestExecuteWithContext_DoesNotSpillResponseHandledResults(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(8)

	large := strings.Repeat("tool output line\n", 50)
	reg.Register(newSpillTool("send_tool", SilentResult(large).WithResponseHandled()))

	result := reg.Execute(context.Background(), "send_tool", nil)

	if !strings.Contains(result.ForLLM, "tool output line") {
		t.Fatalf("expected handled result left inline, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 0 {
		t.Fatalf("expected no artifact tags, got %+v", result.ArtifactTags)
	}
}

func TestExecuteWithContext_SpillPreservesExistingArtifactTags(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(8)

	large := strings.Repeat("tool output line\n", 50)
	toolResult := SilentResult(large)
	toolResult.ArtifactTags = []string{"[file:/tmp/existing.png]"}
	reg.Register(newSpillTool("big_tool", toolResult))

	result := reg.Execute(context.Background(), "big_tool", nil)

	if len(result.ArtifactTags) != 2 {
		t.Fatalf("expected existing tag preserved plus new tag, got %+v", result.ArtifactTags)
	}
	if result.ArtifactTags[0] != "[file:/tmp/existing.png]" {
		t.Fatalf("expected original tag first, got %+v", result.ArtifactTags)
	}
}

func TestExecuteWithContext_SpillFailureKeepsPreview(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspaceFile := filepath.Join(workspaceRoot, "not-a-directory")
	if err := os.WriteFile(workspaceFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to create workspace file: %v", err)
	}

	reg := NewToolRegistry()
	reg.SetWorkspace(workspaceFile)
	reg.SetMaxInlineResultRunes(8)

	large := strings.Repeat("tool output line\n", 50)
	reg.Register(newSpillTool("big_tool", SilentResult(large)))

	result := reg.Execute(context.Background(), "big_tool", nil)

	if utf8.RuneCountInString(result.ForLLM) >= utf8.RuneCountInString(strings.TrimSpace(large)) {
		t.Fatalf("expected ForLLM reduced on persistence failure, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, largeResultSpillSentinel) {
		t.Fatalf("expected spill sentinel on failure path, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "persistence failed") {
		t.Fatalf("expected persistence failure note, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 0 {
		t.Fatalf("expected no artifact tags on persistence failure, got %+v", result.ArtifactTags)
	}
}

func TestToolRegistry_CloneCopiesSpillSettings(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(1234)

	clone := reg.Clone()
	if clone.workspace != workspace {
		t.Fatalf("clone workspace = %q, want %q", clone.workspace, workspace)
	}
	if clone.maxInlineResultRunes != 1234 {
		t.Fatalf("clone maxInlineResultRunes = %d, want 1234", clone.maxInlineResultRunes)
	}
}

func TestExecuteWithContext_SpillNotBypassedByForgedSentinel(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(32)

	// Tool-controlled output that happens to contain the spill marker must not
	// be mistaken for an already-spilled result.
	large := largeResultSpillSentinel + "\n" + strings.Repeat("tool output line\n", 50)
	reg.Register(newSpillTool("big_tool", SilentResult(large)))

	result := reg.Execute(context.Background(), "big_tool", nil)

	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected forged sentinel to still spill, got tags %+v", result.ArtifactTags)
	}
	if utf8.RuneCountInString(result.ForLLM) >= utf8.RuneCountInString(strings.TrimSpace(large)) {
		t.Fatalf("expected ForLLM reduced, got %d runes", utf8.RuneCountInString(result.ForLLM))
	}
}

func TestBuildLargeResultPreview_MultibyteBoundaries(t *testing.T) {
	// 'é' is 2 bytes, '世' is 3 bytes: slicing by byte instead of rune would
	// split a character and produce invalid UTF-8.
	text := strings.Repeat("é", 40) + strings.Repeat("世", 40) + strings.Repeat("x", 40)
	total := utf8.RuneCountInString(text)

	preview, omitted, headRunes, tailRunes := buildLargeResultPreview(text, total, 30)

	if !utf8.ValidString(preview) {
		t.Fatalf("preview is not valid UTF-8: %q", preview)
	}
	if headRunes+tailRunes != 30 || omitted != total-30 {
		t.Fatalf("unexpected budgets: head=%d tail=%d omitted=%d", headRunes, tailRunes, omitted)
	}
	if !strings.HasPrefix(preview, strings.Repeat("é", headRunes)) {
		t.Fatalf("expected preview to start with %d head runes, got %q", headRunes, preview)
	}
	if !strings.HasSuffix(preview, strings.Repeat("x", tailRunes)) {
		t.Fatalf("expected preview to end with %d tail runes, got %q", tailRunes, preview)
	}
}

func TestBuildLargeResultPreview_BudgetInvariants(t *testing.T) {
	// For any oversized input (total > maxRunes > 0) the preview must omit at
	// least one rune and keep a non-empty tail; the builder relies on this
	// instead of re-checking it.
	caps := []int{1, 2, 3, 7, 30, 3071, 3072, 3073, 16 * 1024}
	for _, maxRunes := range caps {
		for _, extra := range []int{1, 2, 5000} {
			total := maxRunes + extra
			text := strings.Repeat("é", total)

			preview, omitted, headRunes, tailRunes := buildLargeResultPreview(text, total, maxRunes)

			if headRunes < 0 || tailRunes < 1 {
				t.Fatalf("max=%d total=%d: bad budgets head=%d tail=%d", maxRunes, total, headRunes, tailRunes)
			}
			if headRunes+tailRunes > maxRunes {
				t.Fatalf("max=%d total=%d: preview %d+%d exceeds cap", maxRunes, total, headRunes, tailRunes)
			}
			if omitted != total-headRunes-tailRunes || omitted < 1 {
				t.Fatalf("max=%d total=%d: omitted=%d", maxRunes, total, omitted)
			}
			if !utf8.ValidString(preview) {
				t.Fatalf("max=%d total=%d: invalid UTF-8 preview", maxRunes, total)
			}
		}
	}
}

func TestBuildLargeResultPreview_DoesNotCopyWholeText(t *testing.T) {
	text := strings.Repeat("a", 4<<20)
	total := utf8.RuneCountInString(text)

	// A []rune copy of the input would allocate ~4x its size (16MB here); the
	// preview itself only needs a few KB.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	buildLargeResultPreview(text, total, 16*1024)
	runtime.ReadMemStats(&after)
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<10 {
		t.Fatalf("preview allocated %d bytes for a %d byte input", grown, len(text))
	}
}

// buildWebFetchEnvelope mirrors the web_fetch result shape from
// pkg/tools/integration/web.go.
func buildWebFetchEnvelope(t *testing.T, text string) string {
	t.Helper()
	envelope, err := json.MarshalIndent(map[string]any{
		"url":       "https://example.com/releases",
		"status":    200,
		"extractor": "markdown",
		"truncated": false,
		"length":    len(text),
		"text":      text,
	}, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal web_fetch envelope: %v", err)
	}
	return string(envelope)
}

func TestPageableToolResultText_TextOnlyPayloadIsVerbatim(t *testing.T) {
	// Hooks, plugins, and other tools can legitimately return {"text": "..."}.
	// Without the web_fetch discriminator fields it is not the envelope.
	content := `{"text": "line one\nline two"}`
	if got := pageableToolResultText(content); got != content {
		t.Fatalf("pageableToolResultText() = %q, want verbatim %q", got, content)
	}
}

func TestPageableToolResultText_AllowsLeadingWhitespace(t *testing.T) {
	// The fast path only skips parsing; it must not reject a valid envelope
	// that happens to be indented.
	body := "line one\nline two"
	envelope := "\n\t  " + strings.TrimSpace(buildWebFetchEnvelope(t, body))
	pageable := pageableToolResultText(envelope)
	if !strings.Contains(pageable, body) {
		t.Fatalf("expected envelope with leading whitespace to unwrap, got %q", pageable)
	}
	if !strings.Contains(pageable, webFetchMetadataTag) {
		t.Fatalf("expected metadata block, got %q", pageable)
	}
}

func TestPageableToolResultText_KeepsMetadataAfterText(t *testing.T) {
	body := "line one\nline two"
	envelope, err := json.Marshal(map[string]any{
		"url":    "https://example.com/releases",
		"status": 200,
		"text":   body,
	})
	if err != nil {
		t.Fatalf("failed to marshal envelope: %v", err)
	}

	pageable := pageableToolResultText(string(envelope))

	text, rest, ok := strings.Cut(pageable, "\n\n"+webFetchMetadataTag+"\n")
	if !ok {
		t.Fatalf("expected metadata block, got %q", pageable)
	}
	if text != body {
		t.Fatalf("expected payload text before metadata, got %q", text)
	}
	metadata, ok := strings.CutSuffix(rest, "\n"+webFetchMetadataClosingTag+"\n")
	if !ok {
		t.Fatalf("expected closing metadata tag, got %q", rest)
	}
	var extras map[string]any
	if err := json.Unmarshal([]byte(metadata), &extras); err != nil {
		t.Fatalf("metadata is not valid JSON: %v", err)
	}
	if len(extras) != 2 || extras["url"] != "https://example.com/releases" || extras["status"] != float64(200) {
		t.Fatalf("unexpected metadata: %#v", extras)
	}
}

func TestPageableToolResultText_UnrecognizedContentIsVerbatim(t *testing.T) {
	cases := map[string]string{
		"ordinary tool JSON":  `{"output": "line\n", "exit_code": 0}`,
		"unknown sibling key": `{"text": "line\n", "exit_code": 0}`,
		"text only":           `{"text": "line\n"}`,
		"missing url":         `{"status": 200, "text": "line\n"}`,
		"missing status":      `{"url": "https://example.com", "text": "line\n"}`,
		"empty url":           `{"url": "", "status": 200, "text": "line\n"}`,
		"string status":       `{"url": "https://example.com", "status": "200", "text": "line\n"}`,
		"bool status":         `{"url": "https://example.com", "status": true, "text": "line\n"}`,
		"not an object":       `[{"text": "line\n"}]`,
		"structured text":     `{"text": {"blob": "line\n"}}`,
		"non-string text":     `{"text": 42}`,
		"empty text":          `{"text": ""}`,
		"missing text":        `{"url": "https://example.com"}`,
		"empty object":        `{}`,
		"unparseable JSON":    `{"text": "line`,
		"plain text":          "line one\nline two",
		"array payload":       `[1, 2, 3]`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if got := pageableToolResultText(content); got != content {
				t.Fatalf("pageableToolResultText() = %q, want verbatim %q", got, content)
			}
		})
	}
}

func TestExecuteWithContext_SpillUnwrapsWebFetchEnvelope(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(512)

	body := strings.Repeat("pageable content line\n", 200)
	envelope := buildWebFetchEnvelope(t, body)
	reg.Register(newSpillTool("web_fetch", SilentResult(envelope)))

	result := reg.Execute(context.Background(), "web_fetch", nil)

	// The inline preview must carry the payload with real newlines, not the
	// JSON envelope with escaped ones.
	if !strings.Contains(result.ForLLM, "pageable content line\n") {
		t.Fatalf("expected real newlines in preview, got %q", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, `\npageable`) {
		t.Fatalf("expected no escaped newlines in preview, got %q", result.ForLLM)
	}
	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected 1 artifact tag, got %+v", result.ArtifactTags)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact file to be readable: %v", err)
	}
	artifact := string(data)
	if !strings.Contains(artifact, "pageable content line\n") {
		t.Fatalf("expected artifact to keep real newlines, got %q", artifact[:min(200, len(artifact))])
	}
	if strings.Contains(artifact, `\npageable`) {
		t.Fatalf("expected no escaped newlines in artifact")
	}
	if !strings.Contains(artifact, webFetchMetadataTag) {
		t.Fatalf("expected metadata block in artifact")
	}
	if !strings.Contains(artifact, `"extractor": "markdown"`) {
		t.Fatalf("expected envelope fields preserved as metadata, got %q", artifact[max(0, len(artifact)-300):])
	}
}

func TestExecuteWithContext_UnwrappedEnvelopeThatFitsIsKeptInline(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)

	// Newline-heavy payload: escaping roughly doubles the raw JSON size, so
	// after unwrapping the result fits the inline budget and must not spill.
	body := strings.Repeat("a\n", 40)
	envelope := strings.TrimSpace(buildWebFetchEnvelope(t, body))
	pageable := pageableToolResultText(envelope)
	rawSize := utf8.RuneCountInString(envelope)
	pageableSize := utf8.RuneCountInString(pageable)
	if pageableSize >= rawSize {
		t.Fatalf("fixture must shrink when unwrapped: raw=%d pageable=%d", rawSize, pageableSize)
	}
	reg.SetMaxInlineResultRunes(pageableSize + (rawSize-pageableSize)/2)
	reg.Register(newSpillTool("web_fetch", SilentResult(envelope)))

	result := reg.Execute(context.Background(), "web_fetch", nil)

	if result.ForLLM != pageable {
		t.Fatalf("expected the unwrapped result kept whole, got %q", result.ForLLM)
	}
	if result.LargeResultSpilled {
		t.Fatalf("expected no spill flag on an unwrapped result that fits")
	}
	if len(result.ArtifactTags) != 0 {
		t.Fatalf("expected no artifact tags, got %+v", result.ArtifactTags)
	}
}

func TestExecuteWithContext_SpillKeepsOpaqueJSONVerbatim(t *testing.T) {
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(32)

	opaque := "{\"output\": \"" + strings.Repeat(`x\n`, 100) + "\", \"exit_code\": 0}"
	reg.Register(newSpillTool("exec", SilentResult(opaque)))

	result := reg.Execute(context.Background(), "exec", nil)

	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected 1 artifact tag, got %+v", result.ArtifactTags)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact file to be readable: %v", err)
	}
	if string(data) != strings.TrimSpace(opaque) {
		t.Fatalf("expected opaque JSON artifact verbatim, got %q", string(data))
	}
}

func TestExecuteWithContext_SpillUnwrapsEnvelopeFromAnyTool(t *testing.T) {
	// Recognition is by shape, not tool name: an exec result (or an AfterTool
	// hook result) that is byte-for-byte the web_fetch envelope is unwrapped
	// too, so the fix is not tied to a single producer.
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(512)

	body := strings.Repeat("pageable content line\n", 200)
	envelope := buildWebFetchEnvelope(t, body)
	reg.Register(newSpillTool("exec", SilentResult(envelope)))

	result := reg.Execute(context.Background(), "exec", nil)

	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected 1 artifact tag, got %+v", result.ArtifactTags)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact file to be readable: %v", err)
	}
	if !strings.Contains(string(data), "pageable content line\n") {
		t.Fatalf("expected the envelope shape to be unwrapped regardless of tool name")
	}
	if !strings.Contains(string(data), webFetchMetadataTag) {
		t.Fatalf("expected metadata block in artifact")
	}
}

func TestExecuteWithContext_SpillKeepsTextOnlyJSONVerbatim(t *testing.T) {
	// Hooks, plugins, and other tools can return {"text": "..."} without the
	// web_fetch discriminator fields. That is not the envelope and must stay
	// byte-for-byte verbatim.
	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(32)

	opaque := "{\"text\": \"" + strings.Repeat(`x\n`, 100) + "\"}"
	reg.Register(newSpillTool("exec", SilentResult(opaque)))

	result := reg.Execute(context.Background(), "exec", nil)

	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected 1 artifact tag, got %+v", result.ArtifactTags)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact file to be readable: %v", err)
	}
	if string(data) != strings.TrimSpace(opaque) {
		t.Fatalf("expected text-only JSON artifact verbatim, got %q", string(data))
	}
}
