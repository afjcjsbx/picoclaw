package tools

import (
	"os"
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
