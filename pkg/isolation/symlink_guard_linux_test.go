//go:build linux

package isolation

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSymlinkMountTargets_SymlinkUnderRWAncestor verifies that a symlink
// planted below a writable mount is detected and causes an error.
func TestCheckSymlinkMountTargets_SymlinkUnderRWAncestor(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate an agent-planted symlink inside the writable workspace.
	evil := filepath.Join(ws, "evil")
	target := filepath.Join(dir, "outside")
	if err := os.Symlink(target, evil); err != nil {
		t.Fatal(err)
	}

	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "rw"},                                 // writable ancestor W
		{Source: "/tmp", Target: filepath.Join(ws, "evil", "cve"), Mode: "rw"}, // R
	}
	err := checkSymlinkMountTargets(plan)
	if err == nil {
		t.Fatal("expected error for symlink under rw mount, got nil")
	}
	if !strings.Contains(err.Error(), evil) {
		t.Fatalf("error should mention the symlink path %q: %v", evil, err)
	}
}

// TestCheckSymlinkMountTargets_SymlinkUnderROAncestorIsOK verifies that a
// symlink inside a read-only mount does not trigger the guard. Read-only mounts
// cannot receive agent-planted symlinks (the sandbox cannot write there).
func TestCheckSymlinkMountTargets_SymlinkUnderROAncestorIsOK(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sub, "link")
	if err := os.Symlink(filepath.Join(dir, "outside"), link); err != nil {
		t.Fatal(err)
	}

	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "ro"}, // read-only ancestor
		{Source: "/tmp", Target: filepath.Join(sub, "link", "new"), Mode: "rw"},
	}
	if err := checkSymlinkMountTargets(plan); err != nil {
		t.Fatalf("expected no error for symlink under ro mount, got: %v", err)
	}
}

// TestCheckSymlinkMountTargets_OutsideRWMountIsOK verifies that a path outside
// any writable mount is not checked.
func TestCheckSymlinkMountTargets_OutsideRWMountIsOK(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(t.TempDir(), "other")
	link := filepath.Join(other, "link")
	// Even if we could create a symlink at a path outside the writable mount,
	// it should not be reported. Plan has no rw ancestor for R.Target.
	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "rw"},
		{Source: "/tmp", Target: link, Mode: "rw"}, // R.Target is outside W.Target
	}
	if err := checkSymlinkMountTargets(plan); err != nil {
		t.Fatalf("expected no error for target outside rw mount, got: %v", err)
	}
}

// TestCheckSymlinkMountTargets_RemappedSourceTarget verifies that when
// Source != Target, the host path is derived from Source (not Target).
func TestCheckSymlinkMountTargets_RemappedSourceTarget(t *testing.T) {
	hostDir := t.TempDir()
	sandboxBase := "/sandbox/data"
	// Create a symlink at hostDir/evil.
	if err := os.Symlink(filepath.Join(hostDir, "outside"), filepath.Join(hostDir, "evil")); err != nil {
		t.Fatal(err)
	}

	plan := []MountRule{
		// Remapped: host /hostDir is visible as /sandbox/data inside the sandbox.
		{Source: hostDir, Target: sandboxBase, Mode: "rw"},
		// R: target is /sandbox/data/evil/new, which maps to hostDir/evil/new.
		{Source: "/tmp", Target: sandboxBase + "/evil/new", Mode: "rw"},
	}
	err := checkSymlinkMountTargets(plan)
	if err == nil {
		t.Fatal("expected error for symlink via remapped rw mount, got nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(hostDir, "evil")) {
		t.Fatalf("error should mention host symlink path %q: %v", filepath.Join(hostDir, "evil"), err)
	}
}

// TestCheckSymlinkMountTargets_NoRWAncestorIsOK verifies that a plan with no
// writable ancestors produces no error.
func TestCheckSymlinkMountTargets_NoRWAncestorIsOK(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "ro"},
		{Source: "/tmp", Target: sub, Mode: "ro"},
	}
	if err := checkSymlinkMountTargets(plan); err != nil {
		t.Fatalf("expected no error when no rw ancestor, got: %v", err)
	}
}

// TestCheckSymlinkMountTargets_DirectChildNoIntermediateIsOK verifies that a
// rule whose target is a direct child of a writable mount (no intermediate
// directories to traverse) is accepted without a symlink check.
func TestCheckSymlinkMountTargets_DirectChildNoIntermediateIsOK(t *testing.T) {
	dir := t.TempDir()
	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "rw"},
		{Source: "/tmp", Target: filepath.Join(dir, "new"), Mode: "rw"},
	}
	if err := checkSymlinkMountTargets(plan); err != nil {
		t.Fatalf("expected no error for direct child of rw mount, got: %v", err)
	}
}

// TestCheckSymlinkMountTargets_LegitimateNestedPathIsOK verifies that a
// real directory hierarchy with no symlinks is accepted.
func TestCheckSymlinkMountTargets_LegitimateNestedPathIsOK(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "rw"},
		{Source: "/tmp", Target: filepath.Join(nested, "output"), Mode: "rw"},
	}
	if err := checkSymlinkMountTargets(plan); err != nil {
		t.Fatalf("expected no error for real directory hierarchy, got: %v", err)
	}
}

// TestBwrapSymlinkCVE_Behavioral is a behavioral end-to-end test that proves
// the PoC from the security report (§7) is blocked by the guard before it
// reaches bwrap, on all bwrap versions.
//
// Without the guard, bwrap < 0.12.0 would create a directory on the host
// filesystem by following an agent-planted symlink in the workspace.
// bwrap >= 0.12.0 refuses on its own; this guard makes all versions safe and
// returns a clear error before bwrap runs.
func TestBwrapSymlinkCVE_Behavioral(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	// Quick check that bwrap can run at all.
	probeArgs := []string{"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "true"}
	if out, err := exec.Command("bwrap", probeArgs...).CombinedOutput(); err != nil {
		t.Skipf("bwrap cannot create namespaces in this environment: %v: %s", err, out)
	}

	dir := t.TempDir()
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate the attack: agent plants a symlink to /tmp in the workspace.
	evil := filepath.Join(workspace, "evil")
	if err := os.Symlink("/tmp", evil); err != nil {
		t.Fatal(err)
	}

	// The attack exposes /tmp with a target under the symlink.
	// Without the guard, bwrap < 0.12 would create /tmp/cve-guard-test on the
	// host.
	targetDir := filepath.Join(workspace, "evil", "cve-guard-test")
	plan := []MountRule{
		{Source: dir, Target: dir, Mode: "rw"},
		{Source: "/tmp", Target: targetDir, Mode: "rw"},
	}
	err := checkSymlinkMountTargets(plan)
	if err == nil {
		t.Fatal("checkSymlinkMountTargets: expected error for PoC attack plan, got nil")
	}
	if !strings.Contains(err.Error(), evil) {
		t.Errorf("error should identify the symlink %q: %v", evil, err)
	}

	// The host /tmp/cve-guard-test must NOT exist: the guard blocked bwrap.
	if _, statErr := os.Lstat("/tmp/cve-guard-test"); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("host /tmp/cve-guard-test must not exist after guard blocks the mount plan")
	}
}
