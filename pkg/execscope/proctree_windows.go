//go:build windows

package execscope

import (
	"os/exec"
	"strconv"
)

// killProcessTree uses taskkill /T to walk the Windows process tree. Job
// Objects are the stronger long-term backend; this keeps the fallback
// consistent with the existing exec behavior.
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	return nil
}
