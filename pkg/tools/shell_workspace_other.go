//go:build !windows

package tools

// guardWindowsWorkspacePaths is a no-op outside Windows; Unix commands use the
// AST workspace pass instead.
func (t *ExecTool) guardWindowsWorkspacePaths(string, string) string {
	return ""
}
