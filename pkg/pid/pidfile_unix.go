//go:build !windows

package pid

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// isProcessRunning checks whether a process with the given PID is alive
// on Unix-like systems using signal(0).
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal(0) does not kill the process but checks existence on Unix.
	err = p.Signal(syscall.Signal(0))
	if err != nil {
		var errno syscall.Errno
		// EPERM means the process exists but we are not allowed to signal it.
		if !errors.As(err, &errno) || errno != syscall.EPERM {
			return false
		}
	}
	// On Linux, /proc/<pid> also exposes non-leader threads of a process.
	// A stale PID file from a previous boot can match a thread ID of the
	// live gateway (Tgid != Pid), making it look like a second gateway is
	// already running and blocking every restart. Only accept PIDs that
	// are thread group leaders.
	if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		if tgid, ok := procStatusTgid(status); ok {
			return tgid == pid
		}
	}
	return true
}

// procStatusTgid extracts the Tgid field from /proc/<pid>/status content.
func procStatusTgid(status []byte) (int, bool) {
	for _, line := range strings.Split(string(status), "\n") {
		value, found := strings.CutPrefix(line, "Tgid:")
		if !found {
			continue
		}
		tgid, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, false
		}
		return tgid, true
	}
	return 0, false
}

// isPicoclawProcess reads /proc/<pid>/comm to confirm the process name
// contains "picoclaw". Returns false when the comm file can be read and
// the name does not match (e.g., PID was reused by an unrelated process).
// Returns true if /proc/<pid>/comm is unreadable so the call site falls
// back to trusting the liveness check alone.
func isPicoclawProcess(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return true // cannot verify — trust liveness check
	}
	return strings.Contains(strings.TrimSpace(string(data)), "picoclaw")
}
