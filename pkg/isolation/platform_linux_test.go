//go:build linux

package isolation

import (
	"errors"
	"os"
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
