//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func prepareCommandForTermination(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}

	// Kill the process group and every descendant that changed session or
	// process group (for example with setsid).
	_ = killProcessTree(pid)
	// Fallback kill on the shell process itself.
	_ = cmd.Process.Kill()
	return nil
}
