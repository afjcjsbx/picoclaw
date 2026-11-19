//go:build linux

package isolation

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestBuildLinuxBwrapArgs_IncludesNamespaceFlagsAndExec(t *testing.T) {
	root := t.TempDir()
	binaryDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(binaryDir, "tool")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := BuildLinuxMountPlan(root, []config.ExposePath{{Source: binaryDir, Target: binaryDir, Mode: "ro"}})
	args, err := buildLinuxBwrapArgs(
		binaryPath,
		binaryPath,
		[]string{binaryPath, "--flag"},
		root,
		plan,
		map[string]bool{"--unshare-ipc": true},
	)
	if err != nil {
		t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
	}
	hasNet := false
	hasIPC := false
	hasExec := false
	for i := range args {
		switch args[i] {
		case "--unshare-net":
			hasNet = true
		case "--unshare-ipc":
			hasIPC = true
		case "--":
			if i+1 < len(args) && args[i+1] == binaryPath {
				hasExec = true
			}
		}
	}
	if hasNet {
		t.Fatalf("bwrap args should not unshare net by default: %v", args)
	}
	if !hasIPC || !hasExec {
		t.Fatalf("bwrap args missing required items: %v", args)
	}
}

func TestBuildLinuxBwrapArgs_MountsEphemeralTmpBeforeBinds(t *testing.T) {
	root := t.TempDir()
	binaryPath := filepath.Join(root, "tool")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := []MountRule{{Source: root, Target: root, Mode: "rw"}}

	args, err := buildLinuxBwrapArgs(binaryPath, binaryPath, []string{binaryPath}, "", plan, nil)
	if err != nil {
		t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
	}

	tmpfsIndex := -1
	bindIndex := -1
	for i, arg := range args {
		switch arg {
		case "--tmpfs":
			if i+1 < len(args) && args[i+1] == "/tmp" {
				tmpfsIndex = i
			}
		case "--bind", "--ro-bind":
			if bindIndex == -1 {
				bindIndex = i
			}
		}
	}
	if tmpfsIndex == -1 {
		t.Fatalf("bwrap args missing --tmpfs /tmp: %v", args)
	}
	if bindIndex != -1 && bindIndex < tmpfsIndex {
		t.Fatalf("--tmpfs /tmp must precede bind rules so they can override it: %v", args)
	}
}

func TestAppendLinuxWorkingDirMount_UncoveredDirIsReadOnly(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	plan := []MountRule{{Source: workspace, Target: workspace, Mode: "rw"}}

	plan = appendLinuxWorkingDirMount(plan, outside)

	assertMountRule(t, plan, outside, "ro")
}

func TestAppendLinuxWorkingDirMount_InstanceRootIsReadOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := BuildLinuxMountPlan(root, nil)

	plan = appendLinuxWorkingDirMount(plan, root)

	assertMountRule(t, plan, root, "ro")
}

func TestAppendLinuxWorkingDirMount_KeepsCoveredWritableDir(t *testing.T) {
	workspace := "/home/alice/.picoclaw/workspace"
	cwd := filepath.Join(workspace, "project")
	plan := []MountRule{{Source: workspace, Target: workspace, Mode: "rw"}}

	plan = appendLinuxWorkingDirMount(plan, cwd)

	if len(plan) != 1 {
		t.Fatalf("cwd covered by an rw rule must not add another rule: %#v", plan)
	}
	if got := linuxEffectiveMountMode(plan, cwd); got != "rw" {
		t.Fatalf("effective cwd mode = %q, want rw", got)
	}
}

func TestAppendLinuxWorkingDirMount_DoesNotWidenReadOnlyParent(t *testing.T) {
	plan := []MountRule{{Source: "/usr", Target: "/usr", Mode: "ro"}}

	plan = appendLinuxWorkingDirMount(plan, "/usr/lib")

	if len(plan) != 1 {
		t.Fatalf("cwd covered by a ro rule must not gain write access: %#v", plan)
	}
	if got := linuxEffectiveMountMode(plan, "/usr/lib"); got != "ro" {
		t.Fatalf("effective cwd mode = %q, want ro", got)
	}
}

