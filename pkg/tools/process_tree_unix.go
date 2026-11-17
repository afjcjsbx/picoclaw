//go:build !windows

package tools

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

const (
	descendantScanAttempts = 5
	descendantScanDelay    = 10 * time.Millisecond
)

// killProcessTree kills the process group led by pid and, before that, every
// descendant that is still reachable through the parent/child relationship.
//
// A process that calls setsid() or setpgid() leaves the shell's process group,
// so a plain kill(-pid) cannot reach it. Descendants are enumerated while the
// shell is still alive, because once the shell dies they are reparented to init
// (or launchd) and the ancestry link is lost.
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}

	// Keep the full snapshot, not just the last scan: a descendant forked during
	// the scan loop can be reparented when the shell dies, so sweep it again
	// after the group signal.
	seen := make(map[int]struct{})
	for attempt := 0; attempt < descendantScanAttempts; attempt++ {
		descendants := descendantPIDs(pid)
		if len(descendants) == 0 {
			break
		}
		for _, descendant := range descendants {
			seen[descendant] = struct{}{}
			_ = syscall.Kill(descendant, syscall.SIGKILL)
		}
		time.Sleep(descendantScanDelay)
	}

	groupErr := syscall.Kill(-pid, syscall.SIGKILL)
	procErr := syscall.Kill(pid, syscall.SIGKILL)
	for descendant := range seen {
		_ = syscall.Kill(descendant, syscall.SIGKILL)
	}
	if groupErr == nil || procErr == nil {
		return nil
	}
	if errors.Is(groupErr, syscall.ESRCH) && errors.Is(procErr, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("failed to kill process %d: %w", pid, errors.Join(groupErr, procErr))
}

// descendantsFromParentMap returns all descendants of root, deepest first, so
// a parent cannot be reaped and reparent its children before they are visited.
func descendantsFromParentMap(root int, parent map[int]int) []int {
	if len(parent) == 0 {
		return nil
	}
	children := make(map[int][]int, len(parent))
	for pid, ppid := range parent {
		children[ppid] = append(children[ppid], pid)
	}

	seen := map[int]bool{root: true}
	current := []int{root}
	var levels [][]int
	for len(current) > 0 {
		var next []int
		for _, pid := range current {
			for _, child := range children[pid] {
				if seen[child] {
					continue
				}
				seen[child] = true
				next = append(next, child)
			}
		}
		if len(next) > 0 {
			levels = append(levels, next)
		}
		current = next
	}

	var descendants []int
	for i := len(levels) - 1; i >= 0; i-- {
		descendants = append(descendants, levels[i]...)
	}
	return descendants
}
