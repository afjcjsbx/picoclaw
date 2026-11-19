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

// guardBlockedError is the reason a guard layer refused a command. It becomes
// the tool's error message.
type guardBlockedError string

func (b guardBlockedError) Error() string { return string(b) }

// blockedBySafetyGuard formats a refusal from the pattern and path layers.
func blockedBySafetyGuard(reason string) error {
	return guardBlockedError("Command blocked by safety guard (" + reason + ")")
}

// guardInput is what every guard layer reads, computed once per command.
type guardInput struct {
	// command is the trimmed command as written; lower is its lower-case form.
	command string
	lower   string
	cwd     string
	// analysis is the shared parse of the command.
	analysis shellguard.Analysis
	// denyForms are the text forms the deny patterns match: the command with
	// quoted heredoc bodies stripped and, on Unix, every canonical simple
	// command.
	denyForms []string
}

// guardLayers decide whether a command may run. They run from the least to the
// most configurable, and the first one that rejects wins:
//
//  1. Hardline patterns: an immutable floor (catastrophic operations and
//     self-protection). They apply even with enable_deny_patterns=false and no
//     allow rule exempts them.
//  2. Custom deny patterns: always enforced and stronger than any allow rule.
//  3. Default deny: the shell constructs the AST cannot inspect (command
//     substitution, pipe-to-shell, shell heredocs). Exempted only by
//     custom_allow_patterns; a hit is always rejected.
//  4. Workspace boundary (restrict_to_workspace): a byte-level path-traversal
//     check, then the AST path guard on Unix or the path scanner on Windows.
//  5. Risk level: the AST classifier rates the command low to critical and
//     rejects anything above risk_threshold. Commands the classifier cannot
//     resolve (dynamic command names, opaque shells, parse errors) are allowed
//     by default; dynamic operands for destructive commands are still rejected.
//
// Each layer returns nil to let the command through.
var guardLayers = []func(*ExecTool, *guardInput) error{
	(*ExecTool).guardHardline,
	(*ExecTool).guardCustomDeny,
	(*ExecTool).guardDefaultDeny,
	(*ExecTool).guardWorkspace,
	(*ExecTool).guardRisk,
}

// guardCommandContext parses the command and runs the guard layers. It returns
// "" to allow it, or the block message.
func (t *ExecTool) guardCommandContext(command, cwd string) string {
	if err := t.guardCommandWithAnalysis(command, cwd, shellguard.Analyze(strings.TrimSpace(command))); err != nil {
		return err.Error()
	}
	return ""
}

// guardCommandWithAnalysis runs the guard layers with a parse the caller
// already owns, so a run does not parse the same command twice.
func (t *ExecTool) guardCommandWithAnalysis(
	command, cwd string, analysis shellguard.Analysis,
) error {
	cmd := strings.TrimSpace(command)
	in := &guardInput{
		command:   cmd,
		lower:     strings.ToLower(cmd),
		cwd:       cwd,
		analysis:  analysis,
		denyForms: denyFormsOf(cmd, analysis),
	}
	for _, layer := range guardLayers {
		if err := layer(t, in); err != nil {
			return err
		}
	}
	return nil
}

// denyFormsOf lists the text forms the deny patterns match. Quoted heredoc
// bodies are literal text: a backtick there cannot run a command, so deny
// patterns must not inspect it.
func denyFormsOf(cmd string, analysis shellguard.Analysis) []string {
	// The caller parsed the untrimmed command, so trim the stripped view.
	stripped := strings.TrimSpace(analysis.StrippedHeredoc)
	if runtime.GOOS == "windows" {
		return []string{strings.ToLower(stripped)}
	}

	// POSIX shells can hide a command behind fixed obfuscation such as $IFS,
	// ANSI-C quoting or a backslash-newline. Resolve those forms and let the AST
	// expose quote-spliced argv words. This runs even when the optional risk
	// classifier is nil, so the hardline floor stays independent of
	// enable_deny_patterns.
	canonicalForms := analysis.Canonical
	if !analysis.CanonicalExact {
		// Fixed rewrites such as $IFS change word boundaries, so the
		// written-form AST is not authoritative; re-parse the normalized text
		// only for those rare commands.
		canonicalForms = shellguard.CanonicalCommands(cmd)
	}
	forms := make([]string, 0, 1+len(canonicalForms))
	forms = append(forms, strings.ToLower(shellguard.NormalizeForDeny(stripped)))
	// Canonicalize every command, not just ones with quoting: wrappers such as
	// env and command expose the nested argv without any special shell syntax,
	// and custom deny patterns must see it.
	for _, form := range canonicalForms {
		forms = append(forms, strings.ToLower(form))
	}
	return forms
}

