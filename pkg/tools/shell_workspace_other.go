//go:build !windows

package tools

// guardWindowsWorkspacePaths is a no-op outside Windows; Unix path containment
// is owned by the OS sandbox.
func (t *ExecTool) guardWindowsWorkspacePaths(string, string) string {
	return ""
}
