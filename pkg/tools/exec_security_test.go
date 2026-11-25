package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func newSecurityExecTool(t *testing.T, allowRemote bool) (*ExecTool, *SessionManager) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.AllowRemote = allowRemote
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm
	return tool, sm
}

func startSecuritySession(t *testing.T, tool *ExecTool, ctx context.Context, command string) string {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    command,
		"background": true,
	})
	require.False(t, result.IsError, result.ForLLM)

	var resp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
	require.NotEmpty(t, resp.SessionID)
	return resp.SessionID
}

func execSessionIDs(t *testing.T, result *ToolResult) []string {
	t.Helper()
	require.False(t, result.IsError, result.ForLLM)

	var resp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
	ids := make([]string, 0, len(resp.Sessions))
	for _, session := range resp.Sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

// TestExecTool_RemoteChannelBlockedForAllActions verifies the channel guard
// applies before dispatch, not only to action=run.
func TestExecTool_RemoteChannelBlockedForAllActions(t *testing.T) {
	tool, _ := newSecurityExecTool(t, false)
	internal := WithToolContext(context.Background(), "cli", "direct")
	id := startSecuritySession(t, tool, internal, "sleep 30")
	t.Cleanup(func() {
		tool.Execute(internal, map[string]any{"action": "kill", "sessionId": id})
	})

	remote := WithToolContext(context.Background(), "telegram", "attacker")
	cases := []struct {
		name string
		args map[string]any
	}{
		{"run", map[string]any{"action": "run", "command": "echo should-not-run"}},
		{"list", map[string]any{"action": "list"}},
		{"poll", map[string]any{"action": "poll", "sessionId": id}},
		{"wait", map[string]any{"action": "wait", "sessionId": id, "timeout": float64(1)}},
		{"read", map[string]any{"action": "read", "sessionId": id}},
		{"write", map[string]any{"action": "write", "sessionId": id, "data": "x"}},
		{"kill", map[string]any{"action": "kill", "sessionId": id}},
		{"send-keys", map[string]any{"action": "send-keys", "sessionId": id, "keys": "enter"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(remote, tc.args)
			require.True(t, result.IsError, result.ForLLM)
			require.Contains(t, result.ForLLM, "restricted to internal channels")
		})
	}

	// The blocked kill must not have terminated the internal session.
	poll := tool.Execute(internal, map[string]any{"action": "poll", "sessionId": id})
	require.False(t, poll.IsError, poll.ForLLM)
	require.Contains(t, poll.ForLLM, `"status":"running"`)
}

// TestExecTool_ChannelArgumentCannotBypassRemoteGuard verifies channel
// selection never trusts model-controlled tool arguments.
func TestExecTool_ChannelArgumentCannotBypassRemoteGuard(t *testing.T) {
	tool, _ := newSecurityExecTool(t, false)

	for _, ctx := range []context.Context{
		context.Background(),
		WithToolContext(context.Background(), "telegram", "attacker"),
	} {
		result := tool.Execute(ctx, map[string]any{
			"action":    "run",
			"command":   "echo bypassed",
			"__channel": "cli",
		})
		require.True(t, result.IsError, result.ForLLM)
		require.Contains(t, result.ForLLM, "restricted to internal channels")
	}

	registry := NewToolRegistry()
	registry.Register(tool)
	result := registry.ExecuteWithContext(
		context.Background(),
		"exec",
		map[string]any{
			"action":    "run",
			"command":   "echo bypassed",
			"__channel": "cli",
		},
		"",
		"",
		nil,
	)
	require.True(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, `unexpected property "__channel"`)
}

// TestExecTool_SessionOwnershipIsolation verifies remote callers can only see
// and operate on sessions created by the same agent, channel and chat.
func TestExecTool_SessionOwnershipIsolation(t *testing.T) {
	tool, _ := newSecurityExecTool(t, true)

	ownerA := WithToolSessionContext(
		WithToolContext(context.Background(), "telegram", "chat-a"),
		"agent-a", "session-a", nil,
	)
	ownerB := WithToolSessionContext(
		WithToolContext(context.Background(), "telegram", "chat-b"),
		"agent-b", "session-b", nil,
	)
	sameChatOtherAgent := WithToolSessionContext(
		WithToolContext(context.Background(), "telegram", "chat-a"),
		"agent-b", "session-b", nil,
	)
	otherChatSameAgent := WithToolSessionContext(
		WithToolContext(context.Background(), "telegram", "chat-b"),
		"agent-a", "session-a", nil,
	)
	internal := WithToolContext(context.Background(), "cli", "direct")

	idA := startSecuritySession(t, tool, ownerA, "sh -c 'echo SESSION-A; sleep 30'")
	idB := startSecuritySession(t, tool, ownerB, "sh -c 'echo SESSION-B; sleep 30'")
	t.Cleanup(func() {
		tool.Execute(internal, map[string]any{"action": "kill", "sessionId": idA})
		tool.Execute(internal, map[string]any{"action": "kill", "sessionId": idB})
	})

	require.ElementsMatch(t, []string{idA}, execSessionIDs(t, tool.Execute(ownerA, map[string]any{"action": "list"})))
	require.ElementsMatch(t, []string{idB}, execSessionIDs(t, tool.Execute(ownerB, map[string]any{"action": "list"})))
	require.ElementsMatch(
		t,
		[]string{idA, idB},
		execSessionIDs(t, tool.Execute(internal, map[string]any{"action": "list"})),
	)
	require.Empty(t, execSessionIDs(t, tool.Execute(sameChatOtherAgent, map[string]any{"action": "list"})))
	require.Empty(t, execSessionIDs(t, tool.Execute(otherChatSameAgent, map[string]any{"action": "list"})))

	crossCallerActions := []map[string]any{
		{"action": "poll", "sessionId": idA},
		{"action": "wait", "sessionId": idA, "timeout": float64(1)},
		{"action": "read", "sessionId": idA},
		{"action": "write", "sessionId": idA, "data": "x"},
		{"action": "kill", "sessionId": idA},
		{"action": "send-keys", "sessionId": idA, "keys": "enter"},
	}
	for _, args := range crossCallerActions {
		result := tool.Execute(ownerB, args)
		require.True(t, result.IsError, "%v: %s", args["action"], result.ForLLM)
		require.Contains(t, result.ForLLM, "session not found")
	}

	// The denied kill must not have terminated A's session.
	poll := tool.Execute(ownerA, map[string]any{"action": "poll", "sessionId": idA})
	require.False(t, poll.IsError, poll.ForLLM)
	require.Contains(t, poll.ForLLM, `"status":"running"`)

	// The owner still has full access.
	require.Eventually(t, func() bool {
		read := tool.Execute(ownerA, map[string]any{"action": "read", "sessionId": idA})
		return !read.IsError && strings.Contains(read.ForLLM, "SESSION-A")
	}, 2*time.Second, 20*time.Millisecond)

	kill := tool.Execute(ownerA, map[string]any{"action": "kill", "sessionId": idA})
	require.False(t, kill.IsError, kill.ForLLM)
}
