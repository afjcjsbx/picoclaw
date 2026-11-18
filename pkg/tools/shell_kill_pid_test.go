package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newKillPIDTestTool(t *testing.T) (*ExecTool, string) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Approvals = config.ApprovalsConfig{Mode: config.ApprovalModeOff}
	tool, err := NewExecToolWithConfig(workspace, true, cfg)
	require.NoError(t, err)
	return tool, workspace
}

func startKillPIDBackgroundSession(t *testing.T, tool *ExecTool, ctx context.Context, command string) (string, int) {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{
		"action": "run", "command": command, "background": true,
	})
	require.False(t, result.IsError, result.ForLLM)

	var payload ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
	require.NotEmpty(t, payload.SessionID)

	session, err := tool.sessionManager.Get(payload.SessionID)
	require.NoError(t, err)
	require.Greater(t, session.PID, 0)
	return payload.SessionID, session.PID
}

func TestExecKillPIDKillsSessionRoot(t *testing.T) {
	tool, _ := newKillPIDTestTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")
	sessionID, pid := startKillPIDBackgroundSession(t, tool, ctx, "sleep 30")

	result := tool.Execute(ctx, map[string]any{"action": "kill-pid", "pid": float64(pid)})
	require.False(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, strconv.Itoa(pid))

	session, err := tool.sessionManager.Get(sessionID)
	require.NoError(t, err)
	require.True(t, session.IsDone(), "killing the root must finish the session")
}

func TestExecKillPIDKillsSessionDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descendant PID helper is Unix-only")
	}
	tool, workspace := newKillPIDTestTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")
	pidFile := filepath.Join(workspace, "child.pid")
	sessionID, _ := startKillPIDBackgroundSession(
		t, tool, ctx,
		"sh -c 'sleep 30 & echo $! > "+pidFile+"; wait'",
	)

	var childPID int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && childPID > 0
	}, 5*time.Second, 20*time.Millisecond)

	result := tool.Execute(ctx, map[string]any{"action": "kill-pid", "pid": float64(childPID)})
	require.False(t, result.IsError, result.ForLLM)
	require.Eventually(t, func() bool {
		return !processAlive(childPID)
	}, 5*time.Second, 20*time.Millisecond)

	// The session shell may finish as soon as its last child exits; the point
	// is that killing the descendant is allowed and the PID disappears.
	_ = tool.Execute(ctx, map[string]any{"action": "kill", "sessionId": sessionID})
}

func TestExecKillPIDRejectsUnownedPID(t *testing.T) {
	tool, _ := newKillPIDTestTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{"action": "kill-pid", "pid": float64(os.Getpid())})
	require.True(t, result.IsError)
	require.Contains(t, result.ForLLM, "not owned by a running exec session")
}

func TestExecKillPIDRejectsInvalidPID(t *testing.T) {
	tool, _ := newKillPIDTestTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")
	for _, value := range []any{float64(0), float64(1), float64(-1), "abc", nil} {
		result := tool.Execute(ctx, map[string]any{"action": "kill-pid", "pid": value})
		require.True(t, result.IsError, "pid=%v must be rejected", value)
	}
}

func TestExecShellKillPointsToKillPID(t *testing.T) {
	tool, workspace := newKillPIDTestTool(t)
	ctx := WithToolContext(context.Background(), "cli", "test")
	reason := tool.guardCommandContext(ctx, "kill 12345", workspace)
	require.Contains(t, reason, "kill-pid")
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
