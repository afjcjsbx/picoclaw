package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchFilesStopsAcrossDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"0.txt", "1.txt"} {
			if err := os.WriteFile(filepath.Join(root, dir, name), []byte("needle\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	tool := NewSearchFilesTool(root, true)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"content", map[string]any{"pattern": "needle", "limit": 1}},
		{"names", map[string]any{"pattern": "*.txt", "target": "files", "limit": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), tc.args)
			if result.IsError {
				t.Fatalf("search failed: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "2 files scanned.") {
				t.Fatalf("search continued into the next directory: %s", result.ForLLM)
			}
		})
	}
}
