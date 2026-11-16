//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func setSysProcAttrForPty(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Setsid and Setpgid conflict. Mutate the existing struct so fields added
	// by the process scope (for example UseCgroupFD) survive.
	cmd.SysProcAttr.Setpgid = false
	cmd.SysProcAttr.Setsid = true
}
