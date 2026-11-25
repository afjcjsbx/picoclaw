//go:build windows

package tools

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// Platform behavior of the exec tool. The posix and windows files define the
// same set of names, so the rest of the package does not branch on the OS.
const (
	// posixShell is false: commands run through PowerShell, which the POSIX
	// parse does not describe, so only the text layers apply.
	posixShell = false
	// ptySupported is false: PTY mode is not available on Windows.
	ptySupported = false
)

// platformDenyPatterns are the text deny patterns of the default guard. They
// include the generic command patterns that risk.go covers on Unix, because
// the AST classifier is POSIX-only.
func platformDenyPatterns() []*regexp.Regexp { return windowsDenyPatterns }

// shellCommand returns the OS shell and its arguments for one exec run.
func shellCommand(command string) (string, []string, error) {
	return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", command}, nil
}

// wrapCommandForCwd leaves PowerShell commands unchanged: they cannot take the
// POSIX cwd probe as a suffix.
func wrapCommandForCwd(command, _ string) (string, bool) {
	return command, false
}

// denyFormsOf lists the text forms the deny patterns match. There is no
// canonicalization: the command is not a POSIX script.
func denyFormsOf(_ string, analysis shellguard.Analysis) []string {
	return []string{strings.ToLower(strings.TrimSpace(analysis.StrippedHeredoc))}
}

// checkWorkspacePaths applies the path scanner, the only workspace layer on
// Windows because the AST pass is unavailable.
func (t *ExecTool) checkWorkspacePaths(in *guardInput, cwdPath string) error {
	if blocked := t.guardWindowsWorkspacePaths(in.command, cwdPath); blocked != "" {
		return guardBlockedError(blocked)
	}
	return nil
}

// execEnvKey normalizes a variable name. Windows environment names are
// case-insensitive, so keys are uppercased to avoid duplicate entries such as
// Path and PATH.
func execEnvKey(key string) string { return strings.ToUpper(key) }

// platformEnvAllowlist lists the variables inherited on top of the default
// allowlist.
func platformEnvAllowlist() map[string]bool { return windowsExecEnvAllowlist }

// protectedPathForms lists the spellings of a protected path that a delete
// command could use: backslashes and forward slashes.
func protectedPathForms(path string) []string {
	path = strings.TrimRight(strings.TrimRight(path, string(filepath.Separator)), "/")
	if path == "" {
		return nil
	}
	return []string{path, filepath.ToSlash(path)}
}

// windowsExecEnvAllowlist contains additional variables needed to run commands
// on Windows, where the default Unix set is not enough.
var windowsExecEnvAllowlist = map[string]bool{
	"PATHEXT":      true,
	"SYSTEMROOT":   true,
	"SYSTEMDRIVE":  true,
	"WINDIR":       true,
	"COMSPEC":      true,
	"APPDATA":      true,
	"LOCALAPPDATA": true,
	"PROGRAMDATA":  true,
	"USERPROFILE":  true,
	"HOMEDRIVE":    true,
	"HOMEPATH":     true,
	"TEMP":         true,
	"TMP":          true,
}
