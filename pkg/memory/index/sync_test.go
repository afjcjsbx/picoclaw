package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSync_IncrementalAddChangeDelete(t *testing.T) {
	workspace, dbPath := setupTestWorkspace(t)
	ctx := context.Background()

	cfg := DefaultMemoryConfig()
	cfg.Storage.Path = dbPath
	cfg.Sync.Watch = false

	createFile(t, workspace, "memory/a.md", "# File A\nOriginal content alpha.\n")
	createFile(t, workspace, "memory/b.md", "# File B\nOriginal content beta.\n")

	mgr, err := newManager("test", workspace, cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer mgr.Close()

	// Initial sync
	if err := mgr.Sync(ctx, SyncOptions{Reason: "init"}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	s1, _ := mgr.Status(ctx)
	if s1.FilesIndexed != 2 {
		t.Errorf("expected 2 files, got %d", s1.FilesIndexed)
	}

	// Add a file
	createFile(t, workspace, "memory/c.md", "# File C\nNew content gamma.\n")
	if err := mgr.Sync(ctx, SyncOptions{Reason: "add"}); err != nil {
		t.Fatalf("sync after add: %v", err)
	}
	s2, _ := mgr.Status(ctx)
	if s2.FilesIndexed != 3 {
		t.Errorf("expected 3 files after add, got %d", s2.FilesIndexed)
	}

	// Modify a file
	createFile(t, workspace, "memory/a.md", "# File A\nModified content alpha updated.\n")
	if err := mgr.Sync(ctx, SyncOptions{Reason: "change"}); err != nil {
		t.Fatalf("sync after change: %v", err)
	}
	results, _ := mgr.Search(ctx, SearchOptions{Query: "modified updated"})
	if len(results) == 0 {
		t.Error("expected results for modified content")
	}

	// Delete a file
	os.Remove(filepath.Join(workspace, "memory/b.md"))
	if err := mgr.Sync(ctx, SyncOptions{Reason: "delete"}); err != nil {
		t.Fatalf("sync after delete: %v", err)
	}
	s3, _ := mgr.Status(ctx)
	if s3.FilesIndexed != 2 {
		t.Errorf("expected 2 files after delete, got %d", s3.FilesIndexed)
	}
}

func TestSync_ForceFullReindex(t *testing.T) {
	workspace, dbPath := setupTestWorkspace(t)
	ctx := context.Background()

	cfg := DefaultMemoryConfig()
	cfg.Storage.Path = dbPath
	cfg.Sync.Watch = false

	createFile(t, workspace, "memory/test.md", "# Test\nForce reindex content.\n")

	mgr, err := newManager("test", workspace, cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	defer mgr.Close()

	// Initial sync
	if err := mgr.Sync(ctx, SyncOptions{Reason: "init"}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// Force reindex
	if err := mgr.Sync(ctx, SyncOptions{Reason: "force", Force: true}); err != nil {
		t.Fatalf("force reindex: %v", err)
	}

	s, _ := mgr.Status(ctx)
	if s.FilesIndexed != 1 {
		t.Errorf("expected 1 file after reindex, got %d", s.FilesIndexed)
	}
	if s.ChunksIndexed <= 0 {
		t.Error("expected chunks after reindex")
	}
}

func TestSync_ReindexOnConfigChange(t *testing.T) {
	workspace, dbPath := setupTestWorkspace(t)
	ctx := context.Background()

	cfg := DefaultMemoryConfig()
	cfg.Storage.Path = dbPath
	cfg.Sync.Watch = false

	createFile(t, workspace, "memory/test.md", "# Test\nConfig change content.\n")

	// First sync with default config
	mgr, err := newManager("test", workspace, cfg)
	if err != nil {
		t.Fatalf("newManager: %v", err)
	}
	if err := mgr.Sync(ctx, SyncOptions{Reason: "init"}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	s1, _ := mgr.Status(ctx)
	mgr.Close()

	// Second sync with changed chunk tokens (triggers reindex)
	cfg2 := cfg
	cfg2.Chunking.Tokens = 200
	mgr2, err := newManager("test", workspace, cfg2)
	if err != nil {
		t.Fatalf("newManager with new config: %v", err)
	}
	defer mgr2.Close()

	if err := mgr2.Sync(ctx, SyncOptions{Reason: "config-change"}); err != nil {
		t.Fatalf("sync with config change: %v", err)
	}
	s2, _ := mgr2.Status(ctx)

	// Should still have files indexed (reindex rebuilds)
	if s2.FilesIndexed != 1 {
		t.Errorf("expected 1 file after reindex, got %d", s2.FilesIndexed)
	}
	_ = s1 // both syncs should succeed
}
