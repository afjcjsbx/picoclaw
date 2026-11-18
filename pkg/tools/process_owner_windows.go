//go:build windows

package tools

import "os"

// pidOwnedBySession reports whether pid is the session root. Windows has no
// descendant helper in this package, so only the tracked root is accepted.
func pidOwnedBySession(pid, root int) bool {
	return pid > 0 && root > 0 && pid == root
}

// killValidatedPID terminates one validated process.
func killValidatedPID(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
