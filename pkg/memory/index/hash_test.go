package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	content := []byte("hello world\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	hash, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}

	// Same content → same hash
	hash2, _ := HashFile(path)
	if hash != hash2 {
		t.Errorf("expected same hash, got %s and %s", hash, hash2)
	}
}

func TestHashFile_NotFound(t *testing.T) {
	_, err := HashFile("/nonexistent/file.md")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestHashContent(t *testing.T) {
	h1 := HashContent([]byte("hello"))
	h2 := HashContent([]byte("hello"))
	h3 := HashContent([]byte("world"))

	if h1 != h2 {
		t.Error("same content should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d chars", len(h1))
	}
}

func TestHashChunkText(t *testing.T) {
	h := HashChunkText("some chunk text")
	if h == "" || len(h) != 64 {
		t.Errorf("expected 64-char hex hash, got %q", h)
	}
}

func TestGenerateChunkID(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		path      string
		startLine int
		endLine   int
		hash      string
		model     string
	}{
		{"basic", "memory", "test.md", 0, 5, "abc", ""},
		{"with_model", "memory", "test.md", 0, 5, "abc", "text-embedding-3-small"},
		{"sessions", "sessions", "session.jsonl", 10, 20, "def", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id1 := GenerateChunkID(tt.source, tt.path, tt.startLine, tt.endLine, tt.hash, tt.model)
			id2 := GenerateChunkID(tt.source, tt.path, tt.startLine, tt.endLine, tt.hash, tt.model)

			if id1 != id2 {
				t.Error("same inputs should produce same ID")
			}
			if len(id1) != 64 {
				t.Errorf("expected 64-char hex ID, got %d chars", len(id1))
			}
		})
	}

	// Different inputs → different IDs
	id1 := GenerateChunkID("memory", "a.md", 0, 5, "abc", "")
	id2 := GenerateChunkID("memory", "b.md", 0, 5, "abc", "")
	if id1 == id2 {
		t.Error("different paths should produce different IDs")
	}
}

func TestHashSessionFile(t *testing.T) {
	h1 := HashSessionFile("content", []int{1, 3, 5})
	h2 := HashSessionFile("content", []int{1, 3, 5})
	h3 := HashSessionFile("content", []int{1, 3, 6})

	if h1 != h2 {
		t.Error("same inputs should produce same hash")
	}
	if h1 == h3 {
		t.Error("different lineMap should produce different hash")
	}
}
