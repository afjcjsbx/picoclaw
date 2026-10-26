//go:build !windows

package tools

import (
	"errors"
	"fmt"
	"syscall"
)

// killProcessGroup kills the process group led by pid, falling back to the
// process itself when the group signal fails. It reports a non-nil error only
// when the process could neither be signaled as a group nor individually:
// ESRCH means the target is already gone, which is the intended end state.
func killProcessGroup(pid int) error {
	groupErr := syscall.Kill(-pid, syscall.SIGKILL)
	if groupErr == nil {
		return nil
	}

	// The process may not have been started in its own process group.
	procErr := syscall.Kill(pid, syscall.SIGKILL)
	if procErr == nil {
		return nil
	}
	if errors.Is(groupErr, syscall.ESRCH) && errors.Is(procErr, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("failed to kill process %d: %w", pid, errors.Join(groupErr, procErr))
}
