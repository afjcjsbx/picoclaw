//go:build linux

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestShellTool_BackgroundNoWaitKeepsScopeAlive(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %v", err)
	}
	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm
	ctx := WithToolContext(context.Background(), "cli", "test")

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	pidFile := filepath.Join(t.TempDir(), "detached.pid")
	t.Setenv("PICOCLAW_EXEC_SETSID_HELPER", "1")
	t.Setenv("PICOCLAW_EXEC_SETSID_PIDFILE", pidFile)

	// The shell exits immediately; only the detached setsid child remains.
	command := fmt.Sprintf("%q -test.run=^%s$ &", executable, execSetsidHelperTestName)
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    command,
		"background": "true",
	})
	if result.IsError {
		t.Fatalf("background run failed: %s", result.ForLLM)
	}
	var response ExecResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &response); err != nil {
		t.Fatalf("decode background response: %v", err)
	}

	session, err := sm.Get(response.SessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	pid := waitForSetsidPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	// The shell is gone but the scope is not empty, so the session must remain
	// observable and killable.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && session.IsDone() {
		time.Sleep(10 * time.Millisecond)
	}
	if session.IsDone() {
		t.Fatal("session was marked done while a detached scope member was still alive")
	}

	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": response.SessionID,
	})
	if killResult.IsError {
		t.Fatalf("kill failed: %s", killResult.ForLLM)
	}
	waitForSetsidProcessExit(t, pid)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !session.IsDone() {
		time.Sleep(10 * time.Millisecond)
	}
	if !session.IsDone() {
		t.Fatal("session did not become done after killing the scope")
	}
}
