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

// shellKillPIDPattern catches the common `kill <pid>` spelling so the block
// message can point the model at the validated kill-pid action.
var shellKillPIDPattern = regexp.MustCompile(`(?:^|[;&|]\s*)kill\s+(?:-\w+\s+)*[0-9]+`)

// guardCommandContext evaluates one command and returns a typed decision. The
// caller must start a process only for shellguard.ActionAllow; ActionReview
// goes through the configured approver and ActionBlock is final.
//
// Layers run from the least to the most configurable, and the first one that
// rejects wins:
//
//  1. Matching forms: quoted heredoc bodies are stripped, and on Unix $IFS,
//     ANSI-C quoting and line continuations are resolved and every command is
//     canonicalized from the AST (wrappers such as env, quote-spliced words).
//  2. Hardline floor: immutable AST rules plus a regex fallback for forms the
//     parser cannot represent. Hardline is never reviewed and never exempted.
//  3. Custom deny patterns: always enforced and stronger than any allow rule.
//  4. Custom unlock patterns: full-command operator exceptions. A match skips
//     the default deny set and the risk classifier; hardline, custom deny and
//     the workspace boundary still run.
//  5. Default deny patterns: the shell constructs the AST cannot inspect
//     (command substitution, pipe-to-shell, shell heredocs). Exempted only by
//     custom_allow_patterns.
//  6. Workspace boundary (restrict_to_workspace): a byte-level path-traversal
//     check, then the AST path guard on Unix or the path scanner on Windows.
//  7. Risk level: the AST classifier rates the command low to critical and
//     rejects anything above risk_threshold. Commands the classifier cannot
//     resolve (dynamic command names, opaque interpreters, parse failures) are
//     returned as ActionReview, never as ActionAllow.
//
// The guard itself does not call a model or an approver. That keeps the policy
// deterministic and lets the caller apply the human approval flow,
// including the allow_unknown compatibility switch, in one place.
func (t *ExecTool) guardCommandContext(
	ctx context.Context, command, cwd string,
) shellguard.Decision {
	_ = ctx
	return t.guardCommandWithAnalysis(command, cwd, shellguard.Analyze(strings.TrimSpace(command)))
}

