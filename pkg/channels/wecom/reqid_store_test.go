package wecom

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReqIDStoreUsesInstanceHome(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("PICOCLAW_HOME", first)
	store, err := newReqIDStore("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("chat", "route", 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first, "wecom", "reqid-store.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PICOCLAW_HOME", second)
	other, err := newReqIDStore("")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := other.Get("chat"); ok {
		t.Fatal("routing state leaked between instances")
	}
}

func TestReqIDStoreReportsCorruptState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newReqIDStore(path); err == nil {
		t.Fatal("corrupt routing state was silently discarded")
	}
}

func TestReqIDStorePersistsRoutes(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "reqids.json")
	store, err := newReqIDStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("chat-1", "req-1", 2, time.Hour); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	reloaded, err := newReqIDStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	route, ok := reloaded.Get("chat-1")
	if !ok {
		t.Fatal("expected persisted route to be loaded")
	}
	if route.ChatID != "chat-1" || route.ReqID != "req-1" || route.ChatType != 2 {
		t.Fatalf("loaded route = %+v", route)
	}
}