func TestAppendLinuxWorkingDirMount_LastCoveringRuleWins(t *testing.T) {
	plan := []MountRule{
		{Source: "/srv", Target: "/srv", Mode: "rw"},
		{Source: "/srv/project", Target: "/srv/project", Mode: "ro"},
	}

	plan = appendLinuxWorkingDirMount(plan, "/srv/project")

	if len(plan) != 2 {
		t.Fatalf("cwd covered by existing rules must not add another rule: %#v", plan)
	}
	if got := linuxEffectiveMountMode(plan, "/srv/project"); got != "ro" {
		t.Fatalf("effective cwd mode = %q, want ro", got)
	}
}

// TestLiveBwrap_WorkingDirOutsideWritableRootsIsReadOnly proves that a cwd
// outside the writable boundary cannot create files in the real sandbox. With
// the previous unconditional rw mount, the touch below succeeded and the file
// appeared on the host.
func TestLiveBwrap_WorkingDirOutsideWritableRootsIsReadOnly(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not found; skipping live sandbox test")
	}
	probe := exec.Command(bwrapPath,
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "true")
	if probeErr := probe.Run(); probeErr != nil {
		t.Skipf("bwrap namespace creation failed (%v); skipping live sandbox test", probeErr)
	}

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{workspace, outside} {
		if mkdirErr := os.MkdirAll(dir, 0o755); mkdirErr != nil {
			t.Fatal(mkdirErr)
		}
	}

	plan := BuildLinuxMountPlan(root, nil)
	plan = appendLinuxWorkingDirMount(plan, outside)
	namespaceFlags, nsErr := cachedLinuxNamespaceFlags(bwrapPath)
	if nsErr != nil {
		t.Skipf("namespace probe failed: %v", nsErr)
	}
	script := "touch escaped.txt; echo status:$?"
	bwrapArgs, buildErr := buildLinuxBwrapArgs(
		"/bin/sh", "/bin/sh",
		[]string{"sh", "-c", script},
		outside,
		plan,
		namespaceFlags,
	)
	if buildErr != nil {
		t.Fatalf("buildLinuxBwrapArgs: %v", buildErr)
	}

	out, runErr := exec.Command(bwrapPath, bwrapArgs[1:]...).CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if runErr != nil {
		t.Logf("bwrap exit: %v; output: %s", runErr, outStr)
	}
	if strings.Contains(outStr, "status:0") {
		t.Fatalf("cwd outside the writable boundary was writable: %s", outStr)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escaped.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("sandbox created %s in a read-only cwd", filepath.Join(outside, "escaped.txt"))
	}
}

func TestResolveLinuxWorkingDir_ResolvesRelativeDir(t *testing.T) {
	cwd := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if chdirErr := os.Chdir(previous); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	}()
	if chdirErr := os.Chdir(cwd); chdirErr != nil {
		t.Fatal(chdirErr)
	}

	resolvedDir, execDir, err := resolveLinuxWorkingDir("./hooks", "./hook.sh")
	if err != nil {
		t.Fatalf("resolveLinuxWorkingDir() error = %v", err)
	}
	want := filepath.Join(cwd, "hooks")
	if resolvedDir != want || execDir != want {
		t.Fatalf("resolveLinuxWorkingDir() = (%q, %q), want (%q, %q)", resolvedDir, execDir, want, want)
	}
}

func TestResolveLinuxCommandPath_UsesExecDirForRelativeCommand(t *testing.T) {
	execDir := filepath.Join(t.TempDir(), "hooks")
	got, err := resolveLinuxCommandPath("./hook.sh", execDir)
	if err != nil {
		t.Fatalf("resolveLinuxCommandPath() error = %v", err)
	}
	want := filepath.Join(execDir, "hook.sh")
	if got != want {
		t.Fatalf("resolveLinuxCommandPath() = %q, want %q", got, want)
	}
}

