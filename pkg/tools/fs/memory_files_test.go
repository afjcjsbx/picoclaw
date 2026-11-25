package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteToolsLeaveMemoryFilesToTheMemoryTool(t *testing.T) {
	ws := t.TempDir()
	memFile := filepath.Join(ws, "memory", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memFile, []byte("fact"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []string{memFile}

	write := NewWriteFileTool(ws, true)
	write.SetMemoryFiles(files)
	edit := NewEditFileTool(ws, true)
	edit.SetMemoryFiles(files)
	appendTool := NewAppendFileTool(ws, true)
	appendTool.SetMemoryFiles(files)

	ctx := context.Background()
	for name, res := range map[string]*ToolResult{
		"write relative": write.Execute(ctx, map[string]any{
			"path": "memory/MEMORY.md", "content": "x", "overwrite": true,
		}),
		"write absolute": write.Execute(ctx, map[string]any{
			"path": memFile, "content": "x", "overwrite": true,
		}),
		"edit":   edit.Execute(ctx, map[string]any{"path": "memory/MEMORY.md", "old_text": "fact", "new_text": "f"}),
		"append": appendTool.Execute(ctx, map[string]any{"path": "memory/MEMORY.md", "content": "more"}),
	} {
		if !res.IsError || !strings.Contains(res.ForLLM, "memory tool") {
			t.Errorf("%s should be refused with a memory tool hint, got %+v", name, res)
		}
	}
	if got, _ := os.ReadFile(memFile); string(got) != "fact" {
		t.Fatalf("refused writes changed the file: %q", got)
	}

	// Other files, even next to it, stay writable.
	if res := write.Execute(ctx, map[string]any{
		"path": "memory/notes.md", "content": strings.Repeat("x", 500),
	}); res.IsError {
		t.Fatalf("unrelated file must stay writable: %s", res.ForLLM)
	}

	// Without the protection (memory disabled) the same write goes through.
	open := NewAppendFileTool(ws, true)
	open.SetMemoryFiles(nil)
	if res := open.Execute(ctx, map[string]any{"path": "memory/MEMORY.md", "content": "more"}); res.IsError {
		t.Fatalf("unprotected tool refused: %s", res.ForLLM)
	}
}

func TestMemoryFilesFollowSymlinks(t *testing.T) {
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

	appendTool := NewAppendFileTool(ws, false)
	appendTool.SetMemoryFiles([]string{memFile})
	res := appendTool.Execute(context.Background(), map[string]any{
		"path": filepath.Join(ws, "alias.md"), "content": "more",
	})
	if !res.IsError {
		t.Fatal("a symlink to a memory file must be protected too")
	}
}
