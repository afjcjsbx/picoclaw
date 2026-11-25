package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

func TestRecoverGoroutine(t *testing.T) {
	ran := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverGoroutine("test", func(any) { ran = true })
		panic("boom")
	}()
	<-done
	require.True(t, ran)

	// No panic: the hook must not run.
	ran = false
	func() {
		defer recoverGoroutine("test", func(any) { ran = true })
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

func TestWrapCommandForCwdUnwritableProbeIsSilent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell wrapper")
	}
	probe := filepath.Join(t.TempDir(), "missing", "probe.txt")
	wrapped, ok := wrapCommandForCwd("echo hi; exit 3", probe)
	require.True(t, ok)

	out, err := exec.Command("sh", "-c", wrapped).CombinedOutput()
	require.Equal(t, "hi\n", string(out))
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 3, exitErr.ExitCode())
}

func TestCwdProbeDirFollowsIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Cleanup(func() { isolation.Configure(nil) })

	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = false
	isolation.Configure(cfg)
	require.Empty(t, cwdProbeDir())

	cfg.Isolation.Enabled = true
	isolation.Configure(cfg)
	require.Equal(t, filepath.Join(home, "runtime-user-env", "tmp"), cwdProbeDir())
}

func TestInformativeExitNote(t *testing.T) {
	note := func(command string, code int) string {
		return informativeExitNote(command, shellguard.Analyze(command), code)
	}
	require.Contains(t, note("grep missing file", 1), "no matches")
	require.Contains(t, note("diff a b", 1), "files differ")
	require.Contains(t, note("false", 1), "")
	require.Empty(t, note("grep missing file", 0))
	require.Empty(t, note("grep missing file", 2))
	require.Contains(t, note("sleep 1", 137), "SIGKILL")
	require.Contains(t, note("sleep 1", 139), "SIGSEGV")
	require.Contains(t, note("sleep 1", 143), "SIGTERM")
	require.Contains(t, note("grep missing file", 137), "SIGKILL")
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
