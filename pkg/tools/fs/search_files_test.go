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

func TestSearchFilesTruncationKeepsMatchVisible(t *testing.T) {
	root := t.TempDir()
	line := strings.Repeat("x", 300) + "needle" + strings.Repeat("y", 300)
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "needle") {
		t.Fatalf("a match beyond the cut must stay visible:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, strings.Repeat("x", maxSearchLineLength)+"…") {
		t.Fatalf("the window should start near the match instead of the line start:\n%s", result.ForLLM)
	}
}

func TestSearchFilesTruncationCutsLongContextLine(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 400)
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("first\n"+long+"\nneedle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle", "context": 1})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, strings.Repeat("x", maxSearchLineLength)+"…") {
		t.Fatalf("context lines should keep the head-truncated form:\n%s", result.ForLLM)
	}
}

func TestSearchFilesSkipsFilesBeyondByteBudget(t *testing.T) {
	root := t.TempDir()
	filler := []byte(strings.Repeat("a", 1_000_000))
	// 67 files x 1,000,000 bytes consume most of the 64 MiB budget.
	for i := 0; i < 67; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%02d.txt", i)), filler, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 200,000 bytes no longer fit in the ~108 KB that remain.
	if err := os.WriteFile(filepath.Join(root, "f67.txt"), []byte(strings.Repeat("b", 200_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	// This small file does fit and must still be reached.
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "z.txt:1: needle") {
		t.Fatalf("a small file after the byte budget must still be searched:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "safety limit") {
		t.Fatalf("expected the safety-limit notice when files are skipped:\n%s", result.ForLLM)
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
	if got := truncateSearchLine(short, nil); got != short {
		t.Errorf("short line changed: %q", got)
	}

	ascii := strings.Repeat("a", maxSearchLineLength+20)
	if got, want := truncateSearchLine(ascii, nil), strings.Repeat("a", maxSearchLineLength)+"\u2026"; got != want {
		t.Errorf("ASCII line cut to %q, want %q", got, want)
	}

	// The window is sized in bytes but always ends on a rune boundary.
	got := truncateSearchLine(strings.Repeat("\u6f22", maxSearchLineLength+5), nil)
	if want := strings.Repeat("\u6f22", maxSearchLineLength/3) + "\u2026"; got != want {
		t.Errorf("CJK line cut to %d runes, want %d", utf8.RuneCountInString(got), maxSearchLineLength/3+1)
	}
	if !utf8.ValidString(got) {
		t.Errorf("CJK line cut mid-rune: %q", got)
	}

	// A match past the cut moves the window so the matched text stays visible.
	long := strings.Repeat("x", 400) + "needle" + strings.Repeat("y", 100)
	window := truncateSearchLine(long, []int{400, 406})
	if !strings.Contains(window, "needle") {
		t.Errorf("match beyond the cut was not kept visible: %q", window)
	}
	if len(window) > maxSearchLineLength+2*len("\u2026") {
		t.Errorf("window larger than allowed: %d bytes", len(window))
	}
}
