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

// seedArtifactDir creates n artifact files with fresh modification times, so
// only the max-files retention rule can remove them.
func seedArtifactDir(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := range n {
		path := filepath.Join(dir, fmt.Sprintf("seed_%03d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}
	return dir
}

// forgetArtifactPruneState drops the process-global throttle entry for dir so
// the test does not inherit a prune from an earlier PersistTextArtifact call.
func forgetArtifactPruneState(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() { artifactPruneStates.Delete(dir) })
}

func persistArtifact(t *testing.T, dir string) {
	t.Helper()
	if _, err := PersistTextArtifact(dir, "artifact_*.txt", "payload"); err != nil {
		t.Fatalf("PersistTextArtifact: %v", err)
	}
}

func countArtifacts(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	return len(entries)
}

// TestPersistTextArtifact_PrunesAfterBurstWrite covers the write-count arm of
// the throttle: the first write prunes, the next artifactPruneMaxWrites writes
// skip the scan, and the following write clears the backlog.
func TestPersistTextArtifact_PrunesAfterBurstWrite(t *testing.T) {
	dir := seedArtifactDir(t, DefaultArtifactMaxFiles+72)
	forgetArtifactPruneState(t, dir)

	persistArtifact(t, dir)
	if got := countArtifacts(t, dir); got != DefaultArtifactMaxFiles {
		t.Fatalf("after first write: %d artifacts, want %d", got, DefaultArtifactMaxFiles)
	}

	for i := 1; i <= artifactPruneMaxWrites; i++ {
		persistArtifact(t, dir)
		want := DefaultArtifactMaxFiles + i
		if got := countArtifacts(t, dir); got != want {
			t.Fatalf("write %d after prune: %d artifacts, want %d (scan should be skipped)", i+1, got, want)
		}
	}

	persistArtifact(t, dir)
	if got := countArtifacts(t, dir); got != DefaultArtifactMaxFiles {
		t.Fatalf("burst prune: %d artifacts, want %d", got, DefaultArtifactMaxFiles)
	}
}

// TestPersistTextArtifact_PrunesAfterInterval covers the time arm of the
// throttle: a directory that grew while the scan was skipped is pruned again
// once artifactPruneMinInterval has elapsed.
func TestPersistTextArtifact_PrunesAfterInterval(t *testing.T) {
	dir := seedArtifactDir(t, DefaultArtifactMaxFiles+72)
	forgetArtifactPruneState(t, dir)

	persistArtifact(t, dir)
	if got := countArtifacts(t, dir); got != DefaultArtifactMaxFiles {
		t.Fatalf("after first write: %d artifacts, want %d", got, DefaultArtifactMaxFiles)
	}

	// Within the interval the scan is skipped even when the directory is over
	// the retention target.
	for i := range 10 {
		path := filepath.Join(dir, fmt.Sprintf("extra_%02d.txt", i))
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	persistArtifact(t, dir)
	if got, want := countArtifacts(t, dir), DefaultArtifactMaxFiles+11; got != want {
		t.Fatalf("within interval: %d artifacts, want %d (scan should be skipped)", got, want)
	}

	// Pretend the interval elapsed instead of sleeping for a minute.
	value, ok := artifactPruneStates.Load(dir)
	if !ok {
		t.Fatal("missing prune state for the test directory")
	}
	state := value.(*artifactPruneState)
	state.mu.Lock()
	state.last = time.Now().Add(-2 * artifactPruneMinInterval)
	state.mu.Unlock()

	persistArtifact(t, dir)
	if got := countArtifacts(t, dir); got != DefaultArtifactMaxFiles {
		t.Fatalf("after interval: %d artifacts, want %d", got, DefaultArtifactMaxFiles)
	}
}
