//go:build !linux && !darwin && !windows

package tools

// descendantPIDs is unavailable on this platform; the process-group kill is
// still used as the final fallback.
func descendantPIDs(root int) []int {
	return nil
}
