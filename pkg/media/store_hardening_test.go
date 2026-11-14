package media

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestPinnedMediaSurvivesExpiryAndScopeRelease(t *testing.T) {
	store := newTestStoreWithCleanup(time.Minute)
	now := time.Now()
	store.nowFunc = func() time.Time { return now }
	path := createTempFile(t, t.TempDir(), "active.png")
	ref, err := store.Store(path, MediaMeta{}, "turn")
	if err != nil {
		t.Fatal(err)
	}
	unpin := PinRefs(store, ref)
	now = now.Add(time.Hour)
	if n := store.CleanExpired(); n != 0 {
		t.Fatalf("expired active media: %d", n)
	}
	if err := store.ReleaseAll("turn"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed pinned file: %v", err)
	}
	unpin()
	unpin() // Release handles must be idempotent.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file remains after reader finished: %v", err)
	}
}

func TestFailedMediaDeletionIsRetried(t *testing.T) {
	store := NewFileMediaStore()
	path := createTempFile(t, t.TempDir(), "retry.png")
	if _, err := store.Store(path, MediaMeta{}, "turn"); err != nil {
		t.Fatal(err)
	}
	store.removeFile = func(string) error { return os.ErrPermission }
	if err := store.ReleaseAll("turn"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
	if len(store.pendingDeletes) != 1 {
		t.Fatal("failed deletion was forgotten")
	}
	store.removeFile = os.Remove
	store.CleanExpired()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("retry did not remove file: %v", err)
	}
}

func TestReregisteredFileCancelsPendingDeletion(t *testing.T) {
	store := NewFileMediaStore()
	path := createTempFile(t, t.TempDir(), "retry.png")
	if _, err := store.Store(path, MediaMeta{}, "old"); err != nil {
		t.Fatal(err)
	}
	store.removeFile = func(string) error { return os.ErrPermission }
	_ = store.ReleaseAll("old")
	if _, err := store.Store(path, MediaMeta{CleanupPolicy: CleanupPolicyForgetOnly}, "borrowed"); err != nil {
		t.Fatal(err)
	}
	store.removeFile = os.Remove
	store.CleanExpired()
	if err := store.ReleaseAll("borrowed"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("borrowed file removed: %v", err)
	}
}

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