// guardCommandWithAnalysis is guardCommandContext with a parse the caller
// already owns, so a run does not parse the same command twice.
func (t *ExecTool) guardCommandWithAnalysis(
	command, cwd string, analysis shellguard.Analysis,
) shellguard.Decision {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)
	// A command that does not parse cannot be inspected by any AST layer. It is
	// reviewed or blocked, never allowed, and allow_unknown does not override
	// this rule: a malformed command is not the same as an opaque command.
	if runtime.GOOS != "windows" && analysis.File == nil {
		return shellguard.BlockDecision(
			"parse-error",
			shellguard.RiskCritical,
			"Command blocked: command could not be parsed for risk classification",
		)
	}
	// The hardline floor is evaluated on the AST first, so quoting, wrappers
	// and path normalization cannot hide a destructive operation from a regex.
	// The regex floor below stays as a fallback for forms the parser cannot
	// represent, including PowerShell on Windows.
	if runtime.GOOS != "windows" {
		hardlineCwd := cwd
		if hardlineCwd == "" {
			hardlineCwd = t.workingDir
		}
		home := t.execHomeDir()
		if finding, found := shellguard.HardlineFile(analysis.File, hardlineCwd, home); found {
			return shellguard.BlockDecision(
				finding.Rule, shellguard.RiskCritical,
				"Command blocked by safety guard ("+finding.Reason+")",
			)
		}
		if analysis.RiskFile != nil && analysis.RiskFile != analysis.File {
			if finding, found := shellguard.HardlineFile(analysis.RiskFile, hardlineCwd, home); found {
				return shellguard.BlockDecision(
					finding.Rule, shellguard.RiskCritical,
					"Command blocked by safety guard ("+finding.Reason+")",
				)
			}
		}
	}
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
	// They are never reviewed.
	if patternsMatchAny(t.hardlineDenyPatterns, denyForms) {
		return shellguard.BlockDecision(
			"hardline.regex", shellguard.RiskCritical,
			"Command blocked by safety guard (dangerous pattern detected)",
		)
	}

	// Custom deny patterns always apply: they are user-specified security rules
	// that must not be bypassed by custom allow patterns (#3079). Matching the
	// canonical AST words also catches quote-spliced forms such as r''m.
	if patternsMatchAny(t.customDenyPatterns, denyForms) {
		return shellguard.BlockDecision(
			"operator.deny", shellguard.RiskHigh,
			"Command blocked by safety guard (dangerous pattern detected)",
		)
	}

	// Operator allow rules are explicit per-command exceptions. Each rule
	// names the layers it bypasses; the hardline floor, custom deny rules and
	// the workspace boundary always run.
	allowRule, hasAllowRule := t.matchAllowRule(lower)
	bypassShell := hasAllowRule && allowRule.bypassShell
	bypassRisk := hasAllowRule && allowRule.bypassRisk
	bypassApproval := hasAllowRule && allowRule.bypassApproval

	// Default deny patterns can be exempted by a shell_constructs bypass, so
	// operators can selectively permit normally-blocked commands. A hit is
	// final: the risk classifier does not get a second opinion on a construct
	// it cannot inspect.
	if !bypassShell {
		// Use the same canonical forms as the hardline/custom layers. This
		// catches shell-equivalent spellings such as g\it, which the raw
		// lowerForDeny form does not expose.
		if patternsMatchAny(t.denyPatterns, denyForms) {
			reason := "Command blocked by safety guard (dangerous pattern detected)"
			if shellKillPIDPattern.MatchString(lower) {
				reason += "; use action=kill-pid with a PID owned by a tracked exec session, " +
					"or action=kill with the sessionId"
			}
			return shellguard.BlockDecision("shell.construct", shellguard.RiskHigh, reason)
		}
	}

	// The workspace and risk passes share the same parse unless fixed rewrites
	// changed word boundaries; RiskFile then holds the normalized parse.
	parsed := analysis.File
	riskFile := analysis.RiskFile

	// When the OS sandbox is active the kernel owns path containment. Skip the
	// static workspace and interpreter passes: they duplicate the mount policy,
	// produce false positives for valid in-sandbox paths and cannot resolve
	// dynamic interpreter paths anyway. Hardline, operator rules, risk and
	// approval still run.
	if t.restrictToWorkspace && !t.sandboxPaths {
		// The AST pass understands ... components, but these dotted variants
		// are still caught byte-wise for the platform-independent cases and
		// for commands the parser cannot inspect.
		if pathTraversalPattern.MatchString(cmd) {
			return shellguard.BlockDecision(
				"workspace.traversal", shellguard.RiskHigh,
				"Command blocked by safety guard (path traversal detected)",
			)
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return shellguard.AllowDecision("workspace.unresolved-cwd")
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
				return shellguard.BlockDecision("workspace.windows", shellguard.RiskHigh, blocked)
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
				return shellguard.BlockDecision(
					"workspace.path", shellguard.RiskHigh,
					"Command blocked by safety guard ("+err.Error()+")",
				)
			}
		}
	}

	// The AST classifier is the last built-in layer: it sees commands that the
	// byte-level patterns cannot resolve, such as `x=rm; $x -rf /`. Only an
	// explicit custom unlock pattern can bypass it; custom_allow_patterns no
	// longer does. Known commands above the threshold are rejected. Unknown
	// commands are returned for review, never silently allowed.
	if t.riskGuard != nil && !bypassRisk {
		assessment := t.riskGuard.AssessFile(cmd, riskFile)
		if assessment.Reject {
			return shellguard.BlockDecision(
				"risk.dynamic-operand", assessment.Level,
				riskBlockReason(lower, assessment.BlockedError(t.riskGuard.Threshold()).Error()),
			)
		}
		if assessment.Unknown {
			reason := assessment.Reason
			if reason == "" {
				reason = "command risk could not be classified"
			}
			if bypassApproval {
				return shellguard.AllowDecision(allowRule.id)
			}
			return shellguard.ReviewDecision("risk.unknown", assessment.Level, reason)
		}
		if !shellguard.IsAllowed(assessment.Level, t.riskGuard.Threshold()) {
			return shellguard.BlockDecision(
				"risk.threshold", assessment.Level,
				riskBlockReason(lower, assessment.BlockedError(t.riskGuard.Threshold()).Error()),
			)
		}
	}

	if hasAllowRule {
		return shellguard.AllowDecision(allowRule.id)
	}
	return shellguard.AllowDecision("allow")
}

// unknownBlockedReason explains why a command that could not be classified is
// rejected in the strict default policy. Operators can restore the legacy
// behavior with tools.exec.allow_unknown=true or configure an approver.
func unknownBlockedReason(reason string) string {
	if reason == "" {
		reason = "the command could not be classified"
	}
	return "Command blocked by strict unknown policy: " + reason +
		"; set tools.exec.allow_unknown=true or configure an approver to allow it"
}

// riskBlockReason adds the validated kill-pid guidance when a shell kill was
// rejected by the risk layer rather than by a deny pattern.
func riskBlockReason(command, reason string) string {
	if shellKillPIDPattern.MatchString(command) {
		return reason + "; use action=kill-pid with a PID owned by a tracked exec session, or action=kill with the sessionId"
	}
	return reason
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
