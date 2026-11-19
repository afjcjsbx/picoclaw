package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func execStateContext(agentID, sessionKey string) context.Context {
	return WithToolSessionContext(context.Background(), agentID, sessionKey, nil)
}

func TestExecTool_StateRemembersCwd(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := newTestExecTool(workspace, true)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-1")

	first := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "pwd",
		"cwd":     target,
	})
	require.False(t, first.IsError, first.ForLLM)
	require.Contains(t, first.ForLLM, "state-cwd-target")

	second := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, second.IsError, second.ForLLM)
	require.Contains(t, second.ForLLM, "state-cwd-target")

	other := tool.Execute(execStateContext("agent-a", "session-2"), map[string]any{
		"action":  "run",
		"command": "pwd",
	})
	require.False(t, other.IsError, other.ForLLM)
	require.NotContains(t, other.ForLLM, "state-cwd-target")
}

func TestExecTool_StateExplicitCwdReplacesRemembered(t *testing.T) {
	workspace := t.TempDir()
	firstDir := filepath.Join(workspace, "state-cwd-first")
	secondDir := filepath.Join(workspace, "state-cwd-second")
	require.NoError(t, os.MkdirAll(firstDir, 0o755))
	require.NoError(t, os.MkdirAll(secondDir, 0o755))

	tool, err := newTestExecTool(workspace, true)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-1")

	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": "pwd", "cwd": firstDir,
	}).IsError)
	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": "pwd", "cwd": secondDir,
	}).IsError)

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, "state-cwd-second")
	require.NotContains(t, result.ForLLM, "state-cwd-first")
}

func TestExecTool_StateFallsBackWhenRememberedCwdDisappears(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-gone")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := newTestExecTool(workspace, true)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-1")

	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": "pwd", "cwd": target,
	}).IsError)
	require.NoError(t, os.RemoveAll(target))

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "state-cwd-gone")
	require.Contains(t, result.ForLLM, filepath.Base(workspace))
}

func TestExecTool_StateCapturesCwdInsideCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cwd observation is implemented by the POSIX shell wrapper")
	}

	workspace := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "state-cwd-inside"), 0o755))

	tool, err := newTestExecTool(workspace, true)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-1")

	first := tool.Execute(ctx, map[string]any{
		"action": "run", "command": "cd state-cwd-inside",
	})
	require.False(t, first.IsError, first.ForLLM)
	require.Contains(t, first.ForLLM, "state-cwd-inside")

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, "state-cwd-inside")
	require.Contains(t, result.ForLLM, `"cwd":`)
}

func TestExecTool_StateRequiresSessionContext(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-no-session")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := newTestExecTool(workspace, false)
	require.NoError(t, err)
	ctx := context.Background()

	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": "pwd", "cwd": target,
	}).IsError)

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "state-cwd-no-session")
	require.Contains(t, result.ForLLM, filepath.Base(workspace))
}

func TestExecTool_StateNotUpdatedWhenCommandBlocked(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-blocked")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := newTestExecTool(workspace, true)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-blocked")

	blocked := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "sudo true",
		"cwd":     target,
	})
	require.True(t, blocked.IsError)

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "state-cwd-blocked")
	require.Contains(t, result.ForLLM, filepath.Base(workspace))
}

func TestExecTool_ClearSessionResetsState(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-clear")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := newTestExecTool(workspace, false)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-clear")

	first := tool.Execute(ctx, map[string]any{
		"action": "run", "command": "pwd", "cwd": target,
	})
	require.False(t, first.IsError, first.ForLLM)
	require.Contains(t, first.ForLLM, "state-cwd-clear")

	tool.ClearSession("agent-a", "session-clear")

	second := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, second.IsError, second.ForLLM)
	require.NotContains(t, second.ForLLM, "state-cwd-clear")
	require.Contains(t, second.ForLLM, filepath.Base(workspace))
}

func TestExecStateStore_ConcurrentAccessAndBound(t *testing.T) {
	store := newExecStateStore()

	var wg sync.WaitGroup
	for i := 0; i < maxExecStateEntries*2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := execStateContext("agent-a", fmt.Sprintf("session-%d", i))
			store.rememberCwd(ctx, fmt.Sprintf("/tmp/state-%d", i))
			_ = store.load(ctx)
		}(i)
	}
	wg.Wait()

	store.mu.Lock()
	defer store.mu.Unlock()
	require.LessOrEqual(t, len(store.entries), maxExecStateEntries)
}

func TestExecStateStore_OneEntryPerConversation(t *testing.T) {
	store := newExecStateStore()
	ctx := execStateContext("agent-a", "session-1")

	store.rememberCwd(ctx, "/tmp/work")
	count, tracked := store.recordBlock(ctx, "rm -rf /", "blocked")
	require.True(t, tracked)
	require.Equal(t, 1, count)
	require.Len(t, store.entries, 1, "cwd and block streak share the conversation's entry")

	count, _ = store.recordBlock(ctx, "rm -rf /", "blocked")
	require.Equal(t, 2, count)

	store.resetBlock(ctx)
	require.Equal(t, "/tmp/work", store.load(ctx), "an allowed run must not forget the cwd")
	count, _ = store.recordBlock(ctx, "rm -rf /", "blocked")
	require.Equal(t, 1, count, "an allowed run restarts the streak")

	store.clear(execStateKey{agentID: "agent-a", sessionKey: "session-1"})
	require.Empty(t, store.load(ctx))
	require.Empty(t, store.entries)

	_, tracked = store.recordBlock(context.Background(), "x", "y")
	require.False(t, tracked, "a call without session context keeps no state")
}
