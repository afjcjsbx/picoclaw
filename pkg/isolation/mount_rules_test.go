package isolation

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAppendLinuxCommandMountRules_RegularFile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "tool")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := appendLinuxCommandMountRules(nil, binary)
	assertMountRule(t, plan, binary, "ro")
	assertMountRule(t, plan, dir, "ro")
}

func TestAppendLinuxCommandMountRules_SkipsSymlinkDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs extra privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "sh")
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}

	plan := appendLinuxCommandMountRules([]MountRule{{Source: dir, Target: dir, Mode: "ro"}}, link)
	assertNoMountRule(t, plan, link)
	assertMountRule(t, plan, dir, "ro")
	assertMountRule(t, plan, canonicalPath(t, target), "ro")
}

func TestAppendLinuxCommandMountRules_ResolvesTargetOutsideParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs extra privileges on Windows")
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realDir, "tool")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(binDir, "tool")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	plan := appendLinuxCommandMountRules([]MountRule{{Source: binDir, Target: binDir, Mode: "ro"}}, link)
	assertNoMountRule(t, plan, link)
	assertMountRule(t, plan, binDir, "ro")
	assertMountRule(t, plan, canonicalPath(t, target), "ro")
	assertMountRule(t, plan, canonicalPath(t, realDir), "ro")
}

func TestAppendLinuxCommandMountRules_BrokenSymlinkKeepsParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs extra privileges on Windows")
	}

	dir := t.TempDir()
	link := filepath.Join(dir, "missing")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), link); err != nil {
		t.Fatal(err)
	}

	plan := appendLinuxCommandMountRules(nil, link)
	assertNoMountRule(t, plan, link)
	assertMountRule(t, plan, dir, "ro")
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return resolved
}

func assertMountRule(t *testing.T, plan []MountRule, target, mode string) {
	t.Helper()
	for _, rule := range plan {
		if filepath.Clean(rule.Target) == filepath.Clean(target) {
			if rule.Mode != mode {
				t.Fatalf("mount rule %q mode = %q, want %q", target, rule.Mode, mode)
			}
			return
		}
	}
	t.Fatalf("mount plan %#v missing target %q", plan, target)
}

func assertNoMountRule(t *testing.T, plan []MountRule, target string) {
	t.Helper()
	for _, rule := range plan {
		if filepath.Clean(rule.Target) == filepath.Clean(target) {
			t.Fatalf("mount plan %#v must not mount symlink destination %q", plan, target)
		}
	}
}
