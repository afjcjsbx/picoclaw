package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newSmartFloorTool(t *testing.T, restrict bool, threshold string, verdict approvalVerdict) (*ExecTool, *fakeApprovalReviewer, string) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Tools.Exec.RiskThreshold = threshold
	cfg.Approvals = config.ApprovalsConfig{
		Mode:       config.ApprovalModeSmart,
		Model:      "go-glm",
		OnEscalate: config.ApprovalEscalateApprove,
	}
	tool, err := NewExecToolWithConfig(workspace, restrict, cfg)
	require.NoError(t, err)
	reviewer := &fakeApprovalReviewer{verdict: verdict}
	tool.approvalEnabled = true
	tool.approvalReviewer = reviewer
	return tool, reviewer, workspace
}

func TestSmartApprovalCriticalFloorCannotBeDelegated(t *testing.T) {
	tool, reviewer, workspace := newSmartFloorTool(t, true, "critical", approvalApprove)
	ctx := WithToolContext(context.Background(), "cli", "test")

	blocked := []struct {
		name    string
		command string
		want    string
	}{
		{"critical", "rm -rf ./build", "smart-approval floor"},
		{"hardline", "rm -rf /", "dangerous pattern"},
		{"workspace", "cat /etc/passwd", "path outside working dir"},
		{"interpreter", `python3 -c "import shutil; shutil.rmtree('/tmp/canary')"`, "outside the workspace"},
		{"brace", `{rm,-rf,/}`, "dangerous pattern"},
	}
	for _, tc := range blocked {
		reason := tool.guardCommandContext(ctx, tc.command, workspace)
		require.NotEmpty(t, reason, "%s: expected blocked", tc.name)
		require.Contains(t, reason, tc.want, "%s: unexpected reason", tc.name)
	}
	require.Zero(t, reviewer.calls, "the Guardian must never see a floor command")
}

func TestSmartApprovalCriticalFloorIsSmartOnly(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Tools.Exec.RiskThreshold = "critical"
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	require.NoError(t, err)
	ctx := WithToolContext(context.Background(), "cli", "test")

	// The static layer at threshold=critical deliberately allows a critical
	// workspace command. The smart floor is stricter and blocks it above.
	require.Empty(t, tool.guardCommandContext(ctx, "rm -rf ./build", workspace))
}

func TestSmartApprovalFloorStillReviewsUnknownCommands(t *testing.T) {
	tool, reviewer, workspace := newSmartFloorTool(t, true, "medium", approvalApprove)
	ctx := WithToolContext(context.Background(), "cli", "test")

	reason := tool.guardCommandContext(ctx, "bash <(printf 'echo hi')", workspace)
	require.Empty(t, reason)
	require.Equal(t, 1, reviewer.calls, "unknown command should still reach the Guardian")
	require.True(t, strings.Contains(reviewer.command, "bash"))
}
