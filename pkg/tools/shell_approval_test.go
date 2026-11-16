package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

type fakeApprovalReviewer struct {
	verdict     approvalVerdict
	err         error
	calls       int
	command     string
	description string
}

func (r *fakeApprovalReviewer) Review(
	_ context.Context,
	command string,
	description string,
) (approvalVerdict, error) {
	r.calls++
	r.command = command
	r.description = description
	return r.verdict, r.err
}

func newSmartTestTool(
	t *testing.T,
	cfg *config.Config,
	threshold shellguard.RiskLevel,
	verdict approvalVerdict,
) (*ExecTool, *fakeApprovalReviewer) {
	t.Helper()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error = %v", err)
	}
	reviewer := &fakeApprovalReviewer{verdict: verdict}
	tool.approvalEnabled = true
	tool.approvalThreshold = threshold
	tool.approvalReviewer = reviewer
	return tool, reviewer
}

func TestSmartApprovalReviewsHighRisk(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict approvalVerdict
		want    string
	}{
		{name: "approve", verdict: approvalApprove, want: ""},
		{name: "deny", verdict: approvalDeny, want: "DENY"},
		{name: "escalate", verdict: approvalEscalate, want: "ESCALATE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskHigh, tt.verdict)

			got := tool.guardCommand("chmod +x script.sh", t.TempDir())
			if tt.want == "" {
				if got != "" {
					t.Fatalf("guardCommand() = %q, want allowed", got)
				}
			} else if !strings.Contains(got, tt.want) {
				t.Fatalf("guardCommand() = %q, want substring %q", got, tt.want)
			}
			if reviewer.calls != 1 {
				t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
			}
			if !strings.Contains(reviewer.description, "risk level high") {
				t.Errorf("description = %q, want risk level high", reviewer.description)
			}
		})
	}
}

func TestSmartApprovalReviewsDefaultDenyPattern(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskHigh, approvalApprove)
	if got := tool.guardCommand("echo $(whoami)", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed after approval", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if !strings.Contains(reviewer.description, "built-in dangerous-command pattern") {
		t.Errorf("description = %q, want built-in pattern reason", reviewer.description)
	}
}

func TestSmartApprovalReviewsUnknownRisk(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskHigh, approvalApprove)
	if got := tool.guardCommand("sh script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed after approval", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
	if !strings.Contains(reviewer.description, "cannot be inspected") {
		t.Errorf("description = %q, want uninspectable script reason", reviewer.description)
	}
}

func TestSmartApprovalUnknownReviewedAtCriticalThreshold(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskCritical, approvalApprove)
	if got := tool.guardCommand("sh script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed after approval", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
}

func TestSmartApprovalCriticalThresholdSkipsHighRisk(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.RiskThreshold = "critical"
	cfg.Tools.Exec.RiskAllowThreshold = "critical"

	tool, reviewer := newSmartTestTool(t, cfg, shellguard.RiskCritical, approvalDeny)
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want high risk allowed without review", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalCriticalThresholdStillBlocksAboveRiskThreshold(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.RiskThreshold = "medium"

	tool, reviewer := newSmartTestTool(t, cfg, shellguard.RiskCritical, approvalApprove)
	got := tool.guardCommand("chmod +x script.sh", t.TempDir())
	if !strings.Contains(got, "risk classifier") {
		t.Fatalf("guardCommand() = %q, want static risk block", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalSkipsSafeCommands(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskHigh, approvalApprove)
	if got := tool.guardCommand("ls -la", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalUnavailableFailsClosed(t *testing.T) {
	t.Parallel()

	tool, _ := newSmartTestTool(t, nil, shellguard.RiskHigh, approvalApprove)
	tool.approvalReviewer = nil

	got := tool.guardCommand("chmod +x script.sh", t.TempDir())
	if !strings.Contains(got, "unavailable") {
		t.Fatalf("guardCommand() = %q, want unavailable block", got)
	}
}

func TestSmartApprovalDoesNotReviewHardline(t *testing.T) {
	t.Parallel()

	commands := []string{
		"rm -rf /",
		"rm -rf /etc",
		"rm -rf /usr/",
		"rm -rf /home/*",
		"rm -rf /root",
		"rm -rf /etc>/dev/null",
		"sudo rm -rf /etc",
		"sudo -u root rm -rf /etc",
		"FOO=bar rm -rf /etc",
		"sh -c 'rm -rf /etc'",
		"env rm -rf /home",
		"kill -1",
		"kill -1; echo ok",
		"kill -9 1",
		"sudo kill -1",
		"sudo -n kill -1",
		"sh -c 'kill -1'",
		"shutdown -h now",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskHigh, approvalApprove)
			got := tool.guardCommand(command, t.TempDir())
			if !strings.Contains(got, "dangerous pattern") {
				t.Fatalf("guardCommand(%q) = %q, want hardline block", command, got)
			}
			if reviewer.calls != 0 {
				t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
			}
		})
	}
}

func TestSmartApprovalDoesNotReviewDynamicDestructiveOperand(t *testing.T) {
	t.Parallel()

	commands := []string{
		`rm -f "$P"`,
		`r\m -f "$P"`,
		`env rm -f "$P"`,
		`sh -c 'rm -f "$P"'`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			tool, reviewer := newSmartTestTool(t, nil, shellguard.RiskHigh, approvalApprove)
			got := tool.guardCommand(command, t.TempDir())
			if !strings.Contains(got, "dynamic operand for destructive command") {
				t.Fatalf("guardCommand(%q) = %q, want dynamic-operand rejection", command, got)
			}
			if reviewer.calls != 0 {
				t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
			}
		})
	}
}

