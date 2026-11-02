package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteToolsKeepCharLimits(t *testing.T) {
	ws := t.TempDir()
	memFile := filepath.Join(ws, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memFile), 0o755); err != nil {
		t.Fatal(err)
	}
	limits := map[string]int{memFile: 20}

	write := NewWriteFileTool(ws, true)
	write.SetCharLimits(limits)
	edit := NewEditFileTool(ws, true)
	edit.SetCharLimits(limits)
	appendTool := NewAppendFileTool(ws, true)
	appendTool.SetCharLimits(limits)

	ctx := context.Background()
	content := func() string { b, _ := os.ReadFile(memFile); return string(b) }
	denied := func(name string, got *ToolResult) {
		t.Helper()
		if !got.IsError || !strings.Contains(got.ForLLM, "memory tool") {
			t.Errorf("%s should be refused with a memory tool hint, got %+v", name, got)
		}
	}

	// A write over the limit is refused, whether the path is relative or absolute.
	for _, path := range []string{"memory/MEMORY.md", memFile} {
		denied("write "+path, write.Execute(ctx, map[string]any{"path": path, "content": strings.Repeat("x", 30)}))
	}
	if content() != "" {
		t.Fatalf("refused write changed the file: %q", content())
	}

	// Within the limit it goes through, and so does an unrelated file of any size.
	if res := write.Execute(ctx, map[string]any{"path": "memory/MEMORY.md", "content": "short fact"}); res.IsError {
		t.Fatalf("write within the limit refused: %s", res.ForLLM)
	}
	if res := write.Execute(ctx, map[string]any{
		"path": "memory/notes.md", "content": strings.Repeat("x", 500),
	}); res.IsError {
		t.Fatalf("unrelated file must not be limited: %s", res.ForLLM)
	}

	// append_file and edit_file cannot grow it past the limit either.
	denied("append", appendTool.Execute(ctx, map[string]any{
		"path": "memory/MEMORY.md", "content": "\n" + strings.Repeat("y", 30),
	}))
	denied("edit", edit.Execute(ctx, map[string]any{
		"path": "memory/MEMORY.md", "old_text": "short", "new_text": strings.Repeat("z", 30),
	}))
	if got := content(); got != "short fact" {
		t.Fatalf("file = %q", got)
	}

	// An oversized file can still be shrunk, never grown.
	if err := os.WriteFile(memFile, []byte(strings.Repeat("a", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := write.Execute(ctx, map[string]any{
		"path": "memory/MEMORY.md", "content": strings.Repeat("a", 30), "overwrite": true,
	}); res.IsError {
		t.Fatalf("shrinking an oversized file refused: %s", res.ForLLM)
	}
	denied("growing an oversized file", appendTool.Execute(ctx, map[string]any{
		"path": "memory/MEMORY.md", "content": "more",
	}))
}

func TestCharLimitsFollowSymlinks(t *testing.T) {
	ws := t.TempDir()
	memFile := filepath.Join(ws, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(memFile, filepath.Join(ws, "alias.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	write := NewWriteFileTool(ws, false)
	write.SetCharLimits(map[string]int{memFile: 20})
	res := write.Execute(context.Background(), map[string]any{
		"path": filepath.Join(ws, "alias.md"), "content": strings.Repeat("x", 30),
	})
	if !res.IsError {
		t.Fatal("a symlink to a limited file must be limited too")
	}
}
