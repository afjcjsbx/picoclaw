package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func execStateContext(agentID, sessionKey string) context.Context {
	return WithToolSessionContext(context.Background(), agentID, sessionKey, nil)
}

func execEchoEnv(names ...string) string {
	parts := make([]string, len(names))
	if runtime.GOOS == "windows" {
		for i, name := range names {
			parts[i] = "$env:" + name
		}
		return `Write-Output "` + strings.Join(parts, " ") + `"`
	}
	for i, name := range names {
		parts[i] = "$" + name
	}
	return "echo " + strings.Join(parts, " ")
}

func TestExecTool_StateRemembersCwd(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := NewExecTool(workspace, true)
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

	tool, err := NewExecTool(workspace, true)
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

	tool, err := NewExecTool(workspace, true)
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

func TestExecTool_StateRemembersEnv(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-env")

	first := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": execEchoEnv("STATE_ENV_A"),
		"env":     map[string]any{"STATE_ENV_A": "first-value"},
	})
	require.False(t, first.IsError, first.ForLLM)
	require.Contains(t, first.ForLLM, "first-value")

	second := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": execEchoEnv("STATE_ENV_A"),
	})
	require.False(t, second.IsError, second.ForLLM)
	require.Contains(t, second.ForLLM, "first-value")

	other := tool.Execute(execStateContext("agent-a", "session-other"), map[string]any{
		"action":  "run",
		"command": execEchoEnv("STATE_ENV_A"),
	})
	require.False(t, other.IsError, other.ForLLM)
	require.NotContains(t, other.ForLLM, "first-value")
}

func TestExecTool_StateMergesAndClearsEnv(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-env")

	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": execEchoEnv("STATE_ENV_A"),
		"env": map[string]any{"STATE_ENV_A": "one-value"},
	}).IsError)
	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": execEchoEnv("STATE_ENV_B"),
		"env": map[string]any{"STATE_ENV_B": "two-value"},
	}).IsError)

	merged := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": execEchoEnv("STATE_ENV_A", "STATE_ENV_B"),
	})
	require.False(t, merged.IsError, merged.ForLLM)
	require.Contains(t, merged.ForLLM, "one-value")
	require.Contains(t, merged.ForLLM, "two-value")

	// An explicit empty map clears the remembered overlay.
	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": execEchoEnv("STATE_ENV_A"),
		"env": map[string]any{},
	}).IsError)

	cleared := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": execEchoEnv("STATE_ENV_A", "STATE_ENV_B"),
	})
	require.False(t, cleared.IsError, cleared.ForLLM)
	require.NotContains(t, cleared.ForLLM, "one-value")
	require.NotContains(t, cleared.ForLLM, "two-value")
}

func TestExecTool_StateDoesNotCaptureCwdInsideCommand(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-inside")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := NewExecTool(workspace, false)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-1")

	changeDir := "cd state-cwd-inside"
	if runtime.GOOS == "windows" {
		changeDir = "Set-Location state-cwd-inside"
	}
	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": changeDir,
	}).IsError)

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "pwd"})
	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "state-cwd-inside")
	require.Contains(t, result.ForLLM, filepath.Base(workspace))
}

func TestExecTool_StateRequiresSessionContext(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-no-session")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := NewExecTool(workspace, false)
	require.NoError(t, err)
	ctx := context.Background()

	require.False(t, tool.Execute(ctx, map[string]any{
		"action": "run", "command": execEchoEnv("STATE_ENV_NO_SESSION"),
		"cwd": target, "env": map[string]any{"STATE_ENV_NO_SESSION": "sessionless-value"},
	}).IsError)

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "pwd; " + execEchoEnv("STATE_ENV_NO_SESSION"),
	})
	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "state-cwd-no-session")
	require.NotContains(t, result.ForLLM, "sessionless-value")
	require.Contains(t, result.ForLLM, filepath.Base(workspace))
}

func TestExecTool_StateNotUpdatedWhenCommandBlocked(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-blocked")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := NewExecTool(workspace, true)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-blocked")

	blocked := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "sudo true",
		"cwd":     target,
		"env":     map[string]any{"STATE_ENV_BLOCKED": "blocked-value"},
	})
	require.True(t, blocked.IsError)

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "pwd; " + execEchoEnv("STATE_ENV_BLOCKED"),
	})
	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "state-cwd-blocked")
	require.NotContains(t, result.ForLLM, "blocked-value")
	require.Contains(t, result.ForLLM, filepath.Base(workspace))
}

func TestExecTool_ClearSessionResetsState(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "state-cwd-clear")
	require.NoError(t, os.MkdirAll(target, 0o755))

	tool, err := NewExecTool(workspace, false)
	require.NoError(t, err)
	ctx := execStateContext("agent-a", "session-clear")

	first := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "pwd; " + execEchoEnv("STATE_ENV_CLEAR"),
		"cwd":     target,
		"env":     map[string]any{"STATE_ENV_CLEAR": "clear-value"},
	})
	require.False(t, first.IsError, first.ForLLM)
	require.Contains(t, first.ForLLM, "state-cwd-clear")
	require.Contains(t, first.ForLLM, "clear-value")

	tool.ClearSession("agent-a", "session-clear")

	second := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "pwd; " + execEchoEnv("STATE_ENV_CLEAR"),
	})
	require.False(t, second.IsError, second.ForLLM)
	require.NotContains(t, second.ForLLM, "state-cwd-clear")
	require.NotContains(t, second.ForLLM, "clear-value")
	require.Contains(t, second.ForLLM, filepath.Base(workspace))
}

func TestMergeExecStateEnv(t *testing.T) {
	effective, next, provided := mergeExecStateEnv(
		map[string]string{"A": "1"},
		map[string]string{"B": "2", "PATH": "/hacked"},
	)
	require.True(t, provided)
	require.Equal(t, map[string]string{"A": "1", "B": "2"}, effective)
	require.Equal(t, map[string]string{"A": "1", "B": "2"}, next)

	effective, next, provided = mergeExecStateEnv(map[string]string{"A": "1"}, nil)
	require.False(t, provided)
	require.Equal(t, map[string]string{"A": "1"}, effective)
	require.Nil(t, next)

	effective, next, provided = mergeExecStateEnv(map[string]string{"A": "1"}, map[string]string{})
	require.True(t, provided)
	require.Nil(t, effective)
	require.Nil(t, next)
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
			store.storeEnv(ctx, map[string]string{"IDX": fmt.Sprintf("%d", i)})
			_ = store.load(ctx)
		}(i)
	}
	wg.Wait()

	store.mu.Lock()
	defer store.mu.Unlock()
	require.LessOrEqual(t, len(store.entries), maxExecStateEntries)
}
