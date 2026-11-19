package tools

import (
	"regexp"
	"slices"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// newRiskGuard builds the AST-based command classifier. It is part of the
// optional built-in guard set, so enable_deny_patterns=false disables it just
// like the regex deny list. Hardline and custom deny patterns still apply
// independently.
//
// An empty threshold means medium. Windows is skipped because the exec tool
// runs PowerShell there and the classifier's command table describes POSIX
// tools.
func newRiskGuard(threshold string, enabled bool) (*shellguard.Guard, error) {
	if !enabled || !posixShell {
		return nil, nil
	}

	return shellguard.NewGuard(threshold)
}

// ExecRiskAssessment is the subset of the AST risk evaluation that the
// approval gate needs before a command runs.
type ExecRiskAssessment struct {
	// Unknown marks a command the classifier cannot resolve: a dynamic command
	// name, an opaque shell, an unlisted command, or unparseable input.
	Unknown bool
	// Reject marks a command the exec tool blocks regardless of threshold, for
	// example a destructive command with a dynamic operand.
	Reject bool
	// Denied marks an unknown command that the hardline, custom deny or default
	// deny patterns block. It is only evaluated for unknown commands.
	Denied bool
	// Reason explains why the classifier returned an unknown verdict.
	Reason string
}

// AssessExecCommand classifies command with the same AST risk guard the exec
// tool uses. The boolean result is false when the classifier is inactive
// (Windows or enable_deny_patterns=false), in which case there is no unknown
// verdict.
//
// This helper exposes the risk layer and, for unknown commands, the deny
// layers (hardline, custom deny and default deny). The exec guard runs every
// layer again before execution and remains authoritative; the workspace
// boundary is not assessed here.
func AssessExecCommand(command string, cfg *config.Config) (ExecRiskAssessment, bool) {
	if cfg == nil || !cfg.Tools.Exec.EnableDenyPatterns || !posixShell {
		return ExecRiskAssessment{}, false
	}

	guard, err := newRiskGuard(cfg.Tools.Exec.RiskThreshold, true)
	if err != nil {
		return ExecRiskAssessment{}, false
	}

	analysis := shellguard.Analyze(command)
	assessment := guard.AssessFile(command, analysis.RiskFile)
	result := ExecRiskAssessment{
		Unknown: assessment.Verdict == shellguard.VerdictUnknown,
		Reject:  assessment.Verdict == shellguard.VerdictReject,
		Reason:  assessment.Reason,
	}
	if result.Unknown && !result.Reject {
		result.Denied = execDenyPatternsBlock(command, cfg, analysis)
	}
	return result, true
}

// execDenyPatternsBlock reports whether the deny layers of the exec guard
// (hardline, custom deny, default deny) block command. It reuses the guard
// itself on a tool that carries only those layers, so the verdict cannot drift
// from execution. Keep the pattern sets in sync with NewExecToolWithConfig.
func execDenyPatternsBlock(command string, cfg *config.Config, analysis shellguard.Analysis) bool {
	execConfig := cfg.Tools.Exec
	customDeny, err := compilePatterns(execConfig.CustomDenyPatterns, regexp.Compile, "custom deny")
	if err != nil {
		return false
	}
	customAllow, err := compilePatterns(execConfig.CustomAllowPatterns, compileFullMatchPattern, "custom allow")
	if err != nil {
		return false
	}
	t := &ExecTool{
		blockOpaque: true,
		hardlineDenyPatterns: append(
			slices.Clone(hardlineDenyPatterns), selfProtectionHardlinePatterns()...,
		),
		customDenyPatterns:  customDeny,
		customAllowPatterns: customAllow,
	}
	return t.guardCommandWithAnalysis(command, "", analysis) != nil
}
