package fstools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSearchFilesOutputCapKeepsPagination(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("needle%03d %s", i+1, strings.Repeat("x", 220))
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	offset := 0
	for offset < len(lines) {
		result := tool.Execute(context.Background(), map[string]any{
			"pattern": "needle", "limit": 50, "offset": offset, "context": 20,
		})
		if result.IsError {
			t.Fatalf("search failed: %s", result.ForLLM)
		}
		if len(result.ForLLM) > maxSearchOutputBytes {
			t.Fatalf("output has %d bytes, limit is %d", len(result.ForLLM), maxSearchOutputBytes)
		}
		var shown int
		summary := result.ForLLM[strings.LastIndex(result.ForLLM, "\n\n")+2:]
		if _, err := fmt.Sscanf(summary, "%d result(s)", &shown); err != nil {
			t.Fatal(err)
		}
		blocks := strings.Count(result.ForLLM, "a.txt:\n")
		if shown == 0 || shown != blocks {
			t.Fatalf("reported %d results, output contains %d: %s", shown, blocks, result.ForLLM)
		}
		offset += shown
		if offset < len(lines) && !strings.Contains(result.ForLLM, fmt.Sprintf("offset=%d", offset)) {
			t.Fatalf("missing next offset %d", offset)
		}
	}
	if offset != len(lines) {
		t.Fatalf("pagination advanced to %d, want %d", offset, len(lines))
	}
}

func TestTruncateSearchLine(t *testing.T) {
	short := "a plain line"
	if got := truncateSearchLine(short); got != short {
		t.Errorf("short line changed: %q", got)
	}

	ascii := strings.Repeat("a", maxSearchLineLength+20)
	if got, want := truncateSearchLine(ascii), strings.Repeat("a", maxSearchLineLength)+"\u2026"; got != want {
		t.Errorf("ASCII line cut to %q, want %q", got, want)
	}

	got := truncateSearchLine(strings.Repeat("\u6f22", maxSearchLineLength+5))
	if want := strings.Repeat("\u6f22", maxSearchLineLength) + "\u2026"; got != want {
		t.Errorf("CJK line cut to %d runes, want %d", utf8.RuneCountInString(got), maxSearchLineLength+1)
	}
	if !utf8.ValidString(got) {
		t.Errorf("CJK line cut mid-rune: %q", got)
	}
}
