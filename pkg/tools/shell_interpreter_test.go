package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestExecGuardBlocksInterpreterCodeOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	require.NoError(t, err)
	ctx := WithToolContext(context.Background(), "cli", "test")

	blocked := []string{
		`python3 -c "import shutil; shutil.rmtree('/tmp/picoclaw-test-canary')"`,
		`perl -e 'use File::Path qw(remove_tree); remove_tree("/tmp/picoclaw-test-canary")'`,
		`node -e "require('fs').rmSync('/tmp/picoclaw-test-canary',{recursive:true,force:true})"`,
		`ruby -e 'require "fileutils"; FileUtils.rm_rf("/tmp/picoclaw-test-canary")'`,
		`php -r "unlink('/tmp/picoclaw-test-canary');"`,
		`lua -e 'os.remove("/tmp/picoclaw-test-canary")'`,
		`Rscript -e 'unlink("/tmp/picoclaw-test-canary", recursive=TRUE)'`,
		`awk 'BEGIN{system("rm -rf /tmp/picoclaw-test-canary")}'`,
		`sed -e '1e rm -rf /tmp/picoclaw-test-canary' /dev/null`,
		`osascript -e 'do shell script "rm -rf /tmp/picoclaw-test-canary"'`,
		`python3 -c "open('/tmp/picoclaw-test-canary','w').write('x')"`,
		`node -e "require('fs').writeFileSync('/tmp/picoclaw-test-canary','x')"`,
		`python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" /tmp/picoclaw-test-canary`,
		`python3 -c "open('$HOME/outside.txt','w').write('x')"`,
	}
	for _, command := range blocked {
		reason := tool.guardCommandContext(ctx, command, workspace)
		require.NotEmpty(t, reason, "expected blocked: %s", command)
		require.Contains(t, reason, "blocked", "unexpected reason for %s: %s", command, reason)
	}
}

func TestExecGuardAllowsPureInterpreterCode(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	require.NoError(t, err)
	ctx := WithToolContext(context.Background(), "cli", "test")

	allowed := []string{
		`python3 -c "print(1+1)"`,
		`node -e "console.log(1)"`,
		`ruby -e "puts 1"`,
		`php -r "echo 1;"`,
		`awk '{print $1}' notes.txt`,
		`sed -n '1p' notes.txt`,
	}
	for _, command := range allowed {
		require.Empty(t, tool.guardCommandContext(ctx, command, workspace),
			"expected allowed: %s", command)
	}
}
