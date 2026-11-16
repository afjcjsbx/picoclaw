//go:build windows

package tools

import (
	"os/exec"
	"strconv"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

func prepareCommandForTermination(cmd *exec.Cmd) {
	// no-op on Windows
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}

	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	_ = cmd.Process.Kill()
	// A docker-backed command needs its container removed explicitly.
	isolation.CleanupPID(pid)
	return nil
}
