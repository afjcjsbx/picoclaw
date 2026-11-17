package tools

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

func (t *ExecTool) commandMatchesAllowPattern(lower string) bool {
	return patternsMatchAny(t.allowPatterns, []string{lower}) || t.commandMatchesCustomAllowPattern(lower)
}

// commandMatchesCustomAllowPattern reports whether a custom allow pattern
// matches the whole command. These patterns exempt only the default deny set;
// they do not affect the risk classifier.
func (t *ExecTool) commandMatchesCustomAllowPattern(lower string) bool {
	return patternsMatchAny(t.customAllowPatterns, []string{lower})
}

func (t *ExecTool) commandMatchesCustomRiskAllowPattern(lower string) bool {
	return patternsMatchAny(t.customRiskAllowPatterns, []string{lower})
}

// riskAllowExempts reports whether a matching custom risk allow pattern covers
// the whole command and the command stays within riskAllowThreshold. It uses a
// second guard with that threshold so a later critical command cannot hide
// behind an earlier high-risk match.
func (t *ExecTool) riskAllowExempts(command, lower string) bool {
	if t.riskGuard == nil || !t.commandMatchesCustomRiskAllowPattern(lower) {
		return false
	}
	allowGuard := t.riskGuard.WithThreshold(t.riskAllowThreshold)
	return allowGuard != nil && allowGuard.Check(command) == nil
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

func (t *ExecTool) guardCommand(command, cwd string) string {
	return t.guardCommandContext(context.Background(), command, cwd)
}

// guardCommandContext decides whether a command may run. It returns "" to
// allow it, or the block message. Layers run from the least to the most
// configurable, and the first one that rejects wins:
//
//  1. Matching forms: quoted heredoc bodies are stripped, and on Unix $IFS,
//     ANSI-C quoting and line continuations are resolved and every command is
//     canonicalized from the AST (wrappers such as env, quote-spliced words).
//  2. Hardline patterns: an immutable floor (catastrophic operations and
//     self-protection). They apply even with enable_deny_patterns=false, no
//     allow rule exempts them and the Guardian is never consulted.
//  3. Custom deny patterns: always enforced and stronger than any allow rule.
//  4. Default deny patterns: exempted only by custom_allow_patterns. In smart
//     mode a hit is sent to the Guardian instead of being rejected.
//  5. Allowlist: when allow patterns are set, the command must match one.
//  6. Workspace boundary (restrict_to_workspace): a byte-level path-traversal
//     check, then the AST path guard on Unix or the path scanner on Windows.
//  7. Risk level: the AST classifier rates the command low to critical and
//     rejects anything above risk_threshold, unless custom_risk_allow_patterns
//     exempts it up to risk_allow_threshold. Unparseable commands and dynamic
//     command names count as unknown and are rejected whatever the threshold
//     (in smart mode they go to layer 8 instead).
//  8. Smart approvals: commands flagged by layer 4 or at or above
//     approvals.smart_threshold (and unknown ones) are reviewed by the Guardian
//     model. APPROVE runs the command; DENY, ESCALATE and any error block it.
func (t *ExecTool) guardCommandContext(ctx context.Context, command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)
	// One parse serves heredoc stripping, canonical deny forms, workspace
	// validation and the risk classifier.
	analysis := shellguard.Analyze(cmd)
	// Quoted heredoc bodies are literal text: a backtick there cannot run a
	// command, so deny patterns must not inspect it.
	stripped := analysis.StrippedHeredoc
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
	// They are never sent to the Guardian model.
	if patternsMatchAny(t.hardlineDenyPatterns, denyForms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}

	// Custom deny patterns always apply: they are user-specified security rules
	// that must not be bypassed by custom allow patterns (#3079). Matching the
	// canonical AST words also catches quote-spliced forms such as r''m.
	if patternsMatchAny(t.customDenyPatterns, denyForms) {
		return "Command blocked by safety guard (dangerous pattern detected)"
	}

	needsApproval := false
	approvalDescription := ""

	// Default deny patterns can be exempted by custom allow patterns, so
	// operators can selectively permit normally-blocked commands. In smart
	// mode they can also be reviewed instead of being rejected outright.
	if !t.commandMatchesCustomAllowPattern(lower) {
		// Use the same canonical forms as the hardline/custom layers. This
		// catches shell-equivalent spellings such as g\it, which the raw
		// lowerForDeny form does not expose.
		if patternsMatchAny(t.denyPatterns, denyForms) {
			if !t.approvalEnabled {
				return "Command blocked by safety guard (dangerous pattern detected)"
			}
			needsApproval = true
			approvalDescription = "matched a built-in dangerous-command pattern"
		}
	}

	if len(t.allowPatterns) > 0 {
		if !t.commandMatchesAllowPattern(lower) {
			return "Command blocked by safety guard (not in allowlist)"
		}
	}

	// The workspace and risk passes share the same AST. CheckWorkspaceFile
	// re-parses only when fixed shell rewrites changed the word boundaries.
	parsed := analysis.File

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
	// byte-level patterns cannot resolve, such as `x=rm; $x -rf /`. Only an
	// explicit custom risk allow pattern can exempt it, and only up to
	// riskAllowThreshold; custom_allow_patterns no longer bypasses this layer.
	// In smart mode, commands at or above approvalThreshold, plus unknown
	// commands, go to the Guardian instead of being rejected outright.
	// Script files are inspected only inside the workspace: Dir resolves relative
	// paths and Root bounds what the guard is willing to read.
	if t.riskGuard != nil {
		root := t.workingDir
		if root == "" {
			root = cwd
		}
		scriptOpts := shellguard.CheckOptions{Dir: cwd, Root: root}
		if t.approvalEnabled {
			assessment := t.riskGuard.AssessFileWithOptions(cmd, parsed, scriptOpts)
			if assessment.Reject {
				return assessment.BlockedError(t.riskGuard.Threshold()).Error()
			}
			riskExempt := t.riskAllowExempts(cmd, lower)
			riskBlocked := !riskExempt &&
				(assessment.Unknown ||
					!shellguard.IsAllowed(assessment.Level, t.riskGuard.Threshold()))
			smartTriggered := !riskExempt &&
				(assessment.Unknown || assessment.Level >= t.approvalThreshold)
			if smartTriggered {
				needsApproval = true
				riskDescription := assessment.Reason
				if !assessment.Unknown || riskDescription == "" {
					riskDescription = "risk level " + assessment.Level.String()
				}
				if approvalDescription == "" {
					approvalDescription = riskDescription
				} else {
					approvalDescription += "; " + riskDescription
				}
			} else if riskBlocked {
				return assessment.BlockedError(t.riskGuard.Threshold()).Error()
			}
		} else if blocked := t.riskGuard.CheckFileWithOptions(cmd, parsed, scriptOpts); blocked != nil {
			if !t.riskAllowExempts(cmd, lower) {
				return blocked.Error()
			}
		}
	}

	if needsApproval {
		if blocked := t.reviewCommand(ctx, cmd, approvalDescription); blocked != "" {
			return blocked
		}
	}

	return ""
}

// reviewCommand asks the Guardian model for a verdict on a command that the
// static layers flagged. Approve returns an empty string; deny, escalate, an
// unavailable reviewer and provider errors all return a block message.
func (t *ExecTool) reviewCommand(ctx context.Context, command, description string) string {
	if t.approvalReviewer == nil {
		return "Command blocked: approval guardian is unavailable; manual approval is not configured"
	}
	verdict, err := t.approvalReviewer.Review(ctx, command, description)
	if err != nil {
		return "Command blocked: approval guardian could not review the command; manual approval is not configured"
	}
	switch verdict {
	case approvalApprove:
		return ""
	case approvalDeny:
		return "Command blocked by approval guardian (DENY)"
	default:
		return "Command blocked by approval guardian (ESCALATE); manual approval is not configured"
	}
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
