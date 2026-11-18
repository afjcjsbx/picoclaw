//go:build !windows

package tools

// pidOwnedBySession reports whether pid is the session root or one of its
// descendants. Descendants are enumerated while the root is alive, so a child
// that called setsid is still covered.
func pidOwnedBySession(pid, root int) bool {
	if pid <= 0 || root <= 0 {
		return false
	}
	if pid == root {
		return true
	}
	for _, descendant := range descendantPIDs(root) {
		if descendant == pid {
			return true
		}
	}
	return false
}

// killValidatedPID terminates one validated process and its descendants.
func killValidatedPID(pid int) error {
	return killProcessTree(pid)
}
