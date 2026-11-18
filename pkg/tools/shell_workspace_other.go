//go:build !windows

package tools

// guardWindowsWorkspacePaths is a no-op outside Windows; Unix path containment
// is enforced by the OS sandbox.
func (t *ExecTool) guardWindowsWorkspacePaths(string, string) string {
	return ""
}
