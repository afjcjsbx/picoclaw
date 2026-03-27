package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempDBPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test.sqlite")
}

func TestOpenStore_CreatesDB(t *testing.T) {
	path := tempDBPath(t)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("database file was not created")
	}
}

func TestStore_FTSAvailable(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	if !s.FTSAvailable() {
		t.Fatal("expected FTS5 to be available with modernc/sqlite")
	}
}

func TestStore_MetaGetSet(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"simple", "test_key", "test_value"},
		{"json", "meta_json", `{"model":"text-embedding-3-small"}`},
		{"empty_value", "empty", ""},
		{"overwrite", "test_key", "new_value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.SetMeta(tt.key, tt.value); err != nil {
				t.Fatalf("SetMeta: %v", err)
			}
			got, err := s.GetMeta(tt.key)
			if err != nil {
				t.Fatalf("GetMeta: %v", err)
			}
			if got != tt.value {
				t.Errorf("got %q, want %q", got, tt.value)
			}
		})
	}
}

func TestStore_MetaGetMissing(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	got, err := s.GetMeta("nonexistent")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing key, got %q", got)
	}
}

func TestStore_FilesCRUD(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	f := FileRecord{
		Path:   "memory/test.md",
		Source: "memory",
		Hash:   "abc123",
		Mtime:  now,
		Size:   1024,
	}

	// Upsert
	if err := s.UpsertFile(f); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}

	// Get
	got, err := s.GetFile(f.Path)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got == nil {
		t.Fatal("GetFile returned nil")
	}
	if got.Hash != f.Hash {
		t.Errorf("hash: got %q, want %q", got.Hash, f.Hash)
	}

	// List
	files, err := s.ListFiles()
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("ListFiles: got %d files, want 1", len(files))
	}

	// Delete
	if err := s.DeleteFile(f.Path); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	got, err = s.GetFile(f.Path)
	if err != nil {
		t.Fatalf("GetFile after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestStore_ChunksCRUD(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	chunks := []Chunk{
		{ID: "c1", Path: "test.md", Source: "memory", StartLine: 0, EndLine: 5, Hash: "h1", Model: "", Text: "hello world", Embedding: "", UpdatedAt: now},
		{ID: "c2", Path: "test.md", Source: "memory", StartLine: 6, EndLine: 10, Hash: "h2", Model: "", Text: "foo bar", Embedding: "", UpdatedAt: now},
	}

	if err := s.UpsertChunks(chunks); err != nil {
		t.Fatalf("UpsertChunks: %v", err)
	}

	count, err := s.CountChunks()
	if err != nil {
		t.Fatalf("CountChunks: %v", err)
	}
	if count != 2 {
		t.Errorf("CountChunks: got %d, want 2", count)
	}

	byPath, err := s.ListChunksByPath("test.md")
	if err != nil {
		t.Fatalf("ListChunksByPath: %v", err)
	}
	if len(byPath) != 2 {
		t.Errorf("ListChunksByPath: got %d, want 2", len(byPath))
	}

	// Delete by path
	if err := s.DeleteChunksByPath("test.md"); err != nil {
		t.Fatalf("DeleteChunksByPath: %v", err)
	}
	count, _ = s.CountChunks()
	if count != 0 {
		t.Errorf("expected 0 chunks after delete, got %d", count)
	}
}

func TestStore_FTSSearch(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	if !s.FTSAvailable() {
		t.Skip("FTS5 not available")
	}

	now := time.Now().UnixMilli()
	chunks := []Chunk{
		{ID: "c1", Path: "test.md", Source: "memory", StartLine: 0, EndLine: 5, Hash: "h1", Text: "golang programming language", UpdatedAt: now},
		{ID: "c2", Path: "test.md", Source: "memory", StartLine: 6, EndLine: 10, Hash: "h2", Text: "python scripting language", UpdatedAt: now},
		{ID: "c3", Path: "test.md", Source: "memory", StartLine: 11, EndLine: 15, Hash: "h3", Text: "rust systems programming", UpdatedAt: now},
	}
	if err := s.UpsertChunks(chunks); err != nil {
		t.Fatalf("UpsertChunks: %v", err)
	}

	results, ranks, err := s.SearchFTS(`"golang"`, 10)
	if err != nil {
		t.Fatalf("SearchFTS: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'golang', got %d", len(results))
	}
	if len(ranks) != len(results) {
		t.Error("ranks length mismatch")
	}
	if len(results) > 0 && results[0].ID != "c1" {
		t.Errorf("expected chunk c1, got %s", results[0].ID)
	}
}

func TestStore_EmbeddingCache(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	entries := []EmbeddingCacheEntry{
		{Provider: "openai", Model: "text-embedding-3-small", ProviderKey: "key1", Hash: "h1", Embedding: "[0.1,0.2]", Dims: 2, UpdatedAt: now},
		{Provider: "openai", Model: "text-embedding-3-small", ProviderKey: "key1", Hash: "h2", Embedding: "[0.3,0.4]", Dims: 2, UpdatedAt: now + 1},
	}

	if err := s.SetCachedEmbeddings(entries); err != nil {
		t.Fatalf("SetCachedEmbeddings: %v", err)
	}

	count, err := s.CountCacheEntries()
	if err != nil {
		t.Fatalf("CountCacheEntries: %v", err)
	}
	if count != 2 {
		t.Errorf("CountCacheEntries: got %d, want 2", count)
	}

	cached, err := s.GetCachedEmbeddings("openai", "text-embedding-3-small", "key1", []string{"h1", "h2", "h3"})
	if err != nil {
		t.Fatalf("GetCachedEmbeddings: %v", err)
	}
	if len(cached) != 2 {
		t.Errorf("expected 2 cached entries, got %d", len(cached))
	}
	if _, ok := cached["h1"]; !ok {
		t.Error("expected h1 in cache")
	}
	if _, ok := cached["h3"]; ok {
		t.Error("h3 should not be in cache")
	}

	// Evict
	if err := s.EvictOldestCacheEntries(1); err != nil {
		t.Fatalf("EvictOldestCacheEntries: %v", err)
	}
	count, _ = s.CountCacheEntries()
	if count != 1 {
		t.Errorf("expected 1 entry after eviction, got %d", count)
	}
}

func TestStore_CountChunksBySource(t *testing.T) {
	s, err := OpenStore(tempDBPath(t))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	now := time.Now().UnixMilli()
	chunks := []Chunk{
		{ID: "c1", Path: "a.md", Source: "memory", StartLine: 0, EndLine: 1, Hash: "h1", Text: "text1", UpdatedAt: now},
		{ID: "c2", Path: "b.md", Source: "memory", StartLine: 0, EndLine: 1, Hash: "h2", Text: "text2", UpdatedAt: now},
		{ID: "c3", Path: "c.jsonl", Source: "sessions", StartLine: 0, EndLine: 1, Hash: "h3", Text: "text3", UpdatedAt: now},
	}
	if err := s.UpsertChunks(chunks); err != nil {
		t.Fatalf("UpsertChunks: %v", err)
	}

	counts, err := s.CountChunksBySource()
	if err != nil {
		t.Fatalf("CountChunksBySource: %v", err)
	}
	if counts["memory"] != 2 {
		t.Errorf("memory count: got %d, want 2", counts["memory"])
	}
	if counts["sessions"] != 1 {
		t.Errorf("sessions count: got %d, want 1", counts["sessions"])
	}
}
