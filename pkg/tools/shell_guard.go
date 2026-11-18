package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// commandMatchesCustomAllowPattern reports whether a custom allow pattern
// matches the whole command. These patterns exempt only the default deny set;
// they do not affect the risk classifier.
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
//  1. Matching forms: quoted heredoc bodies are stripped, and on Unix $IFS,
//     ANSI-C quoting and line continuations are resolved and every command is
//     canonicalized from the AST (wrappers such as env, quote-spliced words).
//  2. Hardline patterns: an immutable floor (catastrophic operations and
//     self-protection). They apply even with enable_deny_patterns=false and no
//     allow rule exempts them.
//  3. Custom deny patterns: always enforced and stronger than any allow rule.
//  4. Default deny patterns: the shell constructs the AST cannot inspect
//     (command substitution, pipe-to-shell, shell heredocs). Exempted only by
//     custom_allow_patterns; a hit is always rejected.
//  5. Workspace boundary (restrict_to_workspace): a byte-level path-traversal
//     check, then the AST path guard on Unix or the path scanner on Windows.
//  6. Risk level: the AST classifier rates the command low to critical and
//     rejects anything above risk_threshold. Commands the classifier cannot
//     resolve (dynamic command names, opaque shells, parse errors) are allowed
//     by default; dynamic operands for destructive commands are still rejected.
func (t *ExecTool) guardCommandContext(command, cwd string) string {
	return t.guardCommandWithAnalysis(command, cwd, shellguard.Analyze(strings.TrimSpace(command)))
}

// guardCommandWithAnalysis is guardCommandContext with a parse the caller
// already owns, so a run does not parse the same command twice.
func (t *ExecTool) guardCommandWithAnalysis(
	command, cwd string, analysis shellguard.Analysis,
) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)
	// Quoted heredoc bodies are literal text: a backtick there cannot run a
	// command, so deny patterns must not inspect it. The caller parsed the
	// untrimmed command, so trim the stripped view like before.
	stripped := strings.TrimSpace(analysis.StrippedHeredoc)
	lowerForDeny := strings.ToLower(stripped)
	denyForms := []string{lowerForDeny}

	// POSIX shells can hide a command behind fixed obfuscation such as
	// $IFS, ANSI-C quoting or a backslash-newline. Resolve those forms and
	// let the AST expose quote-spliced argv words. This runs even when the
	// optional risk classifier is nil, so the hardline floor remains
	// independent of enable_deny_patterns.
	if runtime.GOOS != "windows" {
		lowerForDeny = strings.ToLower(shellguard.NormalizeForDeny(stripped))
		canonicalForms := analysis.Canonical
		if !analysis.CanonicalExact {
			// Fixed rewrites such as $IFS change word boundaries, so the
			// written-form AST is not authoritative; re-parse the normalized
			// text only for those rare commands.
			canonicalForms = shellguard.CanonicalCommands(cmd)
		}
		denyForms = make([]string, 0, 1+len(canonicalForms))
		denyForms = append(denyForms, lowerForDeny)
		// Canonicalize every command, not just ones with quoting: wrappers
		// such as env and command expose the nested argv without any special
		// shell syntax, and custom deny patterns must see it.
		for _, form := range canonicalForms {
			denyForms = append(denyForms, strings.ToLower(form))
		}
	}

	// Hardline patterns are an immutable floor: they apply even when the
	// default deny set is disabled and no custom allow rule can exempt them.
	if patternsMatchAny(t.hardlineDenyPatterns, denyForms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}

	// Custom deny patterns always apply: they are user-specified security rules
	// that must not be bypassed by custom allow patterns (#3079). Matching the
	// canonical AST words also catches quote-spliced forms such as r''m.
	if patternsMatchAny(t.customDenyPatterns, denyForms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}

	// Default deny patterns can be exempted by custom allow patterns, so
	// operators can selectively permit normally-blocked commands. A hit is
	// final.
	if !t.commandMatchesCustomAllowPattern(lower) {
		// Use the same canonical forms as the hardline/custom layers. This
		// catches shell-equivalent spellings such as g\it, which the raw
		// lowerForDeny form does not expose.
		if patternsMatchAny(t.denyPatterns, denyForms) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	// The workspace and risk passes share the same parse unless fixed rewrites
	// changed word boundaries; RiskFile then holds the normalized parse.
	parsed := analysis.File
	riskFile := analysis.RiskFile

	if t.restrictToWorkspace {
		// The AST pass understands ... components, but these dotted variants
		// are still caught byte-wise for the platform-independent cases and
		// for commands the parser cannot inspect.
		if pathTraversalPattern.MatchString(cmd) {
			return "Command blocked by safety guard (path traversal detected)"
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}
		// Resolve symlinks in the workspace root too. Candidate paths are
		// resolved with EvalSymlinks before the containment check, so comparing
		// them against an unresolved root reports every relative path as an
		// escape whenever the workspace path contains a symlink (for example
		// /var -> /private/var on macOS, or a symlinked project directory).
		if resolved, err := filepath.EvalSymlinks(cwdPath); err == nil {
			cwdPath = resolved
		}

		if runtime.GOOS == "windows" {
			// PowerShell does not parse as POSIX shell, so the AST pass is
			// unavailable and the Windows path scanner remains the only
			// workspace layer.
			if blocked := t.guardWindowsWorkspacePaths(cmd, cwdPath); blocked != "" {
				return blocked
			}
		} else {
			policy := shellguard.WorkspacePolicy{
				Cwd:    cwdPath,
				Home:   t.execHomeDir(),
				CDPath: t.baseEnv["CDPATH"] != "",
				AllowPath: func(p string) bool {
					return t.workspacePathAllowed(p, cwdPath)
				},
			}
			if err := shellguard.CheckWorkspaceFile(cmd, parsed, policy); err != nil {
				return "Command blocked by safety guard (" + err.Error() + ")"
			}
		}
	}

	// The AST classifier is the last built-in layer: it sees commands that the
	// byte-level patterns cannot resolve, such as `x=rm; $x -rf /`.
	// custom_allow_patterns does not bypass it. Known commands above the
	// threshold are rejected, while unknown commands are allowed by default.
	if t.riskGuard != nil {
		assessment := t.riskGuard.AssessFile(cmd, riskFile)
		if assessment.Reject {
			return assessment.BlockedError(t.riskGuard.Threshold()).Error()
		}
		if !assessment.Unknown && !shellguard.IsAllowed(assessment.Level, t.riskGuard.Threshold()) {
			return assessment.BlockedError(t.riskGuard.Threshold()).Error()
		}
	}

	return ""
}

// workspacePathAllowed reports whether p stays inside cwdPath or is covered by
// the operator allowlist. Symlinks are resolved against the nearest existing
// ancestor so a new file behind a symlinked directory is still checked.
func (t *ExecTool) workspacePathAllowed(p, cwdPath string) bool {
	if resolved, err := fstools.ResolvePathAgainstExistingAncestor(p); err == nil {
		p = resolved
	}
	if safePaths[p] {
		return true
	}
	if fstools.IsAllowedPath(p, t.allowedPathPatterns) {
		return true
	}
	return fstools.IsWithinWorkspace(p, cwdPath)
}

// execHomeDir returns the home directory the child shell will see. A HOME
// present in the cached base environment (config env_set or inherited) wins,
// even when it is not absolute; tilde expansion is then unresolvable and
// blocked. os.UserHomeDir is only a fallback when the key is absent.
func (t *ExecTool) execHomeDir() string {
	if home, ok := t.baseEnv["HOME"]; ok {
		if filepath.IsAbs(home) {
			return home
		}
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		return home
	}
	return ""
}
