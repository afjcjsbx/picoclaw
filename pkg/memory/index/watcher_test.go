package index

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWatcher_DetectsFileChanges(t *testing.T) {
	workspace := t.TempDir()
	memDir := filepath.Join(workspace, "memory")
	os.MkdirAll(memDir, 0755)

	// Create initial file
	testFile := filepath.Join(memDir, "test.md")
	os.WriteFile(testFile, []byte("initial"), 0644)

	var syncCount atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := NewWatcher(ctx, WatcherConfig{
		WorkspaceDir: workspace,
		DebounceMs:   100, // Short debounce for testing
		Logger:       slog.Default(),
		OnSync: func() {
			syncCount.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// Modify the file
	time.Sleep(200 * time.Millisecond)
	os.WriteFile(testFile, []byte("modified"), 0644)

	// Wait for debounce + processing
	time.Sleep(500 * time.Millisecond)

	if syncCount.Load() == 0 {
		t.Error("expected at least one sync trigger after file modification")
	}
}

func TestWatcher_IgnoresNonMemoryFiles(t *testing.T) {
	workspace := t.TempDir()
	memDir := filepath.Join(workspace, "memory")
	os.MkdirAll(memDir, 0755)

	// Create a memory file to make the dir watchable
	os.WriteFile(filepath.Join(memDir, "keep.md"), []byte("keep"), 0644)

	var syncCount atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := NewWatcher(ctx, WatcherConfig{
		WorkspaceDir: workspace,
		DebounceMs:   100,
		Logger:       slog.Default(),
		OnSync: func() {
			syncCount.Add(1)
		},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	// Create a non-memory file at workspace root
	time.Sleep(200 * time.Millisecond)
	os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main"), 0644)

	time.Sleep(500 * time.Millisecond)

	if syncCount.Load() > 0 {
		t.Error("should not trigger sync for non-memory files")
	}
}

func TestWatcher_ContextCancellation(t *testing.T) {
	workspace := t.TempDir()
	memDir := filepath.Join(workspace, "memory")
	os.MkdirAll(memDir, 0755)

	ctx, cancel := context.WithCancel(context.Background())

	w, err := NewWatcher(ctx, WatcherConfig{
		WorkspaceDir: workspace,
		DebounceMs:   100,
		Logger:       slog.Default(),
		OnSync:       func() {},
	})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	// Cancel should stop the watcher cleanly
	cancel()
	time.Sleep(200 * time.Millisecond)

	// Close should not panic or block
	err = w.Close()
	if err != nil {
		t.Errorf("Close: %v", err)
	}
}
