package fstools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/media"
)

func TestProtectedWorkspaceReads(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{".env", ".env.local", ".envrc", ".security.yml", ".netrc"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("TOP_SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(workspace, ".aws"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".aws", "credentials"), []byte("TOP_SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "normal.txt"), []byte("public"), 0o600); err != nil {
		t.Fatal(err)
	}
	protectedPaths := []string{".env", ".env.local", ".envrc", ".security.yml", ".netrc", ".aws/credentials"}
	if err := os.Symlink(".env", filepath.Join(workspace, "alias")); err == nil {
		protectedPaths = append(protectedPaths, "alias")
	}

	for _, restrict := range []bool{false, true} {
		for _, name := range protectedPaths {
			path := filepath.Join(workspace, name)
			tool := NewReadFileTool(workspace, restrict, MaxReadFileSize, []*regexp.Regexp{regexp.MustCompile(".*")})
			result := tool.Execute(context.Background(), map[string]any{"path": path})
			if !result.IsError || !strings.Contains(result.ForLLM, "access denied") ||
				strings.Contains(result.ForLLM, "TOP_SECRET") {
				t.Fatalf("read_file(%q, restrict=%v) = %+v", name, restrict, result)
			}
		}
	}

	read := NewReadFileTool(workspace, true, MaxReadFileSize)
	result := read.Execute(context.Background(), map[string]any{"path": "normal.txt"})
	if result.IsError || !strings.Contains(result.ForLLM, "public") {
		t.Fatalf("ordinary read failed: %+v", result)
	}
	linesResult := NewReadFileLinesTool(
		workspace,
		true,
		MaxReadFileSize,
	).Execute(context.Background(), map[string]any{"path": ".env"})
	if !linesResult.IsError {
		t.Fatalf("line-based read exposed .env: %+v", linesResult)
	}
	list := NewListDirTool(workspace, true)
	result = list.Execute(context.Background(), map[string]any{"path": "."})
	if result.IsError || !strings.Contains(result.ForLLM, "normal.txt") || strings.Contains(result.ForLLM, ".env") ||
		strings.Contains(result.ForLLM, ".aws") ||
		strings.Contains(result.ForLLM, "alias") {
		t.Fatalf("directory listing exposed protected paths: %+v", result)
	}
	if result := list.Execute(context.Background(), map[string]any{"path": ".aws"}); !result.IsError {
		t.Fatalf("protected directory was listed: %+v", result)
	}

	search := NewSearchFilesTool(workspace, true)
	for _, args := range []map[string]any{
		{"pattern": "TOP_SECRET"},
		{"pattern": "*", "target": "files"},
	} {
		result := search.Execute(context.Background(), args)
		if result.IsError || strings.Contains(result.ForLLM, "TOP_SECRET") || strings.Contains(result.ForLLM, ".env") ||
			strings.Contains(result.ForLLM, ".aws") ||
			strings.Contains(result.ForLLM, "alias") {
			t.Fatalf("search exposed protected paths: %+v", result)
		}
	}
	for _, target := range []string{"content", "files"} {
		pattern := "TOP_SECRET"
		if target == "files" {
			pattern = "*"
		}
		result := search.Execute(
			context.Background(),
			map[string]any{"path": ".env", "pattern": pattern, "target": target},
		)
		if !result.IsError {
			t.Fatalf("direct search of protected path succeeded: %+v", result)
		}
	}
}

func TestProtectedFilesRemainWritableButNotEditableByReading(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".env")
	write := NewWriteFileTool(workspace, true)
	if result := write.Execute(
		context.Background(),
		map[string]any{"path": path, "content": "TOKEN=old"},
	); result.IsError {
		t.Fatalf("create .env: %+v", result)
	}
	if result := write.Execute(
		context.Background(),
		map[string]any{"path": path, "content": "TOKEN=new"},
	); !result.IsError {
		t.Fatalf("overwrite without flag succeeded: %+v", result)
	}
	for _, tool := range []interface {
		Execute(ctx context.Context, args map[string]any) *ToolResult
	}{
		NewEditFileTool(workspace, true), NewAppendFileTool(workspace, true),
	} {
		result := tool.Execute(
			context.Background(),
			map[string]any{"path": path, "old_text": "old", "new_text": "new", "content": "more"},
		)
		if !result.IsError || !strings.Contains(result.ForLLM, "access denied") {
			t.Fatalf("read-modify-write exposed .env: %+v", result)
		}
	}
}

func TestProtectedFilesCannotBeSentAsMedia(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".env"), []byte("TOP_SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := media.NewFileMediaStore()
	send := NewSendFileTool(workspace, true, 0, store)
	send.SetContext("test", "chat")
	load := NewLoadImageTool(workspace, true, 0, store)
	load.SetContext("test", "chat")
	for _, tool := range []interface {
		Execute(ctx context.Context, args map[string]any) *ToolResult
	}{send, load} {
		result := tool.Execute(context.Background(), map[string]any{"path": ".env"})
		if !result.IsError || !strings.Contains(result.ForLLM, "access denied") {
			t.Fatalf("media tool accepted .env: %+v", result)
		}
	}
}
