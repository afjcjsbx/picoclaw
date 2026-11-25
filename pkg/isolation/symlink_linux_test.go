//go:build linux

package isolation

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLinuxExecDir_ResolvesSymlink(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(filepath.Dir(realDir), "resolvetest-link-"+filepath.Base(realDir))
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(link)

	got := resolveLinuxExecDir(link)
	if got != realDir {
		t.Fatalf("resolveLinuxExecDir(%q) = %q, want %q", link, got, realDir)
	}
}

func TestResolveLinuxExecDir_NonSymlinkUnchanged(t *testing.T) {
	realDir := t.TempDir()
	got := resolveLinuxExecDir(realDir)
	if got != realDir {
		t.Fatalf("resolveLinuxExecDir(%q) = %q, want unchanged", realDir, got)
	}
}

func TestResolveLinuxExecDir_EmptyIsEmpty(t *testing.T) {
	if got := resolveLinuxExecDir(""); got != "" {
		t.Fatalf("resolveLinuxExecDir(\"\") = %q, want \"\"", got)
	}
}

// TestSymlinkedCwd_PlanAndArgs verifies that a symlinked working directory
// does not appear in the mount plan and that --chdir uses the resolved path.
// This is the unit-level proof that the applyPlatformIsolation fix is correct:
// bwrap >= 0.12 rejects symlink targets ("Can't mount on symlink destination"),
// so the mount plan and --chdir must reference the real directory only.
func TestSymlinkedCwd_PlanAndArgs(t *testing.T) {
	realDir := t.TempDir()
	link := filepath.Join(filepath.Dir(realDir), "cwdtest-link-"+filepath.Base(realDir))
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(link)

	// Write a minimal executable under the real directory.
	exe := filepath.Join(realDir, "tool")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	// resolveLinuxExecDir must return the real path.
	resolvedExecDir := resolveLinuxExecDir(link)
	if resolvedExecDir != realDir {
		t.Fatalf("resolveLinuxExecDir(%q) = %q, want %q", link, resolvedExecDir, realDir)
	}

	// Build the mount plan exactly as applyPlatformIsolation does post-fix.
	plan := BuildLinuxMountPlan(root, nil)
	plan = appendLinuxCommandMountRules(plan, exe)
	plan = appendLinuxWorkingDirMount(plan, resolvedExecDir)

	// No mount rule should reference the symlink path.
	for _, rule := range plan {
		if rule.Source == link || rule.Target == link {
			t.Errorf("mount plan contains symlink path %q in rule %+v", link, rule)
		}
	}

	// Build the bwrap arg list; --chdir must be the resolved path.
	args, err := buildLinuxBwrapArgs(exe, exe, []string{exe}, resolvedExecDir, plan, nil)
	if err != nil {
		t.Fatalf("buildLinuxBwrapArgs: %v", err)
	}
	for i, a := range args {
		if a == "--chdir" && i+1 < len(args) {
			if args[i+1] != realDir {
				t.Errorf("--chdir = %q, want resolved path %q", args[i+1], realDir)
			}
		}
		if a == link || strings.HasPrefix(a, link+string(filepath.Separator)) {
			t.Errorf("bwrap arg %q references symlink path %q", a, link)
		}
	}
}

// TestBwrap_SymlinkedCwd_ResolvedPathInSandbox is a behavioral test that
// actually runs bwrap with a symlinked cwd. It proves that the resolved-path
// approach works across bwrap versions (pre-fix, bwrap 0.12 would fail with
// "Can't mount on symlink destination"). Skipped when bwrap is not available
// or cannot create user namespaces (plain CI runners without unprivileged
// user namespaces).
func TestBwrap_SymlinkedCwd_ResolvedPathInSandbox(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not found in PATH")
	}
	// Quick probe: abort if the kernel rejects user namespaces.
	probe := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "true",
	)
	if probeErr := probe.Run(); probeErr != nil {
		t.Skipf("bwrap namespace probe failed (kernel may lack unprivileged user namespaces): %v", probeErr)
	}

	realDir := t.TempDir()
	link := filepath.Join(filepath.Dir(realDir), "bwraptest-link-"+filepath.Base(realDir))
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(link)

	root := t.TempDir()
	resolvedDir := resolveLinuxExecDir(link)
	if resolvedDir != realDir {
		t.Fatalf("resolveLinuxExecDir(%q) = %q, want %q", link, resolvedDir, realDir)
	}

	plan := BuildLinuxMountPlan(root, nil)
	plan = appendLinuxWorkingDirMount(plan, resolvedDir)

	namespaceFlags, flagErr := probeLinuxNamespaceFlags(bwrapPath, func(path string, fargs ...string) ([]byte, error) {
		var stderr bytes.Buffer
		c := exec.Command(path, fargs...)
		c.Stderr = &stderr
		return stderr.Bytes(), c.Run()
	})
	if flagErr != nil {
		t.Skipf("namespace flag probe failed: %v", flagErr)
	}

	args, buildErr := buildLinuxBwrapArgs(
		"/bin/sh", "/bin/sh",
		[]string{"/bin/sh", "-c", "pwd -P"},
		resolvedDir,
		plan,
		namespaceFlags,
	)
	if buildErr != nil {
		t.Fatalf("buildLinuxBwrapArgs: %v", buildErr)
	}

	cmd := exec.Command(bwrapPath, args[1:]...)
	out, runErr := cmd.Output()
	if runErr != nil {
		t.Fatalf("bwrap with resolved symlink cwd failed: %v", runErr)
	}
	gotPwd := strings.TrimSpace(string(out))
	if gotPwd != realDir {
		t.Errorf("pwd -P inside sandbox = %q, want resolved path %q", gotPwd, realDir)
	}
}
