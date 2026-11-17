package instancepath

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunRecoveryPreservesLiveRuns(t *testing.T) {
	base := t.TempDir()
	live, err := newRun(base)
	if err != nil {
		t.Fatal(err)
	}
	defer live.lock.Close()
	dead, err := newRun(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dead.path, "orphan"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dead.lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := recoverRuns(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(live.path); err != nil {
		t.Fatalf("live run removed: %v", err)
	}
	if _, err := os.Stat(dead.path); !os.IsNotExist(err) {
		t.Fatalf("abandoned run remains: %v", err)
	}
}

func TestRecoveryAfterProcessExit(t *testing.T) {
	if base := os.Getenv("PICOCLAW_TEST_RUN_BASE"); base != "" {
		run, err := newRun(base)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(run.path, "unfinished"), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Deliberately leave the file and lock behind, as a crash would.
		os.Exit(0)
	}
	base := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryAfterProcessExit$")
	cmd.Env = append(os.Environ(), "PICOCLAW_TEST_RUN_BASE="+base)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v: %s", err, out)
	}
	if err := recoverRuns(base); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatalf("crash files remain: %v, %v", entries, err)
	}
}

func TestRecoveryIgnoresForeignAndSymlinkedDirectories(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "run-symlink")); err != nil {
		t.Skip(err)
	}
	if err := os.Mkdir(filepath.Join(base, "run-no-marker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := recoverRuns(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "run-no-marker")); err != nil {
		t.Fatal(err)
	}
}
