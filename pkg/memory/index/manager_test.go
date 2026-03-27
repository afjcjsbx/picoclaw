package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func setupTestWorkspace(t *testing.T) (string, string) {
	t.Helper()
	workspace := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.sqlite")

	// Create memory directory
	memDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatal(err)
	}

	return workspace, dbPath
}

func createFile(t *testing.T, workspace, relPath, content string) {
	t.Helper()
	absPath := filepath.Join(workspace, relPath)
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestManager_FullLifecycle(t *testing.T) {
	workspace, dbPath := setupTestWorkspace(t)
	ctx := context.Background()

	cfg := DefaultMemoryConfig()
	cfg.Storage.Path = dbPath

	// Create initial files
	createFile(t, workspace, "MEMORY.md", "# Project Memory\n\nThis is the main memory file.\n")
	createFile(t, workspace, "memory/notes.md", "# Notes\n\nSome important notes about the project.\nGolang is great.\n")
	createFile(t, workspace, "memory/api.md", "# API Reference\n\nThe API uses REST endpoints.\nAuthentication is via Bearer tokens.\n")

	mgr, err := newManager("test-agent", workspace, cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer mgr.Close()

	// Step 1: Sync
	err = mgr.Sync(ctx, SyncOptions{Reason: "initial"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// Step 2: Verify status
	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.FilesIndexed != 3 {
		t.Errorf("expected 3 files indexed, got %d", status.FilesIndexed)
	}
	if status.ChunksIndexed <= 0 {
		t.Error("expected chunks to be indexed")
	}
	if !status.FTSAvailable {
		t.Error("expected FTS to be available")
	}

	// Step 3: Search
	results, err := mgr.Search(ctx, SearchOptions{Query: "golang"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'golang'")
	}
	found := false
	for _, r := range results {
		if r.Path == "memory/notes.md" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected notes.md in results for 'golang'")
	}

	// Step 4: ReadFile
	readResult, err := mgr.ReadFile(ctx, ReadFileOptions{Path: "MEMORY.md"})
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if readResult.Text == "" {
		t.Error("expected non-empty text from ReadFile")
	}

	// Step 5: ReadFile with non-existent file
	readResult, err = mgr.ReadFile(ctx, ReadFileOptions{Path: "memory/nonexistent.md"})
	if err != nil {
		t.Fatalf("ReadFile nonexistent: %v", err)
	}
	if readResult.Text != "" {
		t.Error("expected empty text for nonexistent file")
	}

	// Step 6: Modify a file and re-sync
	createFile(t, workspace, "memory/notes.md", "# Notes Updated\n\nThe notes have been updated with new content.\nRust is also great.\n")

	err = mgr.Sync(ctx, SyncOptions{Reason: "update"})
	if err != nil {
		t.Fatalf("Sync after update: %v", err)
	}

	// Search for new content
	results, err = mgr.Search(ctx, SearchOptions{Query: "rust"})
	if err != nil {
		t.Fatalf("Search for rust: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for 'rust' after update")
	}

	// Step 7: Delete a file and re-sync
	os.Remove(filepath.Join(workspace, "memory/api.md"))

	err = mgr.Sync(ctx, SyncOptions{Reason: "delete"})
	if err != nil {
		t.Fatalf("Sync after delete: %v", err)
	}

	status, err = mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status after delete: %v", err)
	}
	if status.FilesIndexed != 2 {
		t.Errorf("expected 2 files after deletion, got %d", status.FilesIndexed)
	}

	// Search for deleted content
	results, err = mgr.Search(ctx, SearchOptions{Query: "bearer authentication"})
	if err != nil {
		t.Fatalf("Search for deleted content: %v", err)
	}
	for _, r := range results {
		if r.Path == "memory/api.md" {
			t.Error("deleted file should not appear in search results")
		}
	}
}

func TestManager_ReadFile_BoundaryEnforcement(t *testing.T) {
	workspace, dbPath := setupTestWorkspace(t)
	ctx := context.Background()

	cfg := DefaultMemoryConfig()
	cfg.Storage.Path = dbPath

	createFile(t, workspace, "MEMORY.md", "# Memory\n")

	mgr, err := newManager("test-agent", workspace, cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer mgr.Close()

	tests := []struct {
		name      string
		path      string
		wantError bool
	}{
		{"allowed_memory_md", "MEMORY.md", false},
		{"allowed_memory_subdir", "memory/test.md", false},
		{"disallowed_outside", "../../../etc/passwd", true},
		{"disallowed_non_memory", "src/main.go", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mgr.ReadFile(ctx, ReadFileOptions{Path: tt.path})
			if tt.wantError && err == nil {
				t.Error("expected error for disallowed path")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestManager_MemoryMD_Indexed(t *testing.T) {
	// Test that a memory file at the workspace root gets indexed.
	// On case-insensitive filesystems (macOS), MEMORY.md and memory.md
	// are the same file. We test that whichever name is used, it gets indexed.
	workspace, dbPath := setupTestWorkspace(t)
	ctx := context.Background()

	cfg := DefaultMemoryConfig()
	cfg.Storage.Path = dbPath

	// Create MEMORY.md with searchable content
	createFile(t, workspace, "MEMORY.md", "# Project Memory\nUnique searchable content xyzzy42.\n")

	mgr, err := newManager("test-agent", workspace, cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer mgr.Close()

	err = mgr.Sync(ctx, SyncOptions{Reason: "test"})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.FilesIndexed == 0 {
		t.Error("expected MEMORY.md to be indexed")
	}

	results, err := mgr.Search(ctx, SearchOptions{Query: "xyzzy42"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results from MEMORY.md")
	}
}

func TestSearch_QueryExpansion(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"simple", "golang programming", `"golang" AND "programming"`},
		{"stop_words", "the quick brown fox", `"quick" AND "brown" AND "fox"`},
		{"dedup", "test test test", `"test"`},
		{"mixed", "How does the API work", `"api" AND "work"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildFTSQuery(tt.query)
			if got != tt.want {
				t.Errorf("BuildFTSQuery(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestSearch_BM25ScoreNormalization(t *testing.T) {
	tests := []struct {
		name string
		rank float64
		want float64
	}{
		{"negative_high", -10.0, 10.0 / 11.0},
		{"negative_low", -0.5, 0.5 / 1.5},
		{"zero", 0, 1.0},
		{"positive", 1.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeBM25Score(tt.rank)
			if diff := got - tt.want; diff > 0.001 || diff < -0.001 {
				t.Errorf("NormalizeBM25Score(%v) = %v, want %v", tt.rank, got, tt.want)
			}
		})
	}
}

func TestSearch_SnippetTruncation(t *testing.T) {
	// 700 UTF-16 chars should not be truncated
	short := "hello world"
	if got := TruncateSnippet(short, 700); got != short {
		t.Errorf("short string should not be truncated")
	}

	// Generate a string > 700 UTF-16 code units
	long := ""
	for i := 0; i < 800; i++ {
		long += "a"
	}
	truncated := TruncateSnippet(long, 700)
	encoded := make([]uint16, 0, len(truncated))
	for _, r := range truncated {
		encoded = append(encoded, uint16(r))
	}
	if len(encoded) > 700 {
		t.Errorf("truncated snippet exceeds 700 UTF-16 units: %d", len(encoded))
	}
}
