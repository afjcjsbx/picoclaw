//go:build !mipsle && !netbsd && !(freebsd && arm)

package dashboardauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorePasswordLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	wantPath := filepath.Join(dir, DBFilename)
	if got := store.DBPath(); got != wantPath {
		t.Fatalf("DBPath() = %q, want %q", got, wantPath)
	}

	ctx := context.Background()
	initialized, err := store.IsInitialized(ctx)
	if err != nil {
		t.Fatalf("IsInitialized() before setup error = %v", err)
	}
	if initialized {
		t.Fatal("IsInitialized() = true before setup")
	}
	ok, err := store.VerifyPassword(ctx, "first-password")
	if err != nil {
		t.Fatalf("VerifyPassword() before setup error = %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword() = true before setup")
	}
	if setErr := store.SetPassword(ctx, ""); setErr == nil {
		t.Fatal("SetPassword(empty) error = nil, want error")
	}

	if setErr := store.SetPassword(ctx, "first-password"); setErr != nil {
		t.Fatalf("SetPassword() error = %v", setErr)
	}
	initialized, err = store.IsInitialized(ctx)
	if err != nil || !initialized {
		t.Fatalf("IsInitialized() after setup = %v, %v; want true, nil", initialized, err)
	}
	assertPassword(t, ctx, store, "first-password", true)
	assertPassword(t, ctx, store, "wrong-password", false)

	raw, err := os.ReadFile(store.DBPath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(raw), "first-password") {
		t.Fatal("password database contains plaintext password")
	}

	if closeErr := store.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	reopened, err := Open(store.DBPath())
	if err != nil {
		t.Fatalf("Open() after Close error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertPassword(t, ctx, reopened, "first-password", true)

	if setErr := reopened.SetPassword(ctx, "second-password"); setErr != nil {
		t.Fatalf("SetPassword(change) error = %v", setErr)
	}
	assertPassword(t, ctx, reopened, "first-password", false)
	assertPassword(t, ctx, reopened, "second-password", true)
}

func TestStoreOpenFailures(t *testing.T) {
	t.Run("missing parent", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing", DBFilename)
		if _, err := Open(path); err == nil {
			t.Fatal("Open() error = nil, want error")
		}
	})

	t.Run("corrupt database", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), DBFilename)
		if err := os.WriteFile(path, []byte("not a sqlite database"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Fatal("Open() error = nil, want error")
		}
	})
}

func TestStoreClosedAndCanceledContext(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	ctx := context.Background()
	if _, initErr := store.IsInitialized(ctx); initErr == nil {
		t.Fatal("IsInitialized() on closed store error = nil")
	}
	if setErr := store.SetPassword(ctx, "password"); setErr == nil {
		t.Fatal("SetPassword() on closed store error = nil")
	}
	if _, verifyErr := store.VerifyPassword(ctx, "password"); verifyErr == nil {
		t.Fatal("VerifyPassword() on closed store error = nil")
	}

	open, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = open.Close() })
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, initErr := open.IsInitialized(canceled); !errors.Is(initErr, context.Canceled) {
		t.Fatalf("IsInitialized(canceled) error = %v, want context.Canceled", initErr)
	}
	if _, verifyErr := open.VerifyPassword(canceled, "password"); !errors.Is(verifyErr, context.Canceled) {
		t.Fatalf("VerifyPassword(canceled) error = %v, want context.Canceled", verifyErr)
	}
}

func assertPassword(t *testing.T, ctx context.Context, store *Store, password string, want bool) {
	t.Helper()
	got, err := store.VerifyPassword(ctx, password)
	if err != nil {
		t.Fatalf("VerifyPassword(%q) error = %v", password, err)
	}
	if got != want {
		t.Fatalf("VerifyPassword(%q) = %v, want %v", password, got, want)
	}
}
