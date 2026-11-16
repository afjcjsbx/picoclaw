package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoverGoroutine(t *testing.T) {
	ran := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverGoroutine("test", func() { ran = true })
		panic("boom")
	}()
	<-done
	require.True(t, ran)

	// No panic: the hook must not run.
	ran = false
	func() {
		defer recoverGoroutine("test", func() { ran = true })
	}()
	require.False(t, ran)
}

func TestReadObservedCwd(t *testing.T) {
	path, err := newCwdProbeFile()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(path) })

	require.NoError(t, os.WriteFile(path, []byte("/workspace/sub\n"), 0o600))
	require.Equal(t, "/workspace/sub", readObservedCwd(path))

	require.NoError(t, os.Remove(path))
	require.Empty(t, readObservedCwd(path))
}

func TestWrapCommandForCwdSkipsLineContinuation(t *testing.T) {
	command := "echo hi \\"
	wrapped, ok := wrapCommandForCwd(command, "/tmp/probe")
	require.False(t, ok)
	require.Equal(t, command, wrapped)
}

func TestInformativeExitNote(t *testing.T) {
	require.Contains(t, informativeExitNote("grep missing file", 1), "no matches")
	require.Contains(t, informativeExitNote("diff a b", 1), "files differ")
	require.Contains(t, informativeExitNote("false", 1), "")
	require.Empty(t, informativeExitNote("grep missing file", 0))
	require.Empty(t, informativeExitNote("grep missing file", 2))
}

func TestExecToolAddsInformativeExitNote(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o600))

	tool, err := newTestExecTool(dir, false)
	require.NoError(t, err)
	ctx := WithToolContext(context.Background(), "cli", "test")

	result := tool.Execute(ctx, map[string]any{
		"action": "run", "command": "grep missing a.txt",
	})
	meta := parseExecRunMeta(t, result.ForLLM)
	require.Contains(t, meta["exitNote"], "no matches")

	result = tool.Execute(ctx, map[string]any{
		"action": "run", "command": "diff a.txt b.txt",
	})
	meta = parseExecRunMeta(t, result.ForLLM)
	require.Contains(t, meta["exitNote"], "files differ")
}
