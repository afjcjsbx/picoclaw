//go:build !windows

package tools

// killProcessGroup kills the process group led by pid and any descendant that
// escaped it by calling setsid() or setpgid(). It reports a non-nil error only
// when the process could neither be signaled as a group nor individually:
// ESRCH means the target is already gone, which is the intended end state.
func killProcessGroup(pid int) error {
	return killProcessTree(pid)
}
