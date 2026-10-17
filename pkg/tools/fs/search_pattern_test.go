package fstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchPatternMatch(t *testing.T) {
	cases := []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"", "anything/x.go", true},
		{"docker-compose", "docker-compose.yml", true},
		{"docker-compose", "deploy/docker-compose.prod.yml", true},
		{"docker-compose", "compose.yml", false},
		{"README", "docs/README.md", true},
		{"*.go", "pkg/main.go", true},
		{"*.go", "main.go", true},
		{"*.go", "main.py", false},
		{"src/*.ts", "src/a.ts", true},
		{"src/*.ts", "src/nested/a.ts", false},
		{"src/**/*.ts", "src/a.ts", true},
		{"src/**/*.ts", "src/nested/deep/a.ts", true},
		{"src/**/*.ts", "lib/a.ts", false},
		{"**/*.md", "a/b/c.md", true},
		{"**/*.md", "c.md", true},
		{"*.{js,ts}", "app.ts", true},
		{"*.{js,ts}", "app.js", true},
		{"*.{js,ts}", "app.py", false},
		{"{src,lib}/**", "src/a/b.ts", true},
		{"{src,lib}/**", "lib/x.ts", true},
		{"{src,lib}/**", "docs/x.ts", false},
		{"deploy/*.yml", "deploy/app.yml", true},
		{"deploy/*.yml", "other/app.yml", false},
		{"[abc]*.txt", "a1.txt", true},
		{"[!abc]*.txt", "d1.txt", true},
		{"[!abc]*.txt", "a1.txt", false},
	}
	for _, tc := range cases {
		pattern, err := newSearchPattern(tc.pattern)
		if err != nil {
			t.Fatalf("newSearchPattern(%q): %v", tc.pattern, err)
		}
		if got := pattern.match(tc.rel); got != tc.want {
			t.Errorf("pattern %q match(%q) = %v, want %v", tc.pattern, tc.rel, got, tc.want)
		}
	}
}

func TestSearchPatternInvalidGlob(t *testing.T) {
	if _, err := newSearchPattern("[z-a]"); err == nil {
		t.Fatal("expected an error for an invalid character class")
	}
}

func TestSearchFilesNamePatterns(t *testing.T) {
	root := t.TempDir()
	files := []string{
		"docker-compose.yml",
		"deploy/docker-compose.prod.yml",
		"deploy/notes.md",
		"src/app.ts",
		"src/nested/deep.ts",
		"src/app.py",
	}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewSearchFilesTool(root, true)
	cases := []struct {
		name    string
		pattern string
		want    []string
		absent  []string
	}{
		{
			name:    "substring finds nested and flat",
			pattern: "docker-compose",
			want:    []string{"docker-compose.yml", "deploy/docker-compose.prod.yml"},
			absent:  []string{"notes.md"},
		},
		{
			name:    "empty pattern lists files",
			pattern: "",
			want:    []string{"docker-compose.yml", "deploy/notes.md", "src/app.py"},
		},
		{
			name:    "recursive glob crosses directories",
			pattern: "src/**/*.ts",
			want:    []string{"src/app.ts", "src/nested/deep.ts"},
			absent:  []string{"app.py"},
		},
		{
			name:    "braces expand alternatives",
			pattern: "*.{yml,md}",
			want:    []string{"docker-compose.yml", "deploy/notes.md"},
			absent:  []string{"app.ts"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), map[string]any{
				"pattern": tc.pattern,
				"target":  "files",
				"limit":   200,
			})
			if result.IsError {
				t.Fatalf("search failed: %s", result.ForLLM)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.ForLLM, want) {
					t.Errorf("expected %q in output:\n%s", want, result.ForLLM)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(result.ForLLM, absent) {
					t.Errorf("did not expect %q in output:\n%s", absent, result.ForLLM)
				}
			}
		})
	}
}

func TestSearchFilesEmptyContentPattern(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{"pattern": ""})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "a.txt:1: one") || !strings.Contains(result.ForLLM, "a.txt:2: two") {
		t.Fatalf("empty pattern should match every line:\n%s", result.ForLLM)
	}
}

func TestSearchFilesFileGlobMatchesRelativePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"src/app.ts", "src/nested/deep.ts", "src/app.py"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewSearchFilesTool(root, true)
	result := tool.Execute(context.Background(), map[string]any{
		"pattern":   "needle",
		"file_glob": "src/**/*.ts",
	})
	if result.IsError {
		t.Fatalf("search failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "src/app.ts") || !strings.Contains(result.ForLLM, "src/nested/deep.ts") {
		t.Fatalf("expected both .ts files:\n%s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "app.py") {
		t.Fatalf("file_glob should exclude app.py:\n%s", result.ForLLM)
	}
}
