//go:build linux

package isolation

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestBuildLinuxBwrapArgs_IncludesPIDNamespaceFlag verifies that the generated
// bwrap command line always includes --unshare-pid regardless of which optional
// namespace flags are active.
func TestBuildLinuxBwrapArgs_IncludesPIDNamespaceFlag(t *testing.T) {
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
	for _, tc := range []struct {
		name  string
		flags map[string]bool
	}{
		{name: "no optional flags", flags: nil},
		{name: "with ipc", flags: map[string]bool{"--unshare-ipc": true}},
		{name: "without ipc", flags: map[string]bool{"--unshare-ipc": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := buildLinuxBwrapArgs(binaryPath, binaryPath, []string{binaryPath}, root, plan, tc.flags)
			if err != nil {
				t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
			}
			if !contains(args, "--unshare-pid") {
				t.Errorf("bwrap args missing --unshare-pid: %v", args)
			}
			// --unshare-pid must appear before --proc so bwrap sets up the
			// new PID namespace before mounting the fresh procfs.
			pidIdx, procIdx := -1, -1
			for i, a := range args {
				switch a {
				case "--unshare-pid":
					pidIdx = i
				case "--proc":
					procIdx = i
				}
			}
			if pidIdx < 0 || procIdx < 0 || pidIdx > procIdx {
				t.Errorf("--unshare-pid (%d) must appear before --proc (%d): %v", pidIdx, procIdx, args)
			}
		})
	}
}

// TestProbeLinuxPIDNamespace_Success verifies that a passing probe returns nil.
func TestProbeLinuxPIDNamespace_Success(t *testing.T) {
	run := func(path string, args ...string) ([]byte, error) {
		if !contains(args, "--unshare-pid") || !contains(args, "--proc") {
			t.Fatalf("probe did not include --unshare-pid or --proc: %v", args)
		}
		return nil, nil
	}
	if err := probeLinuxPIDNamespace("/usr/bin/bwrap", run); err != nil {
		t.Fatalf("probeLinuxPIDNamespace() unexpected error = %v", err)
	}
}

// TestProbeLinuxPIDNamespace_ProcMountFailure verifies that a /proc mount
// failure (Docker default masked /proc) returns a clear, actionable error
// naming the Docker and Kubernetes fixes.
func TestProbeLinuxPIDNamespace_ProcMountFailure(t *testing.T) {
	wantErr := errors.New("exit status 1")
	output := []byte("bwrap: Can't mount proc on /proc: Operation not permitted")

	err := probeLinuxPIDNamespace("/usr/bin/bwrap", func(string, ...string) ([]byte, error) {
		return output, wantErr
	})
	if err == nil {
		t.Fatal("probeLinuxPIDNamespace() expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error should wrap the underlying error; got: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"systempaths=unconfined",
		"procMount",
		"isolation.enabled",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; got: %s", want, msg)
		}
	}
}

// TestPIDNamespaceSandboxIsolation is a behavioral integration test that
// requires a real bwrap binary and a kernel that permits PID namespaces.
// It is skipped when those conditions are not met so CI on plain runners
// stays green.
//
// The test starts a victim sleep process outside the sandbox, then runs a
// command inside bwrap with --unshare-pid and verifies that the victim PID
// is not visible from within the sandbox.
func TestPIDNamespaceSandboxIsolation(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not found:", err)
	}

	// Confirm the probe itself succeeds; if not, skip rather than fail so
	// Docker containers without systempaths=unconfined stay green.
	run := func(path string, args ...string) ([]byte, error) {
		var stderr bytes.Buffer
		cmd := exec.Command(path, args...)
		cmd.Stderr = &stderr
		e := cmd.Run()
		return stderr.Bytes(), e
	}
	if probeErr := probeLinuxPIDNamespace(bwrapPath, run); probeErr != nil {
		t.Skipf("PID namespace probe unavailable (run with --security-opt systempaths=unconfined): %v", probeErr)
	}

	// Start a victim sleep that must not be visible from inside the sandbox.
	victim := exec.Command("sleep", "600")
	if startErr := victim.Start(); startErr != nil {
		t.Fatal("start victim sleep:", startErr)
	}
	t.Cleanup(func() { _ = victim.Process.Kill(); _ = victim.Wait() })
	victimPID := victim.Process.Pid

	// Run inside the sandbox: check whether the victim proc entry exists.
	var stdout, stderr bytes.Buffer
	check := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--unshare-pid",
		"--proc", "/proc",
		"--",
		"sh", "-c", fmt.Sprintf(
			"test -e /proc/%d && echo VISIBLE || echo NOT_VISIBLE",
			victimPID,
		),
	)
	check.Stdout = &stdout
	check.Stderr = &stderr
	if runErr := check.Run(); runErr != nil {
		t.Fatalf("bwrap check failed: %v\nstderr: %s", runErr, stderr.String())
	}
	result := strings.TrimSpace(stdout.String())
	if result != "NOT_VISIBLE" {
		t.Errorf(
			"victim PID %d visible inside sandbox (got %q); PID namespace isolation not working",
			victimPID, result,
		)
	}

	// /proc/1 inside the sandbox must be bwrap, not the host init.
	var proc1out, proc1err bytes.Buffer
	proc1 := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--unshare-pid",
		"--proc", "/proc",
		"--",
		"cat", "/proc/1/comm",
	)
	proc1.Stdout = &proc1out
	proc1.Stderr = &proc1err
	if runErr := proc1.Run(); runErr != nil {
		t.Fatalf("bwrap /proc/1/comm failed: %v\nstderr: %s", runErr, proc1err.String())
	}
	comm := strings.TrimSpace(proc1out.String())
	// In a PID namespace, bwrap itself is PID 1; "systemd" or "init" means
	// we are still sharing the host PID table.
	if comm == "systemd" || comm == "init" {
		t.Errorf("/proc/1 inside sandbox reports %q; expected bwrap, not host init", comm)
	}
}
