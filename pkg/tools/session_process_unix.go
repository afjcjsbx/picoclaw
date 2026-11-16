//go:build !windows

package tools

import "github.com/sipeed/picoclaw/pkg/execscope"

// killProcessGroup is the last-resort path used when a session has no scope.
func killProcessGroup(pid int) error {
	return execscope.KillProcessTree(pid)
}
