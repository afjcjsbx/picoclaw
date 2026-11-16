//go:build linux

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestShellTool_ForegroundExitKillsDetachedSetsidChild(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %v", err)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "detached.pid")
	t.Setenv("PICOCLAW_EXEC_SETSID_HELPER", "1")
	t.Setenv("PICOCLAW_EXEC_SETSID_PIDFILE", pidFile)

	// The intermediate shell waits until the detached child has written its
	// PID, then exits. This is the normal-exit counterpart of the timeout PoC:
	// the tool succeeds, but the detached child must not survive.
	command := fmt.Sprintf(
		"%q -test.run=^%s$ & while [ ! -s %q ]; do sleep 0.01; done; exit 0",
		executable, execSetsidHelperTestName, pidFile,
	)
	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": command,
	})
	if result.IsError {
		t.Fatalf("foreground run failed: %s", result.ForLLM)
	}

	pid := waitForSetsidPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitForSetsidProcessExit(t, pid)
}
