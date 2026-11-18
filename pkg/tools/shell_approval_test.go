package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
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
	tool.approvalReviewer = reviewer
	return tool, reviewer
}

func TestSmartApprovalBlocksHighRiskWithoutReview(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, approvalApprove)

	got := tool.guardCommand("chmod +x script.sh", t.TempDir())
	if !strings.Contains(got, "risk classifier") {
		t.Fatalf("guardCommand() = %q, want static risk block", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalBlocksDefaultDenyPatternWithoutReview(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, approvalApprove)
	got := tool.guardCommand("echo $(whoami)", t.TempDir())
	if !strings.Contains(got, "dangerous pattern") {
		t.Fatalf("guardCommand() = %q, want static deny block", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalReviewsUnknownRisk(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, approvalApprove)
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

func TestSmartApprovalEscalateBlocksByDefault(t *testing.T) {
	t.Parallel()

	tool, reviewer := newSmartTestTool(t, nil, approvalEscalate)
	got := tool.guardCommand("sh script.sh", t.TempDir())
	if !strings.Contains(got, "ESCALATE") {
		t.Fatalf("guardCommand() = %q, want escalate block", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
}

func TestSmartApprovalEscalateCanBeApprovedByConfig(t *testing.T) {
	t.Parallel()

	cfg := smartApprovalTestConfig()
	cfg.Approvals.OnEscalate = config.ApprovalEscalateApprove
	tool, reviewer := newSmartTestTool(t, cfg, approvalEscalate)

	if got := tool.guardCommand("sh script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed after escalated approval", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
}

func TestSmartApprovalDenyStillBlocksWhenEscalateApproved(t *testing.T) {
	t.Parallel()

	cfg := smartApprovalTestConfig()
	cfg.Approvals.OnEscalate = config.ApprovalEscalateApprove
	tool, reviewer := newSmartTestTool(t, cfg, approvalDeny)

	got := tool.guardCommand("sh script.sh", t.TempDir())
	if !strings.Contains(got, "(DENY)") {
		t.Fatalf("guardCommand() = %q, want DENY block", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
}

func TestSmartApprovalReviewErrorBlocksEvenWhenEscalateApproved(t *testing.T) {
	t.Parallel()

	cfg := smartApprovalTestConfig()
	cfg.Approvals.OnEscalate = config.ApprovalEscalateApprove
	tool, reviewer := newSmartTestTool(t, cfg, approvalEscalate)
	reviewer.err = errors.New("provider unavailable")

	got := tool.guardCommand("sh script.sh", t.TempDir())
	if !strings.Contains(got, "could not review") {
		t.Fatalf("guardCommand() = %q, want fail-closed review error", got)
	}
}

func TestSmartApprovalReviewsUnknownAtCriticalThreshold(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.RiskThreshold = "critical"

	tool, reviewer := newSmartTestTool(t, cfg, approvalApprove)
	if got := tool.guardCommand("sh script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed after approval", got)
	}
	if reviewer.calls != 1 {
		t.Fatalf("reviewer calls = %d, want 1", reviewer.calls)
	}
}

func TestSmartApprovalAllowsHighRiskBelowThresholdWithoutReview(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.RiskThreshold = "critical"

	tool, reviewer := newSmartTestTool(t, cfg, approvalDeny)
	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want high risk allowed without review", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalBlocksHighRiskAboveThresholdWithoutReview(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.RiskThreshold = "medium"

	tool, reviewer := newSmartTestTool(t, cfg, approvalApprove)
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

	tool, reviewer := newSmartTestTool(t, nil, approvalApprove)
	if got := tool.guardCommand("ls -la", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalSkipsUnlockedCommands(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.CustomUnlockPatterns = []string{`^sh script\.sh$`}
	tool, reviewer := newSmartTestTool(t, cfg, approvalDeny)
	if got := tool.guardCommand("sh script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want allowed by unlock pattern", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalUnavailableFailsClosed(t *testing.T) {
	t.Parallel()

	tool, _ := newSmartTestTool(t, nil, approvalApprove)
	tool.approvalReviewer = nil

	got := tool.guardCommand("sh script.sh", t.TempDir())
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
			tool, reviewer := newSmartTestTool(t, nil, approvalApprove)
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
			tool, reviewer := newSmartTestTool(t, nil, approvalApprove)
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
	tool, reviewer := newSmartTestTool(t, cfg, approvalApprove)

	got := tool.guardCommand("echo forbidden", t.TempDir())
	if !strings.Contains(got, "dangerous pattern") {
		t.Fatalf("guardCommand() = %q, want custom deny block", got)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestSmartApprovalCustomUnlockStillApplies(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Tools.Exec.CustomUnlockPatterns = []string{`^chmod \+x script\.sh$`}
	tool, reviewer := newSmartTestTool(t, cfg, approvalDeny)

	if got := tool.guardCommand("chmod +x script.sh", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want explicit unlock", got)
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
	if tool.approvalReviewer == nil {
		t.Fatal("approvalReviewer is nil, want a configured guardian")
	}
	if tool.approvalOnEscalate != config.ApprovalEscalateBlock {
		t.Fatalf(
			"approvalOnEscalate = %q, want %q",
			tool.approvalOnEscalate, config.ApprovalEscalateBlock,
		)
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

	if got := tool.guardCommand("sh script.sh", t.TempDir()); got != "" {
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
	got := tool.guardCommand("sh script.sh", t.TempDir())
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
		SmartTimeoutSeconds: 5,
	}
	return cfg
}
