package tools

import (
	"regexp"
	"runtime"
	"slices"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// newRiskGuard builds the AST-based command classifier. It is part of the
// optional built-in guard set, so enable_deny_patterns=false disables it just
// like the regex deny list. Hardline and custom deny patterns still apply
// independently.
//
// Windows is skipped because the exec tool runs PowerShell there and the
// classifier's command table describes POSIX tools.
func newRiskGuard(cfg *config.Config, enabled bool) (*shellguard.Guard, error) {
	if !enabled || runtime.GOOS == "windows" {
		return nil, nil
	}

	threshold := "medium"
	if cfg != nil && cfg.Tools.Exec.RiskThreshold != "" {
		threshold = cfg.Tools.Exec.RiskThreshold
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
// This helper exposes the risk layer and, for unknown commands, the regex deny
// layers. The exec guard runs every layer again before execution and remains
// authoritative; the workspace boundary is not assessed here.
func AssessExecCommand(command string, cfg *config.Config) (ExecRiskAssessment, bool) {
	if cfg == nil || !cfg.Tools.Exec.EnableDenyPatterns || runtime.GOOS == "windows" {
		return ExecRiskAssessment{}, false
	}

	guard, err := newRiskGuard(cfg, true)
	if err != nil {
		return ExecRiskAssessment{}, false
	}

	analysis := shellguard.Analyze(command)
	assessment := guard.AssessFile(command, analysis.RiskFile)
	result := ExecRiskAssessment{
		Unknown: assessment.Unknown,
		Reject:  assessment.Reject,
		Reason:  assessment.Reason,
	}
	if result.Unknown && !result.Reject {
		result.Denied = execDenyPatternsBlock(command, cfg, analysis)
	}
	return result, true
}

// execDenyPatternsBlock reports whether the regex layers of the exec guard
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
		denyPatterns: defaultDenyPatterns,
		hardlineDenyPatterns: append(
			slices.Clone(hardlineDenyPatterns), selfProtectionHardlinePatterns()...,
		),
		customDenyPatterns:  customDeny,
		customAllowPatterns: customAllow,
	}
	return t.guardCommandWithAnalysis(command, "", analysis) != ""
}
