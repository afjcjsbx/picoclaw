package cron

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreReportsCorruptStore(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, []byte(`{"jobs":[`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := (fileStore{path: storePath}).List(context.Background()); err == nil {
		t.Fatal("List() on a corrupt store returned no error")
	}
}
