//go:build linux

package execscope

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestGCStaleCgroupsRemovesDeadInstance(t *testing.T) {
	root := t.TempDir()
	start, _ := processStartTime(os.Getpid())
	if start == "" {
		start = "0"
	}
	current := filepath.Join(root, fmt.Sprintf("picoclaw-%d-%s-current", os.Getpid(), start))
	stale := filepath.Join(root, "picoclaw-999999-0-stale")
	if err := os.Mkdir(current, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "cgroup.events"), []byte("populated 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	gcStaleCgroups(root, current)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale instance cgroup still exists: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("current instance cgroup was removed: %v", err)
	}
}

func TestCgroupParsers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte("populated 1\nfrozen 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	populated, err := cgroupPopulated(dir)
	if err != nil {
		t.Fatalf("cgroupPopulated() error = %v", err)
	}
	if !populated {
		t.Fatal("cgroupPopulated() = false, want true")
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte("42\n43\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := cgroupProcs(dir)
	if len(got) != 2 || got[0] != 42 || got[1] != 43 {
		t.Fatalf("cgroupProcs() = %v, want [42 43]", got)
	}
}

func TestMountOptionAndUnescape(t *testing.T) {
	if !hasMountOption("rw,nosuid,nodev", "rw") {
		t.Fatal("hasMountOption() did not find rw")
	}
	if hasMountOption("ro,nosuid", "rw") {
		t.Fatal("hasMountOption() found rw in a read-only option list")
	}
	if got := unescapeMountInfo(`/path\040with\040spaces`); got != "/path with spaces" {
		t.Fatalf("unescapeMountInfo() = %q", got)
	}
}
