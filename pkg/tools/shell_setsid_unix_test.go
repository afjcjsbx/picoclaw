//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const execSetsidHelperTestName = "TestExecSetsidHelperProcess"

func TestExecSetsidHelperProcess(t *testing.T) {
	if os.Getenv("PICOCLAW_EXEC_SETSID_HELPER") != "1" {
		t.Skip("helper process only")
	}

	if _, err := syscall.Setsid(); err != nil {
		fmt.Fprintf(os.Stderr, "setsid: %v\n", err)
		os.Exit(2)
	}

	pidPath := os.Getenv("PICOCLAW_EXEC_SETSID_PIDFILE")
	if pidPath == "" {
		os.Exit(3)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write pid file: %v\n", err)
		os.Exit(4)
	}

	time.Sleep(60 * time.Second)
	os.Exit(0)
}

func setsidHelperCommand(t *testing.T) (command, pidFile string) {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	pidFile = filepath.Join(t.TempDir(), "setsid.pid")
	t.Setenv("PICOCLAW_EXEC_SETSID_HELPER", "1")
	t.Setenv("PICOCLAW_EXEC_SETSID_PIDFILE", pidFile)

	command = fmt.Sprintf("%q -test.run=^%s$ & wait", executable, execSetsidHelperTestName)
	return command, pidFile
}

func waitForSetsidPID(t *testing.T, pidFile string) int {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(strings.TrimSpace(string(data))) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("invalid pid file %q: %v", string(data), err)
			}
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pid file %s was never written", pidFile)
	return 0
}

func setsidProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func waitForSetsidProcessExit(t *testing.T, pid int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !setsidProcessAlive(pid) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("setsid child %d is still running after the process tree kill", pid)
}

func TestShellTool_TimeoutKillsSetsidChild(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %v", err)
	}
	tool.SetTimeout(2 * time.Second)

	command, pidFile := setsidHelperCommand(t)
	result := tool.Execute(WithToolContext(context.Background(), "cli", "test"), map[string]any{
		"action":  "run",
		"command": command,
	})
	if !result.IsError {
		t.Fatalf("expected timeout error, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "timed out") {
		t.Fatalf("expected timeout message, got: %s", result.ForLLM)
	}

	pid := waitForSetsidPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitForSetsidProcessExit(t, pid)
}

func TestShellTool_BackgroundKillKillsSetsidChild(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %v", err)
	}

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm
	ctx := WithToolContext(context.Background(), "cli", "test")

	command, pidFile := setsidHelperCommand(t)
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

	pid := waitForSetsidPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": response.SessionID,
	})
	if killResult.IsError {
		t.Fatalf("kill failed: %s", killResult.ForLLM)
	}

	waitForSetsidProcessExit(t, pid)
}
