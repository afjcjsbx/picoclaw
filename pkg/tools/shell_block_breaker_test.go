package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func newBlockBreakerTestTool(t *testing.T) *ExecTool {
	t.Helper()
	tool, err := newTestExecTool(t.TempDir(), false)
	require.NoError(t, err)
	require.NoError(t, tool.SetAllowPatterns([]string{`^echo allowed$`}))
	return tool
}

func blockBreakerContext(session string) context.Context {
	return WithToolSessionContext(context.Background(), "agent-1", session, nil)
}

func runBlockedCommand(t *testing.T, tool *ExecTool, ctx context.Context, command string) *ToolResult {
	t.Helper()
	return tool.Execute(ctx, map[string]any{"action": "run", "command": command})
}

func TestExecBlockBreakerHardStopsAfterIdenticalBlocks(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	ctx := blockBreakerContext("session-1")
	const blocked = "echo blocked"

	for i := 1; i < execBlockHardStopThreshold; i++ {
		result := runBlockedCommand(t, tool, ctx, blocked)
		require.True(t, result.IsError)
		require.Contains(t, result.ForLLM, "not in allowlist")
		require.NotContains(t, result.ForLLM, "HARD STOP", "block %d must not trip the breaker", i)
	}

	tripped := runBlockedCommand(t, tool, ctx, blocked)
	require.True(t, tripped.IsError)
	require.Contains(t, tripped.ForLLM, "not in allowlist")
	require.Contains(t, tripped.ForLLM, "HARD STOP")
	require.Contains(
		t, tripped.ForLLM,
		fmt.Sprintf("blocked this identical command %d times in a row", execBlockHardStopThreshold),
	)

	// The breaker stays open for later identical attempts.
	again := runBlockedCommand(t, tool, ctx, blocked)
	require.Contains(t, again.ForLLM, "HARD STOP")
}

func TestExecBlockBreakerResetsOnAllowedRun(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	ctx := blockBreakerContext("session-1")
	const blocked = "echo blocked"

	for i := 0; i < execBlockHardStopThreshold-1; i++ {
		require.NotContains(t, runBlockedCommand(t, tool, ctx, blocked).ForLLM, "HARD STOP")
	}

	allowed := tool.Execute(ctx, map[string]any{"action": "run", "command": "echo allowed"})
	require.False(t, allowed.IsError)

	for i := 0; i < execBlockHardStopThreshold-1; i++ {
		require.NotContains(
			t, runBlockedCommand(t, tool, ctx, blocked).ForLLM, "HARD STOP",
			"an allowed run must reset the streak",
		)
	}
}

func TestExecBlockBreakerResetsOnDifferentCommand(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	ctx := blockBreakerContext("session-1")
	const first = "echo blocked"
	const second = "echo also-blocked"

	for i := 0; i < execBlockHardStopThreshold-1; i++ {
		require.NotContains(t, runBlockedCommand(t, tool, ctx, first).ForLLM, "HARD STOP")
	}
	require.NotContains(t, runBlockedCommand(t, tool, ctx, second).ForLLM, "HARD STOP")
	require.NotContains(
		t, runBlockedCommand(t, tool, ctx, first).ForLLM, "HARD STOP",
		"an interleaved command must start a new streak",
	)

	// Three consecutive blocks of the second command still trip the breaker.
	for i := 0; i < execBlockHardStopThreshold-1; i++ {
		require.NotContains(t, runBlockedCommand(t, tool, ctx, second).ForLLM, "HARD STOP")
	}
	require.Contains(t, runBlockedCommand(t, tool, ctx, second).ForLLM, "HARD STOP")
}

func TestExecBlockBreakerIsolatedBySession(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	const blocked = "echo blocked"
	sessionA := blockBreakerContext("session-a")
	sessionB := blockBreakerContext("session-b")

	for i := 0; i < execBlockHardStopThreshold; i++ {
		runBlockedCommand(t, tool, sessionA, blocked)
	}
	require.NotContains(
		t, runBlockedCommand(t, tool, sessionB, blocked).ForLLM, "HARD STOP",
		"a new conversation must start with a clean streak",
	)
}

func TestExecBlockBreakerClearSession(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	ctx := blockBreakerContext("session-1")
	const blocked = "echo blocked"

	for i := 0; i < execBlockHardStopThreshold; i++ {
		runBlockedCommand(t, tool, ctx, blocked)
	}
	tool.ClearSession("agent-1", "session-1")
	require.NotContains(
		t, runBlockedCommand(t, tool, ctx, blocked).ForLLM, "HARD STOP",
		"ClearSession must reset the conversation",
	)
}

func TestExecBlockBreakerSkipsCallsWithoutSessionContext(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	const blocked = "echo blocked"

	for i := 0; i < execBlockHardStopThreshold*2; i++ {
		require.NotContains(
			t, runBlockedCommand(t, tool, context.Background(), blocked).ForLLM, "HARD STOP",
			"a call without agent and session context has no conversation to track",
		)
	}
}

func TestExecBlockBreakerKeepsStateBounded(t *testing.T) {
	tool := newBlockBreakerTestTool(t)
	const blocked = "echo blocked"

	for i := 0; i < maxExecBlockSessions+8; i++ {
		runBlockedCommand(t, tool, blockBreakerContext(fmt.Sprintf("session-%d", i)), blocked)
	}

	tool.blockBreaker.mu.Lock()
	size := len(tool.blockBreaker.states)
	tool.blockBreaker.mu.Unlock()
	require.LessOrEqual(t, size, maxExecBlockSessions)
}