func (t *ExecTool) guardHardline(in *guardInput) error {
	if patternsMatchAny(t.hardlineDenyPatterns, in.denyForms) {
		return blockedBySafetyGuard("dangerous pattern detected")
	}
	return nil
}

// guardCustomDeny enforces operator deny rules. They must not be bypassed by
// custom allow patterns (#3079). Matching the canonical AST words also catches
// quote-spliced forms such as r”m.
func (t *ExecTool) guardCustomDeny(in *guardInput) error {
	if patternsMatchAny(t.customDenyPatterns, in.denyForms) {
		return blockedBySafetyGuard("dangerous pattern detected")
	}
	return nil
}

// guardDefaultDeny can be exempted by custom allow patterns, so operators can
// selectively permit normally-blocked commands. A hit is final.
func (t *ExecTool) guardDefaultDeny(in *guardInput) error {
	if !t.commandMatchesCustomAllowPattern(in.lower) && t.matchesDefaultDeny(in.analysis, in.denyForms) {
		return blockedBySafetyGuard("dangerous pattern detected")
	}
	return nil
}

func (t *ExecTool) guardWorkspace(in *guardInput) error {
	if !t.restrictToWorkspace {
		return nil
	}
	// The AST pass understands ... components, but these dotted variants are
	// still caught byte-wise for the platform-independent cases and for
	// commands the parser cannot inspect.
	if pathTraversalPattern.MatchString(in.command) {
		return blockedBySafetyGuard("path traversal detected")
	}

	cwdPath, err := filepath.Abs(in.cwd)
	if err != nil {
		return blockedBySafetyGuard("working directory cannot be resolved")
	}
	// Resolve symlinks in the workspace root too. Candidate paths are resolved
	// with EvalSymlinks before the containment check, so comparing them against
	// an unresolved root reports every relative path as an escape whenever the
	// workspace path contains a symlink (for example /var -> /private/var on
	// macOS, or a symlinked project directory).
	if resolved, err := filepath.EvalSymlinks(cwdPath); err == nil {
		cwdPath = resolved
	}

	if runtime.GOOS == "windows" {
		// PowerShell does not parse as POSIX shell, so the AST pass is
		// unavailable and the Windows path scanner remains the only workspace
		// layer.
		if blocked := t.guardWindowsWorkspacePaths(in.command, cwdPath); blocked != "" {
			return guardBlockedError(blocked)
		}
		return nil
	}

	policy := shellguard.WorkspacePolicy{
		Cwd:    cwdPath,
		Home:   t.execHomeDir(),
		CDPath: t.baseEnv["CDPATH"] != "",
		AllowPath: func(p string) bool {
			return t.workspacePathAllowed(p, cwdPath)
		},
	}
	if err := shellguard.CheckWorkspaceFile(in.command, in.analysis.File, policy); err != nil {
		return blockedBySafetyGuard(err.Error())
	}
	return nil
}

// guardRisk is the last built-in layer: it sees commands that the byte-level
// patterns cannot resolve, such as `x=rm; $x -rf /`. custom_allow_patterns does
// not bypass it. RiskFile equals the written parse unless a fixed rewrite
// changed word boundaries.
func (t *ExecTool) guardRisk(in *guardInput) error {
	if blocked := t.riskGuard.Check(in.command, in.analysis.RiskFile); blocked != nil {
		return blocked
	}
	return nil
}

// matchesDefaultDeny reports whether the command hits the optional default
// deny set. The shell constructs the AST cannot inspect come from the parse,
// so quoted text such as echo '$(date)' is not a hit; the text patterns stand
// in for commands that do not parse and for Windows, where the POSIX parse
// does not apply.
func (t *ExecTool) matchesDefaultDeny(analysis shellguard.Analysis, forms []string) bool {
	if patternsMatchAny(t.denyPatterns, forms) {
		return true
	}
	if !t.blockOpaque {
		return false
	}
	if runtime.GOOS == "windows" || analysis.File == nil {
		return patternsMatchAny(opaqueFallbackPatterns, forms)
	}
	return len(analysis.OpaqueConstructs) > 0
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
