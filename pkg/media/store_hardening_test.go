package media

import (
	"os"
	"testing"
	"time"
)

func TestStoreRejectsInvalidCleanupPolicy(t *testing.T) {
	dir := t.TempDir()
	store := NewFileMediaStore()
	path := createTempFile(t, dir, "bad.jpg")

	if _, err := store.Store(path, MediaMeta{CleanupPolicy: "bogus"}, "scope"); err == nil {
		t.Fatal("expected error for invalid cleanup policy, got nil")
	}
}

func TestResolveRefreshesIdleTTL(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	store := newTestStoreWithCleanup(10 * time.Minute)
	store.nowFunc = func() time.Time { return now }

	path := createTempFile(t, dir, "idle.jpg")
	ref, err := store.Store(path, MediaMeta{Source: "test"}, "scope")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	now = now.Add(20 * time.Minute)

	if _, err := store.Resolve(ref); err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if removed := store.CleanExpired(); removed != 0 {
		t.Fatalf("expected 0 removed after idle refresh, got %d", removed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should remain after idle TTL refresh: %v", err)
	}
}
