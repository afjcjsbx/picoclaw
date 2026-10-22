//go:build linux || darwin

package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSearchFilesSkipsFIFO(t *testing.T) {
	root := t.TempDir()
	pipe := filepath.Join(root, "blocking.pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "match.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"content tree", map[string]any{"pattern": "needle", "path": "."}, "match.txt:1: needle"},
		{"content pipe", map[string]any{"pattern": "needle", "path": "blocking.pipe"}, "No matches found"},
		{"names tree", map[string]any{"pattern": "*.txt", "target": "files", "path": "."}, "match.txt"},
		{"names pipe", map[string]any{"pattern": "*", "target": "files", "path": "blocking.pipe"}, "No matching files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan *ToolResult, 1)
			go func() {
				done <- tool.Execute(ctx, tc.args)
			}()
			select {
			case result := <-done:
				if result.IsError {
					t.Fatalf("search failed: %s", result.ForLLM)
				}
				if !strings.Contains(result.ForLLM, tc.want) {
					t.Fatalf("expected %q in %q", tc.want, result.ForLLM)
				}
			case <-ctx.Done():
				t.Fatal("search blocked on FIFO after context deadline")
			}
		})
	}
}
