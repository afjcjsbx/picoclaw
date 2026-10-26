//go:build !windows

package tools

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestKillProcessGroup_KillsAndIsIdempotent(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	if err := killProcessGroup(pid); err != nil {
		t.Fatalf("killProcessGroup = %v, want nil", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("wait = %v, want exit error from SIGKILL", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killed process did not exit")
	}

	// Killing an already dead process is a no-op, not a failure.
	if err := killProcessGroup(pid); err != nil {
		t.Fatalf("second killProcessGroup = %v, want nil", err)
	}
}

func TestKillProcessGroup_UnknownPIDIsNoError(t *testing.T) {
	// A pid that cannot exist (way beyond pid_max) must not be reported as a
	// kill failure: ESRCH means the desired end state already holds.
	if err := killProcessGroup(1 << 22); err != nil {
		t.Fatalf("killProcessGroup(unknown pid) = %v, want nil", err)
	}
}