func TestBuildLinuxBwrapArgs_UsesResolvedPathForRelativeCommand(t *testing.T) {
	root := t.TempDir()
	execDir := filepath.Join(root, "hooks")
	if err := os.MkdirAll(execDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedPath := filepath.Join(execDir, "hook.sh")
	if err := os.WriteFile(resolvedPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := []MountRule{
		{Source: execDir, Target: execDir, Mode: "rw"},
		{Source: resolvedPath, Target: resolvedPath, Mode: "ro"},
	}
	args, err := buildLinuxBwrapArgs("./hook.sh", resolvedPath, []string{"./hook.sh"}, execDir, plan, nil)
	if err != nil {
		t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
	}
	hasExecDir := false
	for _, arg := range args {
		if arg == execDir {
			hasExecDir = true
			break
		}
	}
	if !hasExecDir {
		t.Fatalf("buildLinuxBwrapArgs() missing resolved chdir: %v", args)
	}
	for i := range args {
		if args[i] == "--" {
			if i+1 >= len(args) || args[i+1] != resolvedPath {
				t.Fatalf("buildLinuxBwrapArgs() exec path = %v, want %q after --", args, resolvedPath)
			}
			return
		}
	}
	t.Fatalf("buildLinuxBwrapArgs() missing exec delimiter: %v", args)
}

func TestBuildLinuxBwrapArgs_UsesNamespaceProbeResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		output  string
		runErr  error
		wantIPC bool
	}{
		{name: "supported", wantIPC: true},
		{name: "unsupported", output: "bwrap: Creating new namespace failed: Invalid argument", runErr: errors.New("exit status 1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags, err := probeLinuxNamespaceFlags("/usr/bin/bwrap", func(path string, args ...string) ([]byte, error) {
				if path != "/usr/bin/bwrap" {
					t.Fatalf("probe path = %q, want resolved bwrap path", path)
				}
				if !contains(args, "--unshare-ipc") || !contains(args, "--") || args[len(args)-1] != "true" {
					t.Fatalf("probe args = %v, want independent IPC namespace probe", args)
				}
				return []byte(tc.output), tc.runErr
			})
			if err != nil {
				t.Fatalf("probeLinuxNamespaceFlags() error = %v", err)
			}

			args, err := buildLinuxBwrapArgs("/bin/true", "/bin/true", []string{"/bin/true"}, "", nil, flags)
			if err != nil {
				t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
			}
			if got := contains(args, "--unshare-ipc"); got != tc.wantIPC {
				t.Fatalf("bwrap args IPC flag = %t, want %t: %v", got, tc.wantIPC, args)
			}
			if !tc.wantIPC {
				for _, required := range []string{"--die-with-parent", "--proc", "/proc", "--dev", "/dev"} {
					if !contains(args, required) {
						t.Errorf("bwrap args missing %q after unsupported IPC probe: %v", required, args)
					}
				}
			}
		})
	}
}

func TestProbeLinuxNamespaceFlags_OtherFailureIsHardError(t *testing.T) {
	wantErr := errors.New("permission denied")
	_, err := probeLinuxNamespaceFlags("/usr/bin/bwrap", func(string, ...string) ([]byte, error) {
		return []byte("bwrap: setting up environment failed"), wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("probeLinuxNamespaceFlags() error = %v, want wrapped %v", err, wantErr)
	}
	if strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("non-EINVAL probe failure was classified unsupported: %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAppendLinuxArgumentMounts_AddsAbsoluteArgumentPaths(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "out", "result.txt")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		t.Fatal(err)
	}

	plan := appendLinuxArgumentMounts(nil, []string{input, "--output=" + output})
	if len(plan) != 2 {
		t.Fatalf("appendLinuxArgumentMounts() len = %d, want 2", len(plan))
	}
	if plan[0].Source != input || plan[0].Mode != "ro" {
		t.Fatalf("appendLinuxArgumentMounts()[0] = %+v, want source=%q mode=ro", plan[0], input)
	}
	if plan[1].Source != filepath.Dir(output) || plan[1].Mode != "rw" {
		t.Fatalf("appendLinuxArgumentMounts()[1] = %+v, want source=%q mode=rw", plan[1], filepath.Dir(output))
	}
}
