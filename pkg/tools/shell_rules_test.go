package tools

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestExecCustomRulesBypassExplicitLayers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rule    config.ExecRuleConfig
		command string
		blocked bool
	}{
		{
			name: "shell constructs",
			rule: config.ExecRuleConfig{
				Pattern: `^echo \$\(date\)$`,
				Action:  "allow",
				Bypass:  []string{"shell_constructs"},
			},
			command: `echo $(date)`,
			blocked: false,
		},
		{
			name: "risk",
			rule: config.ExecRuleConfig{
				Pattern: `^chmod \+x script\.sh$`,
				Action:  "allow",
				Bypass:  []string{"risk"},
			},
			command: "chmod +x script.sh",
			blocked: false,
		},
		{
			name: "approval",
			rule: config.ExecRuleConfig{
				Pattern: `^python3 script\.py$`,
				Action:  "allow",
				Bypass:  []string{"approval"},
			},
			command: "python3 script.py",
			blocked: false,
		},
		{
			name: "unlock all",
			rule: config.ExecRuleConfig{
				Pattern: `^python3 script\.py$`,
				Action:  "allow",
				Bypass:  []string{"shell_constructs", "risk", "approval"},
			},
			command: "python3 script.py",
			blocked: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.Tools.Exec.CustomRules = []config.ExecRuleConfig{tt.rule}
			tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
			if err != nil {
				t.Fatalf("NewExecToolWithConfig: %v", err)
			}
			got := tool.guardCommand(tt.command, t.TempDir())
			if tt.blocked && got == "" {
				t.Fatalf("%q should be blocked", tt.command)
			}
			if !tt.blocked && got != "" {
				t.Fatalf("%q should be allowed, got %q", tt.command, got)
			}
		})
	}
}

func TestExecCustomDenyRuleWinsOverAllowRule(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.CustomRules = []config.ExecRuleConfig{
		{Pattern: `^rm .*$`, Action: "deny"},
		{Pattern: `^rm .*$`, Action: "allow", Bypass: []string{"risk", "approval"}},
	}
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if got := tool.guardCommand("rm file.txt", t.TempDir()); !strings.Contains(
		got,
		"dangerous pattern",
	) {
		t.Fatalf("custom deny must win over allow, got %q", got)
	}
}

func TestExecCustomRulesRejectInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule config.ExecRuleConfig
		want string
	}{
		{"unknown action", config.ExecRuleConfig{Pattern: `^x$`, Action: "yolo"}, "action"},
		{
			"unknown bypass",
			config.ExecRuleConfig{Pattern: `^x$`, Action: "allow", Bypass: []string{"hardline"}},
			"bypass",
		},
		{
			"allow without bypass",
			config.ExecRuleConfig{Pattern: `^x$`, Action: "allow"},
			"at least one bypass",
		},
		{
			"deny with bypass",
			config.ExecRuleConfig{Pattern: `^x$`, Action: "deny", Bypass: []string{"risk"}},
			"cannot declare",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.DefaultConfig()
			cfg.Tools.Exec.CustomRules = []config.ExecRuleConfig{tt.rule}
			_, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestExecGuardLayerTogglesAreIndependent(t *testing.T) {
	t.Parallel()

	enabled := true
	disabled := false
	cfg := config.DefaultConfig()
	cfg.Tools.Exec.EnableDenyPatterns = false
	cfg.Tools.Exec.AllowUnknown = true
	cfg.Tools.Exec.ShellConstructs = &disabled
	cfg.Tools.Exec.RiskClassifier = &enabled
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); !strings.Contains(
		got,
		"risk classifier",
	) {
		t.Fatalf("risk classifier should remain active, got %q", got)
	}
	if got := tool.guardCommand("echo $(date)", t.TempDir()); got != "" {
		t.Fatalf("shell constructs should be disabled, got %q", got)
	}

	cfg = config.DefaultConfig()
	cfg.Tools.Exec.EnableDenyPatterns = false
	cfg.Tools.Exec.AllowUnknown = true
	cfg.Tools.Exec.ShellConstructs = &enabled
	cfg.Tools.Exec.RiskClassifier = &disabled
	tool, err = NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	if got := tool.guardCommand("echo $(date)", t.TempDir()); !strings.Contains(
		got,
		"dangerous pattern",
	) {
		t.Fatalf("shell constructs should be active, got %q", got)
	}
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("risk classifier should be disabled, got %q", got)
	}
}
