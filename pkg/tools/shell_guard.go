package tools

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// commandMatchesCustomAllowPattern reports whether a custom allow pattern
// matches the whole command. These patterns exempt only the default deny set;
// hardline and custom deny rules still win.
func (t *ExecTool) commandMatchesCustomAllowPattern(lower string) bool {
	return patternsMatchAny(t.customAllowPatterns, []string{lower})
}

func patternsMatchAny(patterns []*regexp.Regexp, forms []string) bool {
	for _, pattern := range patterns {
		for _, form := range forms {
			if pattern.MatchString(form) {
				return true
			}
		}
	}
	return false
}

// guardCommandContext decides whether a command may run. The OS sandbox is the
// containment boundary; this function is a best-effort policy layer:
//
//  1. Fixed shell rewrites ($IFS, line continuations) are resolved for the
//     deny patterns.
//  2. Hardline patterns: an immutable floor for catastrophic operations and
//     self-protection. No allow rule exempts them.
//  3. Custom deny patterns: always enforced and stronger than any allow rule.
//  4. Default deny patterns: shell constructs and the compact destructive
//     workspace/repository/remote policy list. Exempted by custom allow rules.
//  5. Windows workspace scanner: the Windows backend does not remap paths, so
//     the static path scanner remains the workspace layer there.
func (t *ExecTool) guardCommandContext(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)
	forms := []string{lower}
	if runtime.GOOS != "windows" {
		forms = append(forms, strings.ToLower(normalizeForDeny(cmd)))
	}

	if patternsMatchAny(t.hardlineDenyPatterns, forms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}
	if patternsMatchAny(t.customDenyPatterns, forms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}
	if !t.commandMatchesCustomAllowPattern(lower) &&
		patternsMatchAny(t.denyPatterns, forms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}

	if t.restrictToWorkspace && runtime.GOOS == "windows" {
		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}
		if resolved, err := filepath.EvalSymlinks(cwdPath); err == nil {
			cwdPath = resolved
		}
		if blocked := t.guardWindowsWorkspacePaths(cmd, cwdPath); blocked != "" {
			return blocked
		}
	}

	return ""
}