func TestSmartApprovalDoesNotReviewCustomDeny(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.CustomDenyPatterns = []string{`\bforbidden\b`}
	tool, reviewer := newSmartTestTool(t, cfg, shellguard.RiskHigh, approvalApprove)

	got := tool.guardCommand("echo forbidden", t.TempDir())
	if !strings.Contains(got, "dangerous pattern") {
		t.Fatalf("guardCommand() = %q, want custom deny block", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalCustomRiskAllowSkipsReview(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.RiskAllowPatterns = []string{`^chmod \+x script\.sh$`}
	cfg.Tools.Exec.RiskAllowThreshold = "high"
	tool, reviewer := newSmartTestTool(t, cfg, shellguard.RiskHigh, approvalDeny)

	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want explicit risk allow", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestHardlineAllowsSubdirectoriesOfProtectedRoots(t *testing.T) {
	t.Parallel()

	tool, err := NewExecToolWithConfig(t.TempDir(), false, nil)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error = %v", err)
	}
	got := tool.guardCommand("rm -rf /home/alice/build", t.TempDir())
	if strings.Contains(got, "dangerous pattern") {
		t.Fatalf("subdirectory delete matched hardline: %q", got)
	}
	if !strings.Contains(got, "risk classifier") {
		t.Fatalf("subdirectory delete = %q, want risk classifier block", got)
	}
}

func TestSmartApprovalConstruction(t *testing.T) {
	t.Parallel()

	cfg := smartApprovalTestConfig()
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error = %v", err)
	}
	if !tool.approvalEnabled {
		t.Fatal("approvalEnabled = false, want true")
	}
	if tool.approvalThreshold != shellguard.RiskCritical {
		t.Errorf("approvalThreshold = %s, want critical", tool.approvalThreshold)
	}
	if tool.approvalReviewer == nil {
		t.Fatal("approvalReviewer is nil, want a configured guardian")
	}
}

func TestSmartApprovalWorksWithoutDefaultDenyPatterns(t *testing.T) {
	t.Parallel()

	cfg := smartApprovalTestConfig()
	cfg.Tools.Exec.EnableDenyPatterns = false
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error = %v", err)
	}
	reviewer := &fakeApprovalReviewer{verdict: approvalApprove}
	tool.approvalReviewer = reviewer
	tool.approvalThreshold = shellguard.RiskHigh

	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed after approval", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
}

func TestSmartApprovalMisconfigurationFailsClosed(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.ModelName = ""
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeSmart}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error = %v", err)
	}
	if tool.approvalReviewer != nil {
		t.Fatal("approvalReviewer is non-nil despite a missing Guardian model")
	}
	got := tool.guardCommand("chmod +x script.sh", t.TempDir())
	if !strings.Contains(got, "unavailable") {
		t.Fatalf("guardCommand() = %q, want fail-closed block", got)
	}
}

func smartApprovalTestConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "guardian",
		Provider:  "openai",
		Model:     "gpt-5.4",
		APIBase:   "http://127.0.0.1:9/v1",
		APIKeys:   config.SimpleSecureStrings("test-key"),
		Enabled:   true,
	}}
	cfg.Agents.Defaults.ModelName = "guardian"
	cfg.Approvals = config.ApprovalsConfig{
		Mode:                config.ApprovalModeSmart,
		Model:               "guardian",
		SmartThreshold:      config.ApprovalSmartThresholdCritical,
		SmartTimeoutSeconds: 5,
	}
	return cfg
}
