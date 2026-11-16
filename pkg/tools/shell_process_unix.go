//go:build !windows

package tools

import (
	"os/exec"
	"syscall"

	"github.com/sipeed/picoclaw/pkg/execscope"
)

func prepareCommandForTermination(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	// Merge fields rather than replacing the struct: cgroup scope setup may
	// have added UseCgroupFD/CgroupFD before this point.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
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
	_ = execscope.KillProcessTree(pid)
	// Fallback kill on the shell process itself.
	_ = cmd.Process.Kill()
	return nil
}
