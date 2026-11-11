package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func TestReadFileLinesTool_RegistryValidationSupportsMaxLinesAndRejectsLimit(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "registry_lines.txt")

	err := os.WriteFile(testFile, []byte("line 1\nline 2\nline 3\n"), 0o644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	reg := NewToolRegistry()
	reg.Register(fstools.NewReadFileLinesTool(
		tmpDir, false, fstools.MaxReadFileSize,
		fstools.DefaultReadFileMaxLines, fstools.DefaultReadFileMaxLineLength,
	))

	result := reg.Execute(context.Background(), "read_file", map[string]any{
		"path":       testFile,
		"start_line": 1,
		"max_lines":  1,
	})
	if result.IsError {
		t.Fatalf("expected max_lines to pass registry validation, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "1|line 1\n") {
		t.Fatalf("expected first line via max_lines, got: %s", result.ForLLM)
	}

	result = reg.Execute(context.Background(), "read_file", map[string]any{
		"path":       testFile,
		"start_line": 2,
		"limit":      1,
	})
	if !result.IsError {
		t.Fatalf("expected limit to be rejected, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "unexpected property \"limit\"") {
		t.Fatalf("expected registry validation error for limit, got: %s", result.ForLLM)
	}
}

func TestReadFileLinesTool_KeepsLargeBase64LikeLineText(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large_text.txt")
	payload := strings.Repeat("A", 3000)
	if err := os.WriteFile(testFile, []byte(payload+"\n"), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	reg := NewToolRegistry()
	reg.SetMaxInlineResultRunes(1 << 20)
	reg.Register(fstools.NewReadFileLinesTool(
		tmpDir, false, fstools.MaxReadFileSize,
		fstools.DefaultReadFileMaxLines, fstools.DefaultReadFileMaxLineLength,
	))

	result := reg.Execute(context.Background(), "read_file", map[string]any{"path": testFile})
	if result.IsError {
		t.Fatalf("read_file error = %s", result.ForLLM)
	}
	if result.ForLLM == toolshared.LargeBase64OmittedMessage {
		t.Fatal("line-mode text must not be replaced by the base64 omission notice")
	}
	if !strings.Contains(result.ForLLM, strings.Repeat("A", 128)) {
		t.Fatalf("expected the line-mode payload to stay inline, got: %s", result.ForLLM)
	}
}

func TestReadFileTool_StillOmitsLargeBase64LikeBytes(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "large_binary_like.txt")
	if err := os.WriteFile(testFile, []byte(strings.Repeat("A", 3000)), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	reg := NewToolRegistry()
	reg.SetMaxInlineResultRunes(1 << 20)
	reg.Register(fstools.NewReadFileTool(tmpDir, false, fstools.MaxReadFileSize))

	result := reg.Execute(context.Background(), "read_file", map[string]any{"path": testFile})
	if result.IsError {
		t.Fatalf("read_file error = %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, toolshared.LargeBase64OmittedMessage) {
		t.Fatalf("byte mode must keep the base64 guard, got: %s", result.ForLLM)
	}
}

func TestEditFileTool_RegistryValidationAcceptsPatchWithoutOldText(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch_registry.txt")
	if err := os.WriteFile(testFile, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	reg := NewToolRegistry()
	reg.Register(fstools.NewEditFileTool(tmpDir, true))

	patch := "*** Begin Patch\n*** Update File: " + testFile + "\n@@\n-before\n+after\n*** End Patch"
	result := reg.Execute(context.Background(), "edit_file", map[string]any{
		"path":  testFile,
		"patch": patch,
	})
	if result.IsError {
		t.Fatalf("expected patch-only edit_file call to pass registry validation, got: %s", result.ForLLM)
	}

	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("Failed to read edited file: %v", err)
	}
	if strings.TrimSpace(string(content)) != "after" {
		t.Fatalf("expected patched content, got: %q", content)
	}
}
