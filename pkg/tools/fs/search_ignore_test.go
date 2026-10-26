package fstools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIgnoreTestFile(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchFilesRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	writeIgnoreTestFile(t, root, map[string]string{
		".gitignore":     "vendor/\n",
		"vendor/dep.txt": "needle\n",
		"main.go":        "needle\n",
	})
	tool := NewSearchFilesTool(root, true)

	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "vendor") {
		t.Fatalf("ignored vendor/ should not be searched:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "main.go") {
		t.Fatalf("expected main.go in output:\n%s", result.ForLLM)
	}

	result = tool.Execute(context.Background(), map[string]any{"pattern": "*.txt", "target": "files"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "dep.txt") {
		t.Fatalf("ignored files should not be listed:\n%s", result.ForLLM)
	}

	// An explicitly targeted file is always searched, even below ignored paths.
	result = tool.Execute(context.Background(), map[string]any{"pattern": "needle", "path": "vendor/dep.txt"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "dep.txt:1: needle") {
		t.Fatalf("explicitly targeted file should be searched:\n%s", result.ForLLM)
	}
}

func TestSearchFilesGitignoreNegation(t *testing.T) {
	root := t.TempDir()
	writeIgnoreTestFile(t, root, map[string]string{
		".gitignore": "*.log\n!keep.log\n",
		"a.log":      "needle\n",
		"keep.log":   "needle\n",
		"readme.txt": "needle\n",
	})
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "a.log") {
		t.Fatalf("ignored a.log should not be searched:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "keep.log") {
		t.Fatalf("negated keep.log should be searched:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "readme.txt") {
		t.Fatalf("expected readme.txt in output:\n%s", result.ForLLM)
	}
}

func TestSearchFilesGitignoreAnchoredRule(t *testing.T) {
	root := t.TempDir()
	writeIgnoreTestFile(t, root, map[string]string{
		".gitignore":           "/build\n",
		"build/root.txt":       "needle\n",
		"sub/build/nested.txt": "needle\n",
	})
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "build/root.txt") {
		t.Fatalf("anchored /build should ignore the root build dir:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "sub/build/nested.txt") {
		t.Fatalf("anchored /build should not ignore nested build dirs:\n%s", result.ForLLM)
	}
}

func TestSearchFilesNestedGitignoreOverrides(t *testing.T) {
	root := t.TempDir()
	writeIgnoreTestFile(t, root, map[string]string{
		".gitignore":      "*.tmp\n",
		"one.tmp":         "needle\n",
		"sub/special.tmp": "needle\n",
		"sub/.gitignore":  "!special.tmp\n",
	})
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "one.tmp") {
		t.Fatalf("one.tmp should be ignored:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "sub/special.tmp") {
		t.Fatalf("nested negation should re-include special.tmp:\n%s", result.ForLLM)
	}
}

func TestSearchFilesRespectsIgnoreFile(t *testing.T) {
	root := t.TempDir()
	writeIgnoreTestFile(t, root, map[string]string{
		".ignore":    "secret.txt\n",
		"secret.txt": "needle\n",
		"other.txt":  "needle\n",
	})
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "secret.txt") {
		t.Fatalf(".ignore entries should be skipped:\n%s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "other.txt") {
		t.Fatalf("expected other.txt in output:\n%s", result.ForLLM)
	}
}

// statCountFs counts Stat calls so tests can assert on syscall traffic.
type statCountFs struct {
	fileSystem
	stats []string
}

func (f *statCountFs) Stat(path string) (fs.FileInfo, error) {
	f.stats = append(f.stats, filepath.Base(path))
	return f.fileSystem.Stat(path)
}

func TestLoadSearchIgnoreSetOnlyTouchesListedFiles(t *testing.T) {
	root := t.TempDir()
	writeIgnoreTestFile(t, root, map[string]string{".gitignore": "*.log\n", "a.go": ""})
	counting := &statCountFs{fileSystem: &hostFs{}}
	entries, err := counting.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	set, ok := loadSearchIgnoreSet(counting, root, entries)
	if !ok || len(set.rules) != 1 {
		t.Fatalf("expected the .gitignore rule to load, got ok=%v rules=%d", ok, len(set.rules))
	}
	if len(counting.stats) != 1 || counting.stats[0] != ".gitignore" {
		t.Fatalf("only the listed .gitignore should be stat'ed, got %v", counting.stats)
	}
}
