package toolshared

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistTextArtifact_WritesAndReadsBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "artifacts")

	path, err := PersistTextArtifact(dir, "sample_*.txt", "hello artifact")
	if err != nil {
		t.Fatalf("PersistTextArtifact: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("artifact dir = %q, want %q", filepath.Dir(path), dir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if string(data) != "hello artifact" {
		t.Fatalf("artifact contents = %q", string(data))
	}
}

func TestPruneArtifacts_RemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	freshPath := filepath.Join(dir, "fresh.txt")
	for _, path := range []string{oldPath, freshPath} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	PruneArtifacts(dir, 24*time.Hour, 0)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale artifact removed, err=%v", err)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("expected fresh artifact kept, err=%v", err)
	}
}

func TestPruneArtifacts_EnforcesMaxFiles(t *testing.T) {
	dir := t.TempDir()
	for i := range 6 {
		path := filepath.Join(dir, fmt.Sprintf("a%d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		mod := time.Now().Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	PruneArtifacts(dir, time.Hour, 2)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 artifacts after prune, got %d", len(entries))
	}
	for _, name := range []string{"a4.txt", "a5.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected newest %s kept: %v", name, err)
		}
	}
}
