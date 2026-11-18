package tools

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

func TestExecDecisionRuleIDs(t *testing.T) {
	t.Parallel()
	skipOnWindows(t)

	cfg := config.DefaultConfig()
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}

	tests := []struct {
		command string
		action  shellguard.Action
		rule    string
	}{
		{"echo hi", shellguard.ActionAllow, "allow"},
		{"rm -rf /", shellguard.ActionBlock, "hardline.rm-protected-root"},
		{"echo $(whoami)", shellguard.ActionBlock, "shell.construct"},
		{"python3 script.py", shellguard.ActionReview, "risk.unknown"},
		{"echo )", shellguard.ActionBlock, "parse-error"},
	}
	for _, tt := range tests {
		decision := tool.guardCommandContext(t.Context(), tt.command, t.TempDir())
		if decision.Action != tt.action {
			t.Errorf("%q action = %s, want %s (reason %q)",
				tt.command, decision.Action, tt.action, decision.Reason)
		}
		if decision.Rule != tt.rule {
			t.Errorf("%q rule = %q, want %q", tt.command, decision.Rule, tt.rule)
		}
	}
}

func TestExecReviewDecisionWithoutApproverIsBlocked(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}

	reason := tool.guardCommand("python3 script.py", t.TempDir())
	if reason == "" {
		t.Fatal("review decision without an approver must be blocked")
	}
}

func TestExecAllowUnknownOnlyAppliesToUnknownRisk(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.AllowUnknown = true
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}

	if reason := tool.guardCommand("python3 script.py", t.TempDir()); reason != "" {
		t.Fatalf("allow_unknown should allow an opaque command, got %q", reason)
	}
	if reason := tool.guardCommand("echo )", t.TempDir()); reason == "" {
		t.Fatal("allow_unknown must not allow a parse failure")
	}
}
