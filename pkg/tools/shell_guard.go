package tools

import (
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// commandMatchesCustomAllowPattern reports whether a custom allow pattern
// matches the whole command. These patterns exempt the default deny set;
// hardline and custom deny patterns still win.
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

// guardCommandContext decides whether a command may run. It returns "" to
// allow it, or the block message. Layers run from the least to the most
// configurable, and the first one that rejects wins:
//
//  1. Hardline patterns: an immutable floor (catastrophic operations and
//     self-protection). They apply even with enable_deny_patterns=false and no
//     allow rule exempts them.
//  2. Custom deny patterns: always enforced and stronger than any allow rule.
//  3. Default deny patterns: shell constructs and destructive repository or
//     remote operations. They can be disabled with enable_deny_patterns=false
//     and exempted with custom_allow_patterns.
//  4. Windows workspace boundary: the restricted-token backend does not remap
//     paths, so the byte-level scanner remains the only workspace check there.
//
// On Unix the OS sandbox is the filesystem and process boundary. The static
// guard deliberately does not parse the command or duplicate the mount policy.
func (t *ExecTool) guardCommandContext(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)
	forms := []string{lower}
	if runtime.GOOS != "windows" {
		if normalized := normalizeGuardText(lower); normalized != lower {
			forms = append(forms, normalized)
		}
	}

	if patternsMatchAny(t.hardlineDenyPatterns, forms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}
	if patternsMatchAny(t.customDenyPatterns, forms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}
	if !t.commandMatchesCustomAllowPattern(lower) {
		if patternsMatchAny(t.denyPatterns, forms) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
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

// normalizeGuardText resolves the cheap fixed shell rewrites that commonly
// hide a hardline command. The OS sandbox remains the boundary, so this is a
// best-effort pass, not a parser.
func normalizeGuardText(command string) string {
	command = strings.ReplaceAll(command, "${ifs}", " ")
	command = strings.ReplaceAll(command, "$ifs", " ")
	command = strings.ReplaceAll(command, "\\\n", "")
	return command
}
