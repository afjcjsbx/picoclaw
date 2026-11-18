package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newBraceGuardTool(t *testing.T) (*ExecTool, string) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	require.NoError(t, err)
	return tool, workspace
}

func TestExecGuardExpandsBraceGroupsBeforeHardlineAndRisk(t *testing.T) {
	tool, workspace := newBraceGuardTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")

	blocked := []string{
		`{rm,-rf,/}`,
		`{rm,-rf,/etc}`,
		`rm -rf {/etc,/tmp}`,
		`{chmod,-R,777,/}`,
	}
	for _, command := range blocked {
		require.NotEmpty(t, tool.guardCommandContext(ctx, command, workspace),
			"expected blocked: %s", command)
	}
}

func TestExecGuardAllowsBenignBraceGroups(t *testing.T) {
	tool, workspace := newBraceGuardTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")

	allowed := []string{
		`{echo,hello}`,
		`echo {alpha,beta}`,
		`echo '{rm,-rf,/}'`,
	}
	for _, command := range allowed {
		require.Empty(t, tool.guardCommandContext(ctx, command, workspace),
			"expected allowed: %s", command)
	}
}
