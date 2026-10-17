package fstools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

func TestSearchFilesScansFilesLargerThanOneMiB(t *testing.T) {
	root := t.TempDir()
	var content strings.Builder
	for content.Len() < 2*1024*1024 {
		content.WriteString("lorem ipsum dolor sit amet consectetur adipiscing elit sed\n")
	}
	content.WriteString("needle\n")
	if err := os.WriteFile(filepath.Join(root, "big.log"), []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "big.log:") || !strings.Contains(result.ForLLM, "needle") {
		t.Fatalf("matches in a file larger than 1 MiB must be found:\n%s", result.ForLLM)
	}

	result = tool.Execute(context.Background(), map[string]any{"pattern": "needle", "output_mode": "count"})
	if result.IsError {
		t.Fatalf("count search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "big.log: 1") {
		t.Fatalf("count mode must scan the whole large file:\n%s", result.ForLLM)
	}
}

func TestSearchFilesSearchesNonUTF8Text(t *testing.T) {
	root := t.TempDir()
	// Latin-1 text: 0xE9 is "é" in ISO-8859-1 and invalid UTF-8.
	if err := os.WriteFile(filepath.Join(root, "latin1.txt"), []byte("caf\xe9 needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "latin1.txt:1:") || !strings.Contains(result.ForLLM, "needle") {
		t.Fatalf("text with invalid UTF-8 must be searched:\n%s", result.ForLLM)
	}
}

func TestSearchFilesSkipsFilesWithNULInHead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte("needle\x00after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "needle") {
		t.Fatalf("a file with a NUL byte in its head is binary and must be skipped:\n%s", result.ForLLM)
	}
}

func TestSearchFilesSearchesNULBeyondHeadSniff(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("a", 2*scanSearchBinarySniffBytes) + "\x00needle\n"
	if err := os.WriteFile(filepath.Join(root, "deep.bin"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "deep.bin:") || !strings.Contains(result.ForLLM, "needle") {
		t.Fatalf("only the head of a file decides binary detection:\n%s", result.ForLLM)
	}
}

func TestSearchFilesContextMergesAdjacentMatches(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 50; i++ {
		switch {
		case i == 10 || i == 11:
			lines = append(lines, fmt.Sprintf("needle line %d", i))
		case i == 40:
			lines = append(lines, "needle far away")
		default:
			lines = append(lines, fmt.Sprintf("filler %d", i))
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle", "context": 2})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	// Lines 10 and 11 merge into one block; line 40 is its own block.
	if got := strings.Count(result.ForLLM, "a.txt:\n"); got != 2 {
		t.Fatalf("expected 2 merged blocks, got %d:\n%s", got, result.ForLLM)
	}
	for _, line := range []string{"  8: filler 8", "  9: filler 9", "> 10: needle line 10", "> 11: needle line 11", "  12: filler 12"} {
		if got := strings.Count(result.ForLLM, line+"\n"); got != 1 {
			t.Fatalf("line %q should appear exactly once, got %d:\n%s", line, got, result.ForLLM)
		}
	}
	if !strings.Contains(result.ForLLM, "2 result(s)") {
		t.Fatalf("merged blocks should be counted as results:\n%s", result.ForLLM)
	}
}

func TestSearchFilesContextPaginationWithoutDuplication(t *testing.T) {
	root := t.TempDir()
	const total = 4000
	lines := make([]string, total)
	for i := range lines {
		lines[i] = fmt.Sprintf("needle%04d %s", i+1, strings.Repeat("x", 220))
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	seen := make(map[string]int)
	offset := 0
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("pagination did not terminate")
		}
		result := tool.Execute(context.Background(), map[string]any{
			"pattern": "needle", "limit": 50, "offset": offset, "context": 20,
		})
		if result.IsError {
			t.Fatalf("search failed: %s", result.ForLLM)
		}
		if len(result.ForLLM) > maxSearchOutputBytes {
			t.Fatalf("output has %d bytes, limit is %d", len(result.ForLLM), maxSearchOutputBytes)
		}
		if !strings.Contains(result.ForLLM, "result(s)") {
			break // no more blocks
		}
		var shown int
		summary := result.ForLLM[strings.LastIndex(result.ForLLM, "\n\n")+2:]
		if _, err := fmt.Sscanf(summary, "%d result(s)", &shown); err != nil {
			t.Fatal(err)
		}
		if shown == 0 {
			break
		}
		for _, match := range regexp.MustCompile(`needle\d{4}`).FindAllString(result.ForLLM, -1) {
			seen[match]++
		}
		offset += shown
	}
	if len(seen) != total {
		t.Fatalf("saw %d distinct matches, want %d", len(seen), total)
	}
	for match, count := range seen {
		if count != 1 {
			t.Fatalf("match %s appeared %d times across pages", match, count)
		}
	}
}

// searchStatCounter wraps a fileSystem and records every stat issued through
// the interface and through files opened by it, so tests can assert that the
// search does not stat the same path twice.
type searchStatCounter struct {
	fileSystem
	stats map[string]int
}

func newSearchStatCounter(inner fileSystem) *searchStatCounter {
	return &searchStatCounter{fileSystem: inner, stats: map[string]int{}}
}

func (c *searchStatCounter) Stat(path string) (fs.FileInfo, error) {
	c.stats[path]++
	return c.fileSystem.Stat(path)
}

func (c *searchStatCounter) Open(path string) (fs.File, error) {
	f, err := c.fileSystem.Open(path)
	if err != nil {
		return nil, err
	}
	return &searchStatCounterFile{File: f, counter: c, path: path}, nil
}

type searchStatCounterFile struct {
	fs.File
	counter *searchStatCounter
	path    string
}

func (f *searchStatCounterFile) Stat() (fs.FileInfo, error) {
	f.counter.stats[f.path]++
	return f.File.Stat()
}

func TestSearchFilesStatsEachFileOnceForContent(t *testing.T) {
	root := t.TempDir()
	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	counter := newSearchStatCounter(&hostFs{})
	tool := &SearchFilesTool{fs: counter}
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle", "path": root})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "3 files scanned.") {
		t.Fatalf("expected all files scanned: %s", result.ForLLM)
	}
	for _, name := range files {
		if got := counter.stats[filepath.Join(root, name)]; got != 1 {
			t.Errorf("expected exactly 1 stat for %s, got %d", name, got)
		}
	}
	// The root and the .gitignore/.ignore probes are each stat'ed once, so no
	// path may be stat'ed twice over the whole search.
	for path, count := range counter.stats {
		if count > 1 {
			t.Errorf("path %s was stat'ed %d times", path, count)
		}
	}
}

func TestSearchFilesDiscoveryOrderSkipsFileStats(t *testing.T) {
	root := t.TempDir()
	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	counter := newSearchStatCounter(&hostFs{})
	tool := &SearchFilesTool{fs: counter}
	result := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.txt", "target": "files", "path": root,
	})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	for _, name := range files {
		if got := counter.stats[filepath.Join(root, name)]; got != 0 {
			t.Errorf("discovery order must not stat %s, got %d stats", name, got)
		}
	}

	counter = newSearchStatCounter(&hostFs{})
	tool = &SearchFilesTool{fs: counter}
	result = tool.Execute(context.Background(), map[string]any{
		"pattern": "*.txt", "target": "files", "path": root, "order": "modified",
	})
	if result.IsError {
		t.Fatalf("modified search failed: %s", result.ForLLM)
	}
	for _, name := range files {
		if got := counter.stats[filepath.Join(root, name)]; got != 1 {
			t.Errorf("modified order needs exactly 1 stat per file for %s, got %d", name, got)
		}
	}
}
