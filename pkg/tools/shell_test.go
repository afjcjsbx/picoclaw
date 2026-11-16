package tools

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func parseExecRunMeta(t *testing.T, forLLM string) map[string]any {
	t.Helper()
	line, _, ok := strings.Cut(forLLM, "\n")
	require.True(t, ok, "expected a metadata header and an output body")
	require.True(t, strings.HasPrefix(line, execRunMetaPrefix), "unexpected exec header: %q", line)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, execRunMetaPrefix)), &meta))
	return meta
}

// TestShellTool_Success verifies successful command execution
func TestFindShellExecutableReturnsAbsoluteExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell lookup is not used on Windows")
	}
	path, err := findShellExecutable()
	if err != nil {
		t.Fatalf("findShellExecutable() error = %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("shell path %q is not absolute", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat shell path: %v", err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("shell path %q is not an executable file", path)
	}
}

func TestShellTool_Success(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "echo 'hello world'",
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// ForUser should contain command output
	if !strings.Contains(result.ForUser, "hello world") {
		t.Errorf("Expected ForUser to contain 'hello world', got: %s", result.ForUser)
	}

	// ForLLM should contain full output
	if !strings.Contains(result.ForLLM, "hello world") {
		t.Errorf("Expected ForLLM to contain 'hello world', got: %s", result.ForLLM)
	}
}

func TestShellTool_ForegroundResultHeader(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{
			"action":  "run",
			"command": "echo 'hello world'",
		})
		require.False(t, result.IsError, result.ForLLM)

		meta := parseExecRunMeta(t, result.ForLLM)
		require.Equal(t, float64(0), meta["exitCode"])
		require.Equal(t, false, meta["timedOut"])
		require.Equal(t, false, meta["truncated"])
		require.Contains(t, meta, "durationMs")

		// The body stays raw: no JSON escaping of newlines.
		_, body, ok := strings.Cut(result.ForLLM, "\n")
		require.True(t, ok)
		require.Contains(t, body, "hello world")
		require.NotContains(t, body, `\n`)
		require.Contains(t, result.ForUser, "hello world")
	})

	t.Run("non-zero exit", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{
			"action":  "run",
			"command": "sh -c 'exit 7'",
		})
		require.True(t, result.IsError)

		meta := parseExecRunMeta(t, result.ForLLM)
		require.Equal(t, float64(7), meta["exitCode"])
		require.NotContains(t, meta, "error")
		require.Contains(t, result.ForUser, "[Command exited with code 7]")
	})

	t.Run("empty output", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{
			"action":  "run",
			"command": "true",
		})
		require.False(t, result.IsError, result.ForLLM)

		meta := parseExecRunMeta(t, result.ForLLM)
		require.Equal(t, float64(0), meta["exitCode"])
		require.Contains(t, result.ForLLM, "(no output)")
		require.Equal(t, "(no output)", result.ForUser)
	})
}

// TestShellTool_Failure verifies failed command execution
func TestShellTool_Failure(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "ls /nonexistent_directory_12345",
	}

	result := tool.Execute(ctx, args)

	// Failure should be marked as error
	if !result.IsError {
		t.Errorf("Expected error for failed command, got IsError=false")
	}

	// ForUser should contain error information
	if result.ForUser == "" {
		t.Errorf("Expected ForUser to contain error info, got empty string")
	}

	// ForLLM carries the exit status in the metadata header.
	meta := parseExecRunMeta(t, result.ForLLM)
	if code, ok := meta["exitCode"].(float64); !ok || code == 0 {
		t.Errorf("Expected a non-zero exitCode in metadata, got: %v", meta["exitCode"])
	}
}

// TestShellTool_Timeout verifies command timeout handling
func TestShellTool_Timeout(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	tool.SetTimeout(100 * time.Millisecond)

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "sleep 10",
	}

	result := tool.Execute(ctx, args)

	// Timeout should be marked as error
	if !result.IsError {
		t.Errorf("Expected error for timeout, got IsError=false")
	}

	meta := parseExecRunMeta(t, result.ForLLM)
	require.Equal(t, true, meta["timedOut"])
	require.Nil(t, meta["exitCode"])
	require.Contains(t, meta["error"], "timed out")

	// Should mention timeout
	if !strings.Contains(result.ForUser, "timed out") {
		t.Errorf("Expected timeout message, got ForUser: %s", result.ForUser)
	}

	// A timeout is the point where the tool knows the command outlived a
	// foreground call; point the model at tracked background sessions.
	require.Contains(t, result.ForLLM, timeoutBackgroundGuidance)
	require.Contains(t, result.ForLLM, "background=true")
}

// TestShellTool_TimeoutArg_Invalid verifies malformed per-run timeouts are rejected before running
func TestShellTool_TimeoutArg_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		timeout any
	}{
		{"fractional", 1.5},
		{"negative", float64(-1)},
		{"overflow", 1e19},
		{"nan", math.NaN()},
		{"infinity", math.Inf(1)},
		{"string", "30"},
		{"bool", true},
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(context.Background(), map[string]any{
				"action":  "run",
				"command": "echo should-not-run",
				"timeout": tt.timeout,
			})

			require.True(t, result.IsError)
			require.Contains(t, result.ForLLM, "timeout must be a non-negative integer number of seconds")
			require.NotContains(t, result.ForLLM, "should-not-run")
		})
	}
}

// TestShellTool_TimeoutArg_OverridesConfigured verifies a per-run timeout replaces the configured one
func TestShellTool_TimeoutArg_OverridesConfigured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep with fractional seconds is not portable to PowerShell")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)
	tool.SetTimeout(100 * time.Millisecond)

	t.Run("longer timeout lets command finish", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{
			"action":  "run",
			"command": "sleep 0.3; echo done",
			"timeout": float64(5),
		})

		require.False(t, result.IsError, result.ForLLM)
		require.Contains(t, result.ForLLM, "done")
	})

	t.Run("zero promotes to background", func(t *testing.T) {
		result := tool.Execute(context.Background(), map[string]any{
			"action":  "run",
			"command": "sleep 0.3; echo done",
			"timeout": float64(0),
		})

		require.False(t, result.IsError, result.ForLLM)
		var resp ExecResponse
		require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
		require.NotEmpty(t, resp.SessionID)
		require.Contains(t, resp.Note, "foreground cap")
	})

	t.Run("shorter timeout kills command", func(t *testing.T) {
		tool, err := NewExecTool("", false)
		require.NoError(t, err)

		start := time.Now()
		result := tool.Execute(context.Background(), map[string]any{
			"action":  "run",
			"command": "sleep 10",
			"timeout": float64(1),
		})

		require.True(t, result.IsError)
		require.Contains(t, result.ForLLM, "timed out after 1s")
		require.Less(t, time.Since(start), 5*time.Second)
	})
}

// TestShellTool_DefaultTimeout verifies the configured default and the
// fallback used when no config is available.
func TestShellTool_DefaultTimeout(t *testing.T) {
	want := time.Duration(config.DefaultExecTimeoutSeconds) * time.Second

	tool, err := NewExecTool("", false)
	require.NoError(t, err)
	require.Equal(t, want, tool.timeout)

	cfg := &config.Config{}
	tool, err = NewExecToolWithConfig("", false, cfg)
	require.NoError(t, err)
	require.Equal(t, want, tool.timeout)

	cfg.Tools.Exec.TimeoutSeconds = 42
	tool, err = NewExecToolWithConfig("", false, cfg)
	require.NoError(t, err)
	require.Equal(t, 42*time.Second, tool.timeout)
}

// TestShellTool_TimeoutAboveForegroundCapPromotes verifies that a timeout
// beyond the foreground cap starts a tracked background session instead of
// blocking or failing the call.
func TestShellTool_TimeoutAboveForegroundCapPromotes(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo promoted",
		"timeout": float64(config.MaxExecForegroundTimeoutSeconds + 1),
	})
	require.False(t, result.IsError, result.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)
	require.Contains(t, started.Note, "foreground cap")

	waited := execResp(t, tool.Execute(ctx, map[string]any{
		"action":    "wait",
		"sessionId": started.SessionID,
		"timeout":   float64(5),
	}))
	require.Equal(t, "done", waited.Status)
	require.Contains(t, waited.Output, "promoted")
}

// TestShellTool_TimeoutAtForegroundCapStaysForeground verifies the cap is
// inclusive: a request equal to the cap is still a foreground run.
func TestShellTool_TimeoutAtForegroundCapStaysForeground(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "echo foreground",
		"timeout": float64(config.MaxExecForegroundTimeoutSeconds),
	})
	require.False(t, result.IsError, result.ForLLM)
	require.True(t, strings.HasPrefix(result.ForLLM, execRunMetaPrefix), result.ForLLM)
	require.Contains(t, result.ForLLM, "foreground")
}

// TestShellTool_DisabledForegroundCapKeepsRunForeground verifies the internal
// escape hatch used by cron, which consumes command output synchronously.
func TestShellTool_DisabledForegroundCapKeepsRunForeground(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)
	tool.foregroundCap = 0

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "echo synchronous",
		"timeout": float64(config.MaxExecForegroundTimeoutSeconds + 1),
	})
	require.False(t, result.IsError, result.ForLLM)
	require.True(t, strings.HasPrefix(result.ForLLM, execRunMetaPrefix), result.ForLLM)
	require.Contains(t, result.ForLLM, "synchronous")
}

// TestShellTool_ConfiguredTimeoutAboveCapPromotes verifies that a configured
// timeout beyond the cap also yields a tracked background session.
func TestShellTool_ConfiguredTimeoutAboveCapPromotes(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.TimeoutSeconds = config.MaxExecForegroundTimeoutSeconds + 1

	tool, err := NewExecToolWithConfig("", false, cfg)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo promoted",
	})
	require.False(t, result.IsError, result.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)
	require.Contains(t, started.Note, "foreground cap")
}

// TestShellTool_TimeoutArg_Background verifies timeout only conflicts with background when non-zero
func TestShellTool_TimeoutArg_Background(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	t.Run("non-zero timeout is rejected", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"action":     "run",
			"command":    "echo hello",
			"background": true,
			"timeout":    float64(5),
		})

		require.True(t, result.IsError)
		require.Contains(t, result.ForLLM, "timeout is only supported for foreground commands")
	})

	t.Run("invalid timeout reports the parsing error", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"action":     "run",
			"command":    "echo hello",
			"background": true,
			"timeout":    float64(-1),
		})

		require.True(t, result.IsError)
		require.Contains(t, result.ForLLM, "timeout must be a non-negative integer number of seconds")
	})

	t.Run("zero timeout starts the session", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{
			"action":     "run",
			"command":    "sleep 10",
			"background": true,
			"timeout":    float64(0),
		})

		require.False(t, result.IsError, result.ForLLM)
		require.Contains(t, result.ForLLM, "sessionId")

		var resp ExecResponse
		require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
		tool.Execute(ctx, map[string]any{"action": "kill", "sessionId": resp.SessionID})
	})
}

// TestShellTool_WorkingDir verifies custom working directory
func TestShellTool_WorkingDir(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0o644)

	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "cat test.txt",
		"cwd":     tmpDir,
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success in custom working dir, got error: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForUser, "test content") {
		t.Errorf("Expected output from custom dir, got: %s", result.ForUser)
	}
}

// TestShellTool_DangerousCommand verifies safety guard blocks dangerous commands
func TestShellTool_DangerousCommand(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "rm -rf /",
	}

	result := tool.Execute(ctx, args)

	// Dangerous command should be blocked
	if !result.IsError {
		t.Errorf("Expected dangerous command to be blocked (IsError=true)")
	}

	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf("Expected 'blocked' message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

func TestShellTool_DangerousCommand_KillBlocked(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "kill 12345",
	}

	result := tool.Execute(ctx, args)
	if !result.IsError {
		t.Errorf("Expected kill command to be blocked")
	}
	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf("Expected blocked message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

// TestShellTool_MissingCommand verifies error handling for missing command
func TestShellTool_MissingCommand(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when command is missing")
	}
}

// TestShellTool_StderrCapture verifies stderr is captured and included
func TestShellTool_StderrCapture(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "sh -c 'echo stdout; echo stderr >&2'",
	}

	result := tool.Execute(ctx, args)

	// Both stdout and stderr should be in output
	if !strings.Contains(result.ForLLM, "stdout") {
		t.Errorf("Expected stdout in output, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "stderr") {
		t.Errorf("Expected stderr in output, got: %s", result.ForLLM)
	}
}

// TestShellTool_ForegroundMergesStdoutStderrInOrder verifies stdout and stderr
// share one stream, so their relative write order is preserved.
func TestShellTool_ForegroundMergesStdoutStderrInOrder(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "sh -c 'echo one; echo two >&2; echo three'",
	})
	require.False(t, result.IsError, result.ForLLM)
	require.Equal(t, []string{"one", "two", "three"}, strings.Split(strings.TrimSpace(result.ForUser), "\n"))
	meta := parseExecRunMeta(t, result.ForLLM)
	require.Equal(t, float64(0), meta["exitCode"])
}

// TestShellTool_BackgroundMergesStderrWithoutBlocking reproduces the old
// deadlock: stderr was read only after stdout reached EOF, so a child that
// filled the stderr pipe blocked forever while no stdout was produced.
func TestShellTool_BackgroundMergesStderrWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX yes/head")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	runResult := tool.Execute(context.Background(), map[string]any{
		"action":     "run",
		"command":    "yes errline | head -20000 >&2; sleep 0.2; echo done",
		"background": true,
	})
	require.False(t, runResult.IsError, runResult.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(runResult.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)

	deadline := time.Now().Add(5 * time.Second)
	for {
		pollResult := tool.Execute(context.Background(), map[string]any{
			"action":    "poll",
			"sessionId": started.SessionID,
		})
		require.False(t, pollResult.IsError, pollResult.ForLLM)

		var pollResp ExecResponse
		require.NoError(t, json.Unmarshal([]byte(pollResult.ForLLM), &pollResp))
		if pollResp.Status == "done" {
			require.NotNil(t, pollResp.ExitCode)
			require.Equal(t, 0, *pollResp.ExitCode)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background process did not finish; stderr pipe likely blocked")
		}
		time.Sleep(50 * time.Millisecond)
	}

	readResult := tool.Execute(context.Background(), map[string]any{
		"action":    "read",
		"sessionId": started.SessionID,
	})
	require.False(t, readResult.IsError, readResult.ForLLM)

	var readResp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(readResult.ForLLM), &readResp))
	require.Contains(t, readResp.Output, "errline")
	require.Contains(t, readResp.Output, "done")
	require.NotNil(t, readResp.ExitCode)
	require.Equal(t, 0, *readResp.ExitCode)
}

// TestShellTool_OutputTruncation verifies foreground output is bounded by the
// configured cap and keeps a head+tail window around the omission notice.
func TestShellTool_OutputTruncation(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	// 20000 chars is under the default 50,000-char cap: the output must stay intact.
	args := map[string]any{
		"action":  "run",
		"command": "python3 -c \"print('x' * 20000)\" || echo " + strings.Repeat("x", 20000),
	}

	result := tool.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("Execute() failed: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "[OUTPUT TRUNCATED") {
		t.Fatalf("output under the default cap should not be truncated")
	}
	if len(result.ForLLM) < 20000 {
		t.Fatalf("expected full output, got length: %d", len(result.ForLLM))
	}

	// With a small configured cap, the first 40% and the last 60% are kept.
	cfg := &config.Config{}
	cfg.Tools.Exec.AllowRemote = true
	cfg.Tools.Exec.MaxOutputChars = 1000
	cappedTool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	capped := cappedTool.Execute(ctx, args)
	if !strings.Contains(capped.ForLLM, "[OUTPUT TRUNCATED") {
		t.Fatalf("expected truncation notice, got length: %d", len(capped.ForLLM))
	}
	if !strings.HasPrefix(capped.ForUser, strings.Repeat("x", 400)) {
		t.Fatal("expected the first 40% of the output to be kept")
	}
	if !strings.HasSuffix(capped.ForUser, strings.Repeat("x", 599)+"\n") {
		t.Fatal("expected the last 60% of the output to be kept")
	}
	meta := parseExecRunMeta(t, capped.ForLLM)
	require.Equal(t, true, meta["truncated"])
}

// TestShellTool_TruncatedOutputSpillsFullText verifies that foreground output
// beyond the configured cap stays recoverable and that the inline view honors
// the global max_inline_result_chars cap: the artifact keeps the full stream,
// so the omitted middle remains readable without expanding model context.
func TestShellTool_TruncatedOutputSpillsFullText(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.AllowRemote = true
	cfg.Tools.Exec.MaxOutputChars = 1000
	cfg.Tools.MaxInlineResultChars = 512

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	workspace := t.TempDir()
	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(512)
	reg.Register(tool)

	full := strings.Repeat("A", 900) + strings.Repeat("M", 20000) + strings.Repeat("Z", 900)
	result := reg.Execute(context.Background(), "exec", map[string]any{
		"action":  "run",
		"command": "python3 -c \"print('A' * 900 + 'M' * 20000 + 'Z' * 900)\" || echo " + full,
	})
	if result.IsError {
		t.Fatalf("Execute() failed: %s", result.ForLLM)
	}

	if len(result.ArtifactTags) != 1 {
		t.Fatalf("expected the full output spilled as one artifact, got %+v", result.ArtifactTags)
	}
	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected artifact readable: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != full {
		t.Fatalf("expected the artifact to hold the full output, got %d chars (want %d)", len(got), len(full))
	}
	// Inline stays bounded and keeps the real head and tail of the full output.
	if got := len([]rune(result.ForLLM)); got > cfg.Tools.MaxInlineResultChars+200 {
		t.Fatalf("inline output has %d runes, want at most %d plus the truncation notice",
			got, cfg.Tools.MaxInlineResultChars)
	}
	if !strings.Contains(result.ForLLM, strings.Repeat("A", 100)) {
		t.Fatal("expected the real output head in the preview")
	}
	if !strings.Contains(result.ForLLM, strings.Repeat("Z", 100)) {
		t.Fatal("expected the real output tail in the preview")
	}
}

func TestShellTool_HeaderDoesNotTriggerGenericSpill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX yes/head")
	}

	workspace := t.TempDir()
	cfg := &config.Config{}
	cfg.Tools.Exec.AllowRemote = true
	cfg.Tools.Exec.MaxOutputChars = 4096
	cfg.Tools.MaxInlineResultChars = 1024

	tool, err := NewExecToolWithConfig(workspace, false, cfg)
	require.NoError(t, err)

	reg := NewToolRegistry()
	reg.SetWorkspace(workspace)
	reg.SetMaxInlineResultRunes(1024)
	reg.Register(tool)

	// The body fits the inline cap, but the metadata header pushes the total
	// over it. The result is already bounded, so the generic spill must not
	// replace the header with a large-result preview.
	result := reg.Execute(context.Background(), "exec", map[string]any{
		"action":  "run",
		"command": "yes x | head -c 1000",
	})
	require.False(t, result.IsError, result.ForLLM)
	require.True(t, strings.HasPrefix(result.ForLLM, execRunMetaPrefix), result.ForLLM)
	require.True(t, result.LargeResultSpilled)
	require.Empty(t, result.ArtifactTags)
	require.Contains(t, result.ForLLM, "x\nx")
}

// TestShellTool_ForegroundSpillUsesWorkspaceArtifact verifies that an oversized
// foreground stream is spilled into the workspace artifact directory instead of
// being retained in memory.
func TestShellTool_ForegroundSpillUsesWorkspaceArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX yes/head")
	}

	workspace := t.TempDir()
	cfg := &config.Config{}
	cfg.Tools.Exec.AllowRemote = true
	cfg.Tools.Exec.MaxOutputChars = 100

	tool, err := NewExecToolWithConfig(workspace, false, cfg)
	require.NoError(t, err)

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "yes x | head -1000",
	})
	require.False(t, result.IsError, result.ForLLM)
	require.True(t, result.LargeResultSpilled)
	require.Len(t, result.ArtifactTags, 1)

	path := strings.TrimSuffix(strings.TrimPrefix(result.ArtifactTags[0], "[file:"), "]")
	require.Contains(t, path, filepath.Join(workspace, ".artifacts", "tool"))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(data), cfg.Tools.Exec.MaxOutputChars)
}

// TestShellTool_WorkingDir_OutsideWorkspace verifies that working_dir cannot escape the workspace directly
func TestShellTool_WorkingDir_OutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}

	tool, err := NewExecTool(workspace, true)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "pwd",
		"cwd":     outsideDir,
	})

	if !result.IsError {
		t.Fatalf("expected working_dir outside workspace to be blocked, got output: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "blocked") {
		t.Errorf("expected 'blocked' in error, got: %s", result.ForLLM)
	}
}

// TestShellTool_WorkingDir_SymlinkEscape verifies that a symlink inside the workspace
// pointing outside cannot be used as working_dir to escape the sandbox.
func TestShellTool_WorkingDir_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	secretDir := filepath.Join(root, "secret")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatalf("failed to create secret dir: %v", err)
	}
	os.WriteFile(filepath.Join(secretDir, "secret.txt"), []byte("top secret"), 0o644)

	// symlink lives inside the workspace but resolves to secretDir outside it
	link := filepath.Join(workspace, "escape")
	if err := os.Symlink(secretDir, link); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	tool, err := NewExecTool(workspace, true)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "cat secret.txt",
		"cwd":     link,
	})

	if !result.IsError {
		t.Fatalf("expected symlink working_dir escape to be blocked, got output: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "blocked") {
		t.Errorf("expected 'blocked' in error, got: %s", result.ForLLM)
	}
}

// TestShellTool_RemoteChannelBlockedByDefault verifies exec is blocked for remote channels
func TestShellTool_RemoteChannelBlockedByDefault(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.AllowRemote = false

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "echo hi"})

	if !result.IsError {
		t.Fatal("expected remote-channel exec to be blocked")
	}
	if !strings.Contains(result.ForLLM, "restricted to internal channels") {
		t.Errorf("expected 'restricted to internal channels' message, got: %s", result.ForLLM)
	}
}

// TestShellTool_InternalChannelAllowed verifies exec is allowed for internal channels
func TestShellTool_InternalChannelAllowed(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.AllowRemote = false

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}
	ctx := WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "echo hi"})

	if result.IsError {
		t.Fatalf("expected internal channel exec to succeed, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "hi") {
		t.Errorf("expected output to contain 'hi', got: %s", result.ForLLM)
	}
}

// TestShellTool_EmptyChannelBlockedWhenNotAllowRemote verifies fail-closed when no channel context
func TestShellTool_EmptyChannelBlockedWhenNotAllowRemote(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.AllowRemote = false

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}
	result := tool.Execute(context.Background(), map[string]any{
		"command": "echo hi",
	})

	if !result.IsError {
		t.Fatal("expected exec with empty channel to be blocked when allowRemote=false")
	}
}

// TestShellTool_AllowRemoteBypassesChannelCheck verifies allowRemote=true permits any channel
func TestShellTool_AllowRemoteBypassesChannelCheck(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.AllowRemote = true

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "echo hi"})

	if result.IsError {
		t.Fatalf("expected allowRemote=true to permit remote channel, got: %s", result.ForLLM)
	}
}

// TestShellTool_RestrictToWorkspace verifies workspace restriction
func TestShellTool_RestrictToWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, false)
	if err != nil {
		t.Errorf("unable to configure exec tool: %s", err)
	}

	tool.SetRestrictToWorkspace(true)

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "cat ../../etc/passwd",
	}

	result := tool.Execute(ctx, args)

	// Path traversal should be blocked
	if !result.IsError {
		t.Errorf("Expected path traversal to be blocked with restrictToWorkspace=true")
	}

	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf(
			"Expected 'blocked' message for path traversal, got ForLLM: %s, ForUser: %s",
			result.ForLLM,
			result.ForUser,
		)
	}
}

// TestShellTool_RelativePathWithSlashAllowed verifies that local relative paths
// under the workspace are not mistaken for absolute paths by the safety guard.
func TestShellTool_RelativePathWithSlashAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	scriptDir := filepath.Join(tmpDir, "skills", "calendar-query", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("failed to create script dir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "query_calendar.py")
	if err := os.WriteFile(scriptPath, []byte("calendar ok\n"), 0o644); err != nil {
		t.Fatalf("failed to create script: %v", err)
	}

	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "cat skills/calendar-query/scripts/query_calendar.py",
	})

	if result.IsError {
		t.Fatalf("relative workspace script path should be allowed, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "calendar ok") {
		t.Fatalf("expected script output, got: %s", result.ForLLM)
	}
}

// TestShellTool_SymlinkedWorkspaceRelativePathAllowed verifies that a
// workspace reached through a symlink does not make ordinary workspace-relative
// paths look like escapes. Candidate paths are resolved with EvalSymlinks, so
// the workspace root must be resolved before the containment check.
func TestShellTool_SymlinkedWorkspaceRelativePathAllowed(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	target := filepath.Join(workspace, "pkg", "textutil", "parse.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}
	if err := os.WriteFile(target, []byte("package textutil\n"), 0o644); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	linkedWorkspace := filepath.Join(root, "linked-workspace")
	if err := os.Symlink(workspace, linkedWorkspace); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	tool, err := NewExecTool(linkedWorkspace, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "cat pkg/textutil/parse.go",
	})

	if result.IsError {
		t.Fatalf("relative path under a symlinked workspace should be allowed, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "package textutil") {
		t.Fatalf("expected file output, got: %s", result.ForLLM)
	}
}

func TestShellTool_AttachedAbsolutePathsStillBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	commands := []string{
		"cat --file=/etc/passwd",
		"cc -I/etc main.c",
		"echo -isystem/usr/include",
	}

	for _, cmd := range commands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("attached absolute path should be blocked: %q\n  got: %s", cmd, result.ForLLM)
		}
	}
}

func TestShellTool_OptionValueRelativeSymlinkEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "passwd"), []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("failed to create outside file: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(workspace, "link")); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	tool, err := NewExecTool(workspace, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "echo --config=link/passwd",
	})

	if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
		t.Fatalf("option value symlink escape should be blocked, got: %s", result.ForLLM)
	}
}

// TestShellTool_NewFileThroughSymlinkedDirBlocked verifies that a redirection
// target that does not exist yet cannot escape the workspace through a
// symlinked directory. EvalSymlinks fails on a missing final component, so the
// candidate must be resolved against its nearest existing ancestor.
func TestShellTool_NewFileThroughSymlinkedDirBlocked(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(workspace, "link")); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	tool, err := NewExecTool(workspace, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "echo pwn > link/newfile",
	})

	if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
		t.Fatalf("new file through symlinked directory should be blocked, got: %s", result.ForLLM)
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "newfile")); !os.IsNotExist(statErr) {
		t.Fatalf("blocked command created a file outside the workspace")
	}
}

// TestShellTool_NewFileThroughWorkspaceSymlinkedDirAllowed verifies that the
// ancestor fallback does not reject a new file created through a symlink that
// still points inside the workspace.
func TestShellTool_NewFileThroughWorkspaceSymlinkedDirAllowed(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	realDir := filepath.Join(workspace, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}
	if err := os.Symlink(realDir, filepath.Join(workspace, "alias")); err != nil {
		t.Skipf("symlinks not supported in this environment: %v", err)
	}

	tool, err := NewExecTool(workspace, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "echo hello > alias/newfile",
	})

	if result.IsError {
		t.Fatalf("new file through workspace symlink should be allowed, got: %s", result.ForLLM)
	}
	data, err := os.ReadFile(filepath.Join(realDir, "newfile"))
	if err != nil {
		t.Fatalf("expected file to be created through symlink: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}

// TestShellTool_DevNullAllowed verifies that /dev/null redirections are not blocked (issue #964).
func TestShellTool_DevNullAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	commands := []string{
		"echo hello 2>/dev/null",
		"echo hello >/dev/null",
		"echo hello > /dev/null",
		"echo hello 2> /dev/null",
		"echo hello >/dev/null 2>&1",
		"find " + tmpDir + " -name '*.go' 2>/dev/null",
	}

	for _, cmd := range commands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if result.IsError && strings.Contains(result.ForLLM, "blocked") {
			t.Errorf("command should not be blocked: %s\n  error: %s", cmd, result.ForLLM)
		}
	}
}

// TestShellTool_DevNullAllowedWithSeparator verifies that /dev/null redirections
// are not blocked when immediately followed by a shell separator (issue #435).
// The absolute-path regex only stops at whitespace or quotes, so it used to
// swallow the separator and produce a bogus path such as "/dev/null;".
func TestShellTool_DevNullAllowedWithSeparator(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	commands := []string{
		"echo a 2>/dev/null; echo b",
		"echo a 2>/dev/null;echo b",
		"echo a 2>/dev/null|cat",
		"echo a 2>/dev/null&&echo b",
		"echo a 2>/dev/null||echo b",
		"echo a 2>/dev/null&echo b",
		"echo a >/dev/null;echo b",
		"echo a 2>/dev/null,",
		"echo a 2>/dev/null)",
		"echo a 2>/dev/null]",
		"echo a 2>/dev/null}",
		"echo a 2>/dev/null:",
		"cat /dev/urandom|head -c 16|od;echo done",
	}

	for _, cmd := range commands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if result.IsError && strings.Contains(result.ForLLM, "blocked") {
			t.Errorf("command should not be blocked: %s\n  error: %s", cmd, result.ForLLM)
		}
	}
}

// TestShellTool_SeparatorKeepsRealPathsBlocked verifies that shell syntax stuck
// to a path cannot be used to sneak an outside-workspace path past the
// containment check, whether the real path comes before or after the separator.
func TestShellTool_SeparatorKeepsRealPathsBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	commands := []string{
		"cat /etc/passwd;echo done",
		"cat /etc/passwd;echo done 2>/dev/null",
		"cat /etc/passwd&&echo done",
		"cat /etc/passwd|cat",
		"cat /etc/shadow)",
		"cat /etc/shadow,",
		// A safe pseudo-device must not act as an allowlist prefix: every
		// separator-delimited token is validated, so the path that follows is
		// still checked (regression test for the prefix-only trimming bug).
		"cat /dev/null;cat</etc/shadow",
		"cat /dev/null&&cat</etc/shadow",
		"cat /dev/null|cat</etc/shadow",
		"cat /dev/null;cat</etc/passwd",
		"echo hi>/dev/null;cat</etc/shadow",
		"cat /dev/null;echo;/etc/motd",
		"cat /dev/null>/tmp/out",
	}

	for _, cmd := range commands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("outside path should still be blocked: %s\n  result: %s", cmd, result.ForLLM)
		}
	}
}

// TestSplitShellPathMatch exercises the splitting helper directly.
func TestSplitShellPathMatch(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"/dev/null", []string{"/dev/null"}},
		{"/dev/null;", []string{"/dev/null"}},
		{"/dev/null;echo b", []string{"/dev/null", "echo b"}},
		{"/dev/null&&echo b", []string{"/dev/null", "echo b"}},
		{"/dev/null||echo b", []string{"/dev/null", "echo b"}},
		{"/dev/null|cat", []string{"/dev/null", "cat"}},
		{"/dev/null)", []string{"/dev/null"}},
		{"/dev/null]", []string{"/dev/null"}},
		{"/dev/null}", []string{"/dev/null"}},
		{"/dev/null,", []string{"/dev/null"}},
		{"/dev/null:", []string{"/dev/null"}},
		// Backticks are covered here only: a full command containing them is
		// rejected earlier by the dangerous-pattern check.
		{"/dev/null`", []string{"/dev/null"}},
		{"/dev/null`echo", []string{"/dev/null", "echo"}},
		{"/etc/passwd;echo", []string{"/etc/passwd", "echo"}},
		{"/etc/passwd", []string{"/etc/passwd"}},
		// Every segment after a separator is kept so it can be validated.
		{"/dev/null;cat</etc/shadow", []string{"/dev/null", "cat", "/etc/shadow"}},
		// Known trade-off: trailing punctuation is stripped even from real names.
		{"/workspace/backup,", []string{"/workspace/backup"}},
		// Windows matches are intentionally left untouched.
		{`C:\dev\null;x`, []string{`C:\dev\null;x`}},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, splitShellPathMatch(tc.in), "splitShellPathMatch(%q)", tc.in)
	}
}

// TestShellTool_BlockDevices verifies that writes to block devices are blocked (issue #965).
func TestShellTool_BlockDevices(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	blocked := []string{
		"echo x > /dev/sda",
		"echo x > /dev/hda",
		"echo x > /dev/vda",
		"echo x > /dev/xvda",
		"echo x > /dev/nvme0n1",
		"echo x > /dev/mmcblk0",
		"echo x > /dev/loop0",
		"echo x > /dev/dm-0",
		"echo x > /dev/md0",
		"echo x > /dev/sr0",
		"echo x > /dev/nbd0",
	}

	for _, cmd := range blocked {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if !result.IsError {
			t.Errorf("expected block device write to be blocked: %s", cmd)
		}
	}
}

// TestShellTool_SafePathsInWorkspaceRestriction verifies that safe kernel pseudo-devices
// are allowed even when workspace restriction is active.
func TestShellTool_SafePathsInWorkspaceRestriction(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	// These reference paths outside workspace but should be allowed via safePaths.
	commands := []string{
		"cat /dev/urandom | head -c 16 | od",
		"echo test > /dev/null",
		"dd if=/dev/zero bs=1 count=1",
	}

	for _, cmd := range commands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if result.IsError && strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("safe path should not be blocked by workspace check: %s\n  error: %s", cmd, result.ForLLM)
		}
	}
}

// TestShellTool_ExitCodeDetails verifies that exit codes are captured with details
func TestShellTool_ExitCodeDetails(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()
	args := map[string]any{
		"action":  "run",
		"command": "sh -c 'exit 42'",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("expected error for non-zero exit code")
	}

	meta := parseExecRunMeta(t, result.ForLLM)
	if got := meta["exitCode"]; got != float64(42) {
		t.Errorf("expected exitCode 42 in metadata, got: %v", got)
	}

	// The human-readable result keeps the detailed exit message.
	if !strings.Contains(result.ForUser, "exited with code 42") {
		t.Errorf("expected 'exited with code 42' in user message, got: %s", result.ForUser)
	}

	// Err field is set by the exec system (may or may not be set depending on implementation)
	// The important thing is that IsError=true
	t.Logf("Exit code result: %s", result.ForLLM)
}

// TestShellTool_TimeoutWithPartialOutput verifies timeout includes partial output
func TestShellTool_TimeoutWithPartialOutput(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	tool.SetTimeout(1 * time.Second) // Give more time for echo to complete

	ctx := context.Background()
	// Use a command that outputs immediately then sleeps
	args := map[string]any{
		"action":  "run",
		"command": "echo 'partial output before timeout' && sleep 30",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("expected error for timeout")
	}

	// Should mention timeout
	if !strings.Contains(result.ForLLM, "timed out") {
		t.Errorf("expected 'timed out' in message, got: %s", result.ForLLM)
	}

	// Log the result for debugging (partial output depends on shell behavior)
	t.Logf("Timeout result: %s", result.ForLLM)
}

// TestShellTool_CustomAllowPatterns verifies that custom allow patterns exempt
// commands from default deny pattern checks.
func TestShellTool_CustomAllowPatterns(t *testing.T) {
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns:  true,
				CustomAllowPatterns: []string{`\bgit\s+push\s+origin\b`},
			},
		},
	}

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := WithToolContext(context.Background(), "cli", "test")

	// "git push origin main" should be allowed by custom allow pattern.
	// It may fail for other reasons (no git repo), but the error should not be a guard block.
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "git push origin main",
	})
	if result.IsError && strings.Contains(result.ForLLM, "blocked") {
		t.Errorf("custom allow pattern should exempt 'git push origin main', got: %s", result.ForLLM)
	}

	// "git push upstream main" should still be blocked (does not match allow pattern).
	result = tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "git push upstream main",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "blocked") {
		t.Errorf("'git push upstream main' should still be blocked by deny pattern, got: %s", result.ForLLM)
	}

	// Without any custom allow pattern, "git push origin main" should be blocked.
	cfgNoAllow := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns: true,
			},
		},
	}
	toolNoAllow, err := NewExecToolWithConfig("", false, cfgNoAllow)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}
	result = toolNoAllow.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "git push origin main",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "blocked") {
		t.Errorf("'git push origin main' should be blocked without custom allow pattern, got: %s", result.ForLLM)
	}
}

// TestShellTool_URLsNotBlocked verifies that commands containing URLs are not
// incorrectly blocked by the workspace restriction safety guard (issue #1203).
func TestShellTool_URLsNotBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	// These commands contain URLs and should NOT be blocked by workspace restriction.
	// The URL path components (e.g., "//github.com") should be recognized as URLs,
	// not as file system paths.
	commands := []string{
		"agent-browser open https://github.com",
		"curl https://api.example.com/data",
		"wget http://example.com/file",
		"browser open https://github.com/user/repo",
		"fetch ftp://ftp.example.com/file.txt",
		"git clone https://github.com/sipeed/picoclaw.git",
	}

	for _, cmd := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		cancel()
		if result.IsError && strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("command with URL should not be blocked by workspace check: %s\n  error: %s", cmd, result.ForLLM)
		}
	}
}

// TestShellTool_FileURISandboxing verifies that file:// URIs that escape the
// workspace are still blocked, even though other URLs are allowed (issue #1254).
func TestShellTool_FileURISandboxing(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	// These file:// URIs should be blocked if they reference paths outside the workspace.
	// Unlike web URLs (http://, https://, ftp://), file:// URIs can be used to escape the sandbox.
	blockedCommands := []string{
		"cat file:///etc/passwd",
		"cat file:///etc/hosts",
		"cat file:///root/.ssh/id_rsa",
	}

	for _, cmd := range blockedCommands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("file:// URI outside workspace should be blocked: %s", cmd)
		}
	}

	// These file:// URIs should be allowed if they reference paths inside the workspace.
	// Create a test file inside the temp directory
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %s", err)
	}

	allowedCommands := []string{
		"cat file://" + testFile,
	}

	for _, cmd := range allowedCommands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if result.IsError && strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("file:// URI inside workspace should be allowed: %s\n  error: %s", cmd, result.ForLLM)
		}
	}
}

// TestShellTool_URLBypassPrevented verifies that a command cannot bypass the workspace
// sandbox by smuggling a real path after a URL that contains the same //path substring.
// e.g. "echo https://etc/passwd && cat //etc/passwd" must still be blocked.
func TestShellTool_URLBypassPrevented(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	// The path //etc/passwd appears twice: once as the host part of an https URL
	// and once as a real (escaped) absolute path. The guard must block the command
	// because the second occurrence is a genuine out-of-workspace path.
	blockedCommands := []string{
		"echo https://etc/passwd && cat //etc/passwd",
		"curl https://host/file && ls //etc",
	}

	for _, cmd := range blockedCommands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("bypass attempt should be blocked: %q\n  got: %s", cmd, result.ForLLM)
		}
	}
}

// TestWindows_TildeBypassPrevented verifies that ~ (home directory) cannot be
// used to escape workspace restrictions on Windows.
func TestWindows_TildeBypassPrevented(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()

	// Tilde should be blocked when it expands outside workspace
	blockedCommands := []string{
		"ls ~",
		"ls ~/some/path",
		"cat ~/.config/file",
		// PowerShell environment variables also expand to home directory
		"dir $env:USERPROFILE",
		"ls $env:USERPROFILE",
		"cat $env:USERPROFILE\\.config\\file",
		// CMD environment variables
		`cmd /c "dir %USERPROFILE%"`,
		`cmd /c "cd %USERPROFILE% && dir"`,
		`cmd /c "type %USERPROFILE%\\.config\\file"`,
	}

	for _, cmd := range blockedCommands {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		if !result.IsError || !strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("tilde bypass should be blocked: %q\n  got: %s", cmd, result.ForLLM)
		}
	}
}

// TestShellTool_PathTraversalVariants verifies that .../.../ and similar
// path traversal variants are blocked.
func TestShellTool_PathTraversalVariants(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()

	// Path traversal variants should be blocked
	blockedCommands := []string{
		"ls .../.../",
		"ls ..../..../",
		"cat .../.../../../etc/passwd",
	}

	for _, cmd := range blockedCommands {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		if !result.IsError || !strings.Contains(result.ForLLM, "path traversal") {
			t.Errorf("path traversal variant should be blocked: %q\n  got: %s", cmd, result.ForLLM)
		}
	}

	// Legitimate commands with ... should not be blocked (if such commands exist)
	// Note: these will fail for other reasons but should not be blocked by path traversal
	allowedCommands := []string{
		"echo ...",
		"ls ...",
	}

	for _, cmd := range allowedCommands {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		// These should not be blocked by path traversal check specifically
		if strings.Contains(result.ForLLM, "path traversal") {
			t.Errorf("legitimate command with ... should not be blocked: %q", cmd)
		}
	}
}

// TestWindows_SymlinkBypassPrevented verifies that symlinks pointing outside
// workspace are detected and blocked after resolution.
func TestWindows_SymlinkBypassPrevented(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := context.Background()

	// /tmp is outside the user workspace, should be blocked after symlink resolution
	// On Windows /tmp resolves to C:\tmp which may not exist, causing different error
	result := tool.Execute(ctx, map[string]any{"action": "run", "command": "ls /tmp"})
	if !result.IsError {
		t.Errorf("symlink bypass should be blocked: %s", result.ForLLM)
	}
}

// TestWindows_PowerShellEncodingBypass verifies that PowerShell encoding bypass techniques are blocked.
func TestWindows_PowerShellEncodingBypass(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	ctx := context.Background()

	// Commands using [Text.Encoding] to construct a command string at runtime.
	encodingBypassCommands := []string{
		// Basic byte array forms
		`[Text.Encoding]::ASCII.GetString([byte[]](0x6c,0x73,0x20,0x7e))`,
		`[Text.Encoding]::ASCII.GetString([byte[]](0x69,0x65,0x78))`,
		// System.Text.Encoding variant
		`[System.Text.Encoding]::ASCII.GetString([byte[]](0x69,0x65,0x78))`,
		// With whitespace variation
		`[System.Text.Encoding]::ASCII.GetString ([byte[]](0x69,0x65,0x78))`,
		// Variable storage form
		`$b = [byte[]](0x69,0x65,0x78); [Text.Encoding]::ASCII.GetString($b)`,
		// UTF8 variant
		`[Text.Encoding]::UTF8.GetString([byte[]](0x69,0x65,0x78))`,
		// Unicode variant
		`[Text.Encoding]::Unicode.GetString([byte[]](0x69,0x00,0x65,0x00,0x78,0x00))`,
	}

	for _, cmd := range encodingBypassCommands {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		if !result.IsError {
			t.Errorf("expected [Text.Encoding] bypass to be blocked: %s", cmd)
		}
		if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
			t.Errorf("expected 'blocked' message for %s, got: %s", cmd, result.ForLLM)
		}
	}

	// Commands using PowerShell's -EncodedCommand flag (base64), including short forms.
	encodedCommands := []string{
		// Full form
		`powershell -NoProfile -NonInteractive -EncodedCommand SQBFAHIAaABlAGwAbAAvAC8A`,
		`pwsh -EncodedCommand aWV4`,
		// Short forms: -e, -ec, -enc, -en
		`pwsh -e SQBFAHIAaABlAGwAbAAvAC8A`,
		`pwsh -ec aWV4`,
		`pwsh -enc aWV4`,
		`pwsh -en aWV4`,
		`powershell -e SQBFAHIAaABlAGwAbAAvAC8A`,
		`powershell -ec aWV4`,
		`powershell -enc aWV4`,
		`powershell -en aWV4`,
	}

	for _, cmd := range encodedCommands {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		if !result.IsError {
			t.Errorf("expected -EncodedCommand to be blocked: %s", cmd)
		}
	}

	// Unicode escape sequences that could construct malicious commands
	// Using double backslash to represent literal \u in Go string
	unicodeCommands := []string{
		`cmd /c "cd %USERPROFILE% \\u0026 dir"`,
		`powershell -Command "Write-Host \\u0049EX"`,
		`cmd /c "echo \\u0069\\u0065\\u0078"`,
	}

	for _, cmd := range unicodeCommands {
		result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
		if !result.IsError {
			t.Errorf("expected Unicode escape to be blocked: %s", cmd)
		}
	}
}

func TestShellTool_Background_ReturnsImmediately(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	ctx := context.Background()
	args := map[string]any{
		"action":     "run",
		"command":    "sleep 5",
		"background": "true",
	}

	start := time.Now()
	result := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	require.False(t, result.IsError, "background run should not error: %s", result.ForLLM)
	require.Less(t, elapsed, time.Second, "background run should return immediately")
	require.Contains(t, result.ForLLM, "sessionId")
}

// TestShellTool_BackgroundSessionReadableImmediately verifies that a background
// session is safe to read as soon as it is visible in the session manager: the
// output buffer must be initialized before the session is published, otherwise
// ProcessSession.Read would dereference a nil buffer.
func TestShellTool_BackgroundSessionReadableImmediately(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 10",
		"background": "true",
	})
	require.False(t, runResult.IsError, "run should succeed: %s", runResult.ForLLM)

	sessions := sm.List()
	require.NotEmpty(t, sessions, "background session should be registered")

	for _, info := range sessions {
		session, err := sm.Get(info.ID)
		require.NoError(t, err)
		require.NotNil(t, session.outputBuffer, "output buffer must exist for published sessions")
		require.NotPanics(t, func() { _ = session.Read() })
	}
}

func TestShellTool_List_Empty(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := context.Background()
	args := map[string]any{"action": "list"}

	result := tool.Execute(ctx, args)
	require.False(t, result.IsError)
	require.Contains(t, result.ForUser, "0 active sessions")
}

func TestShellTool_RunBackground_List(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 10",
		"background": "true",
	})
	require.False(t, runResult.IsError, "run should succeed: %s", runResult.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.SessionID)

	time.Sleep(100 * time.Millisecond)

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	require.False(t, listResult.IsError)

	var listResp ExecResponse
	err = json.Unmarshal([]byte(listResult.ForLLM), &listResp)
	require.NoError(t, err)
	require.Len(t, listResp.Sessions, 1)
	require.Equal(t, resp.SessionID, listResp.Sessions[0].ID)

	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
	require.False(t, killResult.IsError, "kill should succeed: %s", killResult.ForLLM)
}

func TestShellTool_Read_Output(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "echo hello",
		"background": "true",
	})
	require.False(t, runResult.IsError)

	var resp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &resp)
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": resp.SessionID,
	})

	if !readResult.IsError {
		var readResp ExecResponse
		err = json.Unmarshal([]byte(readResult.ForLLM), &readResp)
		require.NoError(t, err)
	}
}

func TestShellTool_Kill(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 100",
		"background": "true",
	})
	require.False(t, runResult.IsError)

	var resp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &resp)
	require.NoError(t, err)

	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
	require.False(t, killResult.IsError, "kill should succeed: %s", killResult.ForLLM)

	time.Sleep(100 * time.Millisecond)

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	var listResp ExecResponse
	err = json.Unmarshal([]byte(listResult.ForLLM), &listResp)
	require.NoError(t, err)
	require.Len(t, listResp.Sessions, 1)
	require.Equal(t, "done", listResp.Sessions[0].Status)
}

func TestShellTool_PTY_AllowedCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Test that PTY is allowed for non-interpreter commands
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "cat",
		"pty":        "true",
		"background": "true",
	})
	require.False(t, result.IsError, "PTY with cat should succeed: %s", result.ForLLM)
	require.Contains(t, result.ForLLM, "sessionId")

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.SessionID)

	// Clean up
	tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
}

func TestShellTool_PTY_WriteRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a PTY session with a command that waits for input
	// Using 'cat' which will wait for stdin
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "cat",
		"pty":        "true",
		"background": "true",
	})
	require.False(t, result.IsError, "PTY run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Write some input to cat
	writeResult := tool.Execute(ctx, map[string]any{
		"action":    "write",
		"sessionId": resp.SessionID,
		"data":      "hello\n",
	})
	require.False(t, writeResult.IsError, "write should succeed: %s", writeResult.ForLLM)

	// Give cat time to process and output
	time.Sleep(200 * time.Millisecond)

	// Read the output
	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": resp.SessionID,
	})

	require.False(t, readResult.IsError, "read should succeed: %s", readResult.ForLLM)

	var readResp ExecResponse
	err = json.Unmarshal([]byte(readResult.ForLLM), &readResp)
	require.NoError(t, err)
	// PTY output should contain "hello"
	require.Contains(t, readResp.Output, "hello")

	// Clean up
	tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
}

func TestShellTool_PTY_Poll(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a PTY session with a long-running command
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 2",
		"pty":        "true",
		"background": "true",
	})
	require.False(t, result.IsError, "PTY run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Poll should show running
	pollResult := tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError, "poll should succeed: %s", pollResult.ForLLM)

	var pollResp ExecResponse
	err = json.Unmarshal([]byte(pollResult.ForLLM), &pollResp)
	require.NoError(t, err)
	require.Equal(t, "running", pollResp.Status)

	// Wait for sleep to complete
	time.Sleep(2500 * time.Millisecond)

	// Poll should show done
	pollResult = tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError)

	err = json.Unmarshal([]byte(pollResult.ForLLM), &pollResp)
	require.NoError(t, err)
	require.Equal(t, "done", pollResp.Status)
}

func TestShellTool_PTY_Kill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a PTY session with a long-running command
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 10",
		"pty":        "true",
		"background": "true",
	})
	require.False(t, result.IsError, "PTY run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Kill the session
	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
	require.False(t, killResult.IsError, "kill should succeed: %s", killResult.ForLLM)

	// Verify kill response shows done status
	var killResp ExecResponse
	err = json.Unmarshal([]byte(killResult.ForLLM), &killResp)
	require.NoError(t, err)
	require.Equal(t, "done", killResp.Status)

	// The session is retained after kill so its final output stays readable.
	pollResult := tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError, "poll should still find the retained session: %s", pollResult.ForLLM)
	var pollResp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(pollResult.ForLLM), &pollResp))
	require.Equal(t, "done", pollResp.Status)
}

func TestShellTool_Write_Read_NonPTY(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a background process that reads from stdin and outputs it
	// Using 'cat' which echoes stdin to stdout
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "cat",
		"pty":        false,
		"background": "true",
	})
	require.False(t, result.IsError, "run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Write some input to cat
	writeResult := tool.Execute(ctx, map[string]any{
		"action":    "write",
		"sessionId": resp.SessionID,
		"data":      "hello world\n",
	})
	require.False(t, writeResult.IsError, "write should succeed: %s", writeResult.ForLLM)

	// Give cat time to process and output
	time.Sleep(200 * time.Millisecond)

	// Read the output
	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": resp.SessionID,
	})
	require.False(t, readResult.IsError, "read should succeed: %s", readResult.ForLLM)

	var readResp ExecResponse
	err = json.Unmarshal([]byte(readResult.ForLLM), &readResp)
	require.NoError(t, err)
	require.Contains(t, readResp.Output, "hello world")

	// Clean up
	tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
}

func TestShellTool_Read_NonPTY_Running(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a long-running process that produces output over time
	// Using sh -c with sleep at the end so process doesn't exit immediately
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sh -c 'echo line1; sleep 0.5; echo line2; sleep 0.5; echo line3; sleep 10'",
		"pty":        false,
		"background": "true",
	})
	require.False(t, result.IsError, "run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Give time for first outputs to be produced
	time.Sleep(300 * time.Millisecond)

	// Read output while process is running
	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": resp.SessionID,
	})
	require.False(t, readResult.IsError, "read should succeed: %s", readResult.ForLLM)

	var readResp ExecResponse
	err = json.Unmarshal([]byte(readResult.ForLLM), &readResp)
	require.NoError(t, err)
	// Should have at least line1
	require.Contains(t, readResp.Output, "line1")

	// Wait for line3 to be produced (line1=0s, line2=0.5s, line3=1s, then sleep 10)
	time.Sleep(1200 * time.Millisecond)

	// Read again - should have line3 as well
	readResult = tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": resp.SessionID,
	})
	require.False(t, readResult.IsError, "read should succeed: %s", readResult.ForLLM)

	err = json.Unmarshal([]byte(readResult.ForLLM), &readResp)
	require.NoError(t, err)
	require.Contains(t, readResp.Output, "line3")

	// Clean up
	tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
}

func TestShellTool_ProcessGroupKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Process group kill not supported on Windows")
	}

	// Note: Testing process group kill with PTY is tricky because the command
	// must be run through an interpreter (sh, bash) which is blocked for PTY.
	// Instead, we test with non-PTY mode which also uses Setsid for background processes.

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a shell that spawns child processes (non-PTY mode)
	// The sh -c command creates child sleep processes
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sh -c 'sleep 30 & sleep 30 & wait'",
		"pty":        false,
		"background": "true",
	})
	require.False(t, result.IsError, "run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Give time for child processes to spawn
	time.Sleep(500 * time.Millisecond)

	// Kill the session - should kill the entire process group
	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
	require.False(t, killResult.IsError, "kill should succeed: %s", killResult.ForLLM)

	// Verify kill response shows done status
	var killResp ExecResponse
	err = json.Unmarshal([]byte(killResult.ForLLM), &killResp)
	require.NoError(t, err)
	require.Equal(t, "done", killResp.Status)

	// The session is retained after kill so its final output stays readable.
	pollResult := tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError, "poll should still find the retained session: %s", pollResult.ForLLM)
	var pollResp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(pollResult.ForLLM), &pollResp))
	require.Equal(t, "done", pollResp.Status)
}

func TestShellTool_PTY_ProcessGroupKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY process group kill not supported on Windows")
	}

	// This test binary creates 4 child sleep processes and waits for signals.
	// It's not an interpreter, so it's allowed with PTY mode.
	// The binary is created in /tmp/test_pgroup.c and compiled as part of test setup.
	testBinary := "/tmp/test_pgroup"
	if _, err := os.Stat(testBinary); os.IsNotExist(err) {
		t.Skip("Test binary /tmp/test_pgroup not found - run: gcc -o /tmp/test_pgroup /tmp/test_pgroup.c")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start the test binary with PTY mode
	// It forks 4 child sleep processes and waits for signals
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    testBinary,
		"pty":        "true",
		"background": "true",
	})
	require.False(t, result.IsError, "run should succeed: %s", result.ForLLM)

	var resp ExecResponse
	err = json.Unmarshal([]byte(result.ForLLM), &resp)
	require.NoError(t, err)

	// Give time for child processes to spawn
	time.Sleep(500 * time.Millisecond)

	// Kill the session - should kill the entire process group
	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": resp.SessionID,
	})
	require.False(t, killResult.IsError, "kill should succeed: %s", killResult.ForLLM)

	// Verify kill response shows done status
	var killResp ExecResponse
	err = json.Unmarshal([]byte(killResult.ForLLM), &killResp)
	require.NoError(t, err)
	require.Equal(t, "done", killResp.Status)

	// The session is retained after kill so its final output stays readable.
	pollResult := tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError, "poll should still find the retained session: %s", pollResult.ForLLM)
	var pollResp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(pollResult.ForLLM), &pollResp))
	require.Equal(t, "done", pollResp.Status)
}

func TestShellTool_PTY_Background_Read(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a fast command with PTY + background mode
	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "echo hello",
		"pty":        "true",
		"background": "true",
	})
	require.False(t, runResult.IsError, "run should succeed: %s", runResult.ForLLM)

	var runResp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &runResp)
	require.NoError(t, err)
	require.NotEmpty(t, runResp.SessionID)
	require.Equal(t, "running", runResp.Status)

	// Wait for command to complete
	time.Sleep(500 * time.Millisecond)

	// Read output - this is the key test: PTY + background mode should preserve output
	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": runResp.SessionID,
	})
	require.False(t, readResult.IsError, "read should succeed: %s", readResult.ForLLM)
	require.Contains(t, readResult.ForLLM, "hello", "output should contain 'hello'")
}

func TestShellTool_PTY_Background_ReadNoBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	// Start a long-running command with PTY + background mode
	// This command produces no output, just sleeps
	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 10",
		"pty":        "true",
		"background": "true",
	})
	require.False(t, runResult.IsError, "run should succeed: %s", runResult.ForLLM)

	var runResp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &runResp)
	require.NoError(t, err)
	require.NotEmpty(t, runResp.SessionID)

	// Read immediately - should NOT block even though process is running and has no output
	// This tests that Read() returns quickly (within 1 second) instead of blocking for 10 seconds
	start := time.Now()
	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": runResp.SessionID,
	})
	elapsed := time.Since(start)

	require.False(t, readResult.IsError, "read should succeed: %s", readResult.ForLLM)
	require.Less(t, elapsed.Seconds(), 1.0, "read should not block, should return within 1 second")

	// Kill the session to clean up
	killResult := tool.Execute(ctx, map[string]any{
		"action":    "kill",
		"sessionId": runResp.SessionID,
	})
	require.False(t, killResult.IsError, "kill should succeed: %s", killResult.ForLLM)
}

func TestShellTool_Poll_Status(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 1",
		"background": "true",
	})
	require.False(t, runResult.IsError)

	var resp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &resp)
	require.NoError(t, err)

	pollResult := tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError)

	var pollResp ExecResponse
	err = json.Unmarshal([]byte(pollResult.ForLLM), &pollResp)
	require.NoError(t, err)
	require.Equal(t, "running", pollResp.Status)

	time.Sleep(1200 * time.Millisecond)

	pollResult = tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": resp.SessionID,
	})
	require.False(t, pollResult.IsError)

	err = json.Unmarshal([]byte(pollResult.ForLLM), &pollResp)
	require.NoError(t, err)
	require.Equal(t, "done", pollResp.Status)
}

func TestShellTool_Action_Run_Sync(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	ctx := context.Background()

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo hello",
	})

	require.False(t, result.IsError)
	require.Contains(t, result.ForLLM, "hello")
}

// TestShellTool_Background_ReadAfterExit verifies that we can read
// buffered output even after the background process has exited.
func TestShellTool_Background_ReadAfterExit(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	ctx := context.Background()

	// Start a background command that produces output and exits quickly
	runResult := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "echo hello && sleep 1 && echo world",
		"background": "true",
	})
	require.False(t, runResult.IsError, "run should succeed: %s", runResult.ForUser)

	// Parse session ID from response
	var resp ExecResponse
	err = json.Unmarshal([]byte(runResult.ForLLM), &resp)
	require.NoError(t, err)
	require.NotEmpty(t, resp.SessionID)
	sessionID := resp.SessionID

	// Wait for process to exit (sleep 1 + some buffer)
	time.Sleep(1500 * time.Millisecond)

	// Poll to verify process is done
	pollResult := tool.Execute(ctx, map[string]any{
		"action":    "poll",
		"sessionId": sessionID,
	})
	require.False(t, pollResult.IsError, "poll should succeed: %s", pollResult.ForLLM)
	var pollResp ExecResponse
	err = json.Unmarshal([]byte(pollResult.ForLLM), &pollResp)
	require.NoError(t, err)
	require.Equal(t, "done", pollResp.Status, "process should be done")

	// Try to read output AFTER process has exited
	readResult := tool.Execute(ctx, map[string]any{
		"action":    "read",
		"sessionId": sessionID,
	})
	require.False(t, readResult.IsError, "read should succeed after exit: %s", readResult.ForLLM)

	var readResp ExecResponse
	err = json.Unmarshal([]byte(readResult.ForLLM), &readResp)
	require.NoError(t, err)

	// Output should contain both "hello" and "world"
	require.Contains(t, readResp.Output, "hello", "should contain hello")
	require.Contains(t, readResp.Output, "world", "should contain world after sleep")
}

func TestSendKeys_CtrlC(t *testing.T) {
	// Note: Ctrl-C as a signal requires sending SIGINT to the process group,
	// which requires elevated privileges. Writing "\x03" to PTY passes the byte
	// to the process but doesn't generate SIGINT for processes that don't read stdin.
	// For interrupting processes, use the kill action instead.
	t.Skip("Ctrl-C as signal not supported - use kill action for interruption")
}

func TestEncodeKeyToken(t *testing.T) {
	tests := []struct {
		token    string
		expected string
		hasError bool
	}{
		// Named keys
		{"enter", "\r", false},
		{"return", "\r", false},
		{"tab", "\t", false},
		{"escape", "\x1b", false},
		{"esc", "\x1b", false},
		{"backspace", "\x7f", false},
		{"up", "\x1b[A", false},
		{"down", "\x1b[B", false},
		{"left", "\x1b[D", false},
		{"right", "\x1b[C", false},
		{"home", "\x1b[1~", false},
		{"end", "\x1b[4~", false},
		{"pageup", "\x1b[5~", false},
		{"pagedown", "\x1b[6~", false},
		{"delete", "\x1b[3~", false},
		{"f1", "\x1bOP", false},
		{"f12", "\x1b[24~", false},

		// Ctrl keys
		{"ctrl-c", "\x03", false},
		{"ctrl-d", "\x04", false},
		{"ctrl-a", "\x01", false},
		{"ctrl-z", "\x1a", false},
		{"c-c", "\x03", false},
		{"c-d", "\x04", false},

		// Alt keys
		{"alt-x", "\x1bx", false},
		{"m-x", "\x1bx", false},

		// Case insensitive tests
		{"ENTER", "\r", false},
		{"TAB", "\t", false},
		{"CTRL-C", "\x03", false},
		{"Ctrl-D", "\x04", false},
		{"ALT-X", "\x1bx", false},
		{"M-X", "\x1bx", false},
		{"UP", "\x1b[A", false},
		{"DOWN", "\x1b[B", false},

		// Unknown keys should return error (use write action for text input)
		{"unknown-key", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.token, func(t *testing.T) {
			result, err := encodeKeyToken(tt.token, PtyKeyModeCSI)
			if tt.hasError {
				require.Error(t, err, "expected error for %s", tt.token)
			} else {
				require.NoError(t, err, "unexpected error for %s", tt.token)
				require.Equal(t, tt.expected, result, "wrong encoding for %s", tt.token)
			}
		})
	}
}

// TestDetectPtyKeyMode tests smkx/rmkx detection in PTY output
func TestDetectPtyKeyMode(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected PtyKeyMode
	}{
		{"no toggle", "hello world", PtyKeyModeNotFound},
		{"smkx only", "\x1b[?1h\x1b=", PtyKeyModeSS3},
		{"rmkx only", "\x1b[?1l\x1b>", PtyKeyModeCSI},
		{"both smkx first", "\x1b[?1h\x1b=...\x1b[?1l\x1b>", PtyKeyModeCSI},
		{"both rmkx first", "\x1b[?1l\x1b>...\x1b[?1h\x1b=", PtyKeyModeSS3},
		{"multiple toggles smkx last", "\x1b[?1h\x1b=...\x1b[?1l\x1b>...\x1b[?1h\x1b=", PtyKeyModeSS3},
		{"multiple toggles rmkx last", "\x1b[?1l\x1b>...\x1b[?1h\x1b=...\x1b[?1l\x1b>", PtyKeyModeCSI},
		{"partial smkx", "\x1b[?1h", PtyKeyModeSS3},
		{"partial rmkx", "\x1b[?1l", PtyKeyModeCSI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectPtyKeyMode(tt.raw)
			require.Equal(t, tt.expected, result, "wrong mode for %s", tt.name)
		})
	}
}

func TestEncodeKeyTokenWithPtyKeyMode(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		mode     PtyKeyMode
		expected string
		hasError bool
	}{
		// CSI mode
		{"up csi", "up", PtyKeyModeCSI, "\x1b[A", false},
		{"down csi", "down", PtyKeyModeCSI, "\x1b[B", false},
		{"left csi", "left", PtyKeyModeCSI, "\x1b[D", false},
		{"right csi", "right", PtyKeyModeCSI, "\x1b[C", false},

		// SS3 mode
		{"up ss3", "up", PtyKeyModeSS3, "\x1bOA", false},
		{"down ss3", "down", PtyKeyModeSS3, "\x1bOB", false},
		{"left ss3", "left", PtyKeyModeSS3, "\x1bOD", false},
		{"right ss3", "right", PtyKeyModeSS3, "\x1bOC", false},
		{"home ss3", "home", PtyKeyModeSS3, "\x1bOH", false},
		{"end ss3", "end", PtyKeyModeSS3, "\x1bOF", false},

		// Other keys unaffected by mode
		{"enter ss3", "enter", PtyKeyModeSS3, "\r", false},
		{"tab ss3", "tab", PtyKeyModeSS3, "\t", false},
		{"ctrl-c ss3", "ctrl-c", PtyKeyModeSS3, "\x03", false},

		// NotFound behaves like CSI
		{"up notfound", "up", PtyKeyModeNotFound, "\x1b[A", false},
		{"down notfound", "down", PtyKeyModeNotFound, "\x1b[B", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := encodeKeyToken(tt.token, tt.mode)
			if tt.hasError {
				require.Error(t, err, "expected error for %s", tt.name)
			} else {
				require.NoError(t, err, "unexpected error for %s", tt.name)
				require.Equal(t, tt.expected, result, "wrong encoding for %s", tt.name)
			}
		})
	}
}

// TestShellTool_SchemelessURLDetection verifies that the scheme-less URL
// detection logic in guardCommand correctly identifies web URL path components
// (e.g., "//github.com" captured by the regex after "https:") and exempts them
// from workspace sandbox checks. It also confirms that paths NOT preceded by a
// recognized web scheme are still blocked.
func TestShellTool_SchemelessURLDetection(t *testing.T) {
	tmpDir := t.TempDir()
	tool, err := NewExecTool(tmpDir, true)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	// Each of the 7 recognized web schemes should have its path component
	// exempted from workspace boundary checks.
	allowedCommands := []string{
		"echo https://github.com",
		"echo http://example.com",
		"echo ftp://ftp.example.com",
		"echo ftps://secure.example.com",
		"echo sftp://sftp.example.com",
		"echo ssh://git@github.com",
		"echo git://github.com",
	}

	for _, cmd := range allowedCommands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if result.IsError && strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("command with recognized web scheme should not be blocked: %s\n  error: %s", cmd, result.ForLLM)
		}
	}

	// Multiple URLs with different schemes in a single command should all be exempt.
	multiURLCommands := []string{
		"echo https://github.com && curl http://example.com",
		"wget ftp://a.com; curl https://b.com",
	}

	for _, cmd := range multiURLCommands {
		result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": cmd})
		if result.IsError && strings.Contains(result.ForLLM, "path outside working dir") {
			t.Errorf("command with multiple web URLs should not be blocked: %s\n  error: %s", cmd, result.ForLLM)
		}
	}
}

func TestShellTool_CustomAllowDoesNotBypassDenyPatterns(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.CustomAllowPatterns = []string{`^jq\b`}
	cfg.Tools.Exec.CustomDenyPatterns = []string{`\$env\b`, `(^|[^.$a-z0-9_])env([^a-z0-9_]|$)`}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}

	got := tool.guardCommand(`jq -n '$ENV.PICOCLAW_VARIANT_CANARY'`, t.TempDir())
	if !strings.Contains(got, "dangerous pattern detected") {
		t.Fatalf("custom allow should not bypass deny patterns, got: %q", got)
	}
}

func TestShellTool_CustomAllowStillPermitsSafeMatch(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.CustomAllowPatterns = []string{`^jq\b`}
	cfg.Tools.Exec.CustomDenyPatterns = []string{`\$env\b`, `(^|[^.$a-z0-9_])env([^a-z0-9_]|$)`}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}

	got := tool.guardCommand(`jq -n '"ok"'`, t.TempDir())
	if got != "" {
		t.Fatalf("safe custom-allowed command should pass guard, got: %q", got)
	}
}

func TestShellTool_CustomAllowDoesNotBecomeStrictAllowlist(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.CustomAllowPatterns = []string{`^jq\b`}
	cfg.Tools.Exec.CustomDenyPatterns = []string{`\$env\b`, `(^|[^.$a-z0-9_])env([^a-z0-9_]|$)`}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig() error: %v", err)
	}

	got := tool.guardCommand("ls", t.TempDir())
	if got != "" {
		t.Fatalf("custom allow patterns should not become a strict allowlist, got: %q", got)
	}
}

// TestShellTool_PTY_ReaderStopsAfterExit verifies that the PTY reader
// goroutine and its descriptors are released once the process exits, instead
// of staying blocked on the master end forever.
func TestShellTool_PTY_ReaderStopsAfterExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PTY not supported on Windows")
	}

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")

	runPTY := func() string {
		result := tool.Execute(ctx, map[string]any{
			"action":     "run",
			"command":    "echo done",
			"pty":        "true",
			"background": "true",
		})
		require.False(t, result.IsError, "PTY run should succeed: %s", result.ForLLM)
		var resp ExecResponse
		require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
		return resp.SessionID
	}

	// Warm up so lazily created runtime goroutines are already accounted for.
	runPTY()
	time.Sleep(300 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		id := runPTY()
		session, err := sm.Get(id)
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			session.mu.Lock()
			defer session.mu.Unlock()
			return session.Status == "done"
		}, 5*time.Second, 20*time.Millisecond, "session should finish")
	}

	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baseline+1
	}, 5*time.Second, 50*time.Millisecond,
		"PTY reader goroutines should stop after the process exits")
}

func startBackgroundSession(t *testing.T, command string) (*ExecTool, context.Context, string) {
	t.Helper()
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{"action": "run", "command": command, "background": "true"})
	require.False(t, result.IsError, result.ForLLM)

	var resp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
	t.Cleanup(func() {
		tool.Execute(ctx, map[string]any{"action": "kill", "sessionId": resp.SessionID})
	})
	return tool, ctx, resp.SessionID
}

func execResp(t *testing.T, result *ToolResult) ExecResponse {
	t.Helper()
	require.False(t, result.IsError, result.ForLLM)
	var resp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
	return resp
}

func TestShellTool_Wait_ReturnsOutputOnExit(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sleep 1 && echo ok")

	start := time.Now()
	resp := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "wait", "sessionId": id, "timeout": float64(10),
	}))

	require.Equal(t, "done", resp.Status)
	require.Contains(t, resp.Output, "ok")
	require.Empty(t, resp.Note)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestShellTool_Wait_TimeoutIsNotAnError(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sleep 30")

	resp := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "wait", "sessionId": id, "timeout": float64(1),
	}))

	require.Equal(t, "running", resp.Status)
	require.Contains(t, resp.Note, "not an error")
}

func TestShellTool_Wait_ClampsToConfiguredTimeout(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sleep 30")
	tool.SetTimeout(time.Second)

	resp := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "wait", "sessionId": id, "timeout": float64(600),
	}))

	require.Equal(t, "running", resp.Status)
	require.Contains(t, resp.Note, "clamped to the configured limit of 1s")
}

func TestShellTool_Wait_InvalidArgs(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sleep 30")

	for _, timeout := range []any{float64(0), float64(-1), float64(1.5), "5"} {
		result := tool.Execute(ctx, map[string]any{"action": "wait", "sessionId": id, "timeout": timeout})
		require.True(t, result.IsError, "timeout=%v", timeout)
	}
	require.True(t, tool.Execute(ctx, map[string]any{"action": "wait"}).IsError)
	require.True(t, tool.Execute(ctx, map[string]any{"action": "wait", "sessionId": "nope"}).IsError)
}

func TestShellTool_Wait_CanceledContext(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sleep 30")
	cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()

	result := tool.Execute(cctx, map[string]any{"action": "wait", "sessionId": id, "timeout": float64(30)})

	require.True(t, result.IsError)
	require.Contains(t, result.ForLLM, "wait interrupted")
}

func TestShellTool_Poll_IdleNoteAfterTwoSilentChecks(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sleep 30")
	poll := func() ExecResponse {
		return execResp(t, tool.Execute(ctx, map[string]any{"action": "poll", "sessionId": id}))
	}

	require.Empty(t, poll().Note)
	require.Contains(t, poll().Note, "action=wait")
}

func TestShellTool_Poll_NewOutputResetsIdleCount(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "echo first; sleep 1; echo second; sleep 30")
	poll := func() ExecResponse {
		return execResp(t, tool.Execute(ctx, map[string]any{"action": "poll", "sessionId": id}))
	}

	time.Sleep(300 * time.Millisecond)
	require.Empty(t, poll().Note) // sees "first": output changed
	time.Sleep(1500 * time.Millisecond)
	require.Empty(t, poll().Note) // sees "second": output changed again
	require.Empty(t, poll().Note) // first silent check
	require.Contains(t, poll().Note, "action=wait")
}

func TestShellTool_ExecuteAsync_NotifiesOnCompletion(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	done := make(chan *ToolResult, 1)
	runResult := tool.ExecuteAsync(ctx, map[string]any{
		"action":     "run",
		"command":    "sh -c 'sleep 0.1; echo background-done'",
		"background": true,
		"notify":     true,
	}, func(_ context.Context, result *ToolResult) {
		done <- result
	})
	require.False(t, runResult.IsError, runResult.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(runResult.ForLLM), &started))
	require.Equal(t, notifyEnabledNote, started.Note)

	select {
	case result := <-done:
		require.Contains(t, result.ForLLM, "background-done")
		require.Contains(t, result.ForLLM, "exitCode: 0")
	case <-time.After(3 * time.Second):
		t.Fatal("completion notification did not arrive")
	}
}

func TestShellTool_ExecuteAsync_PromotedRunNotifies(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	done := make(chan *ToolResult, 1)
	runResult := tool.ExecuteAsync(ctx, map[string]any{
		"action":  "run",
		"command": "sh -c 'sleep 0.1; echo promoted-done'",
		"timeout": float64(config.MaxExecForegroundTimeoutSeconds + 1),
		"notify":  true,
	}, func(_ context.Context, result *ToolResult) {
		done <- result
	})
	require.False(t, runResult.IsError, runResult.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(runResult.ForLLM), &started))
	require.Contains(t, started.Note, "foreground cap")
	require.Contains(t, started.Note, notifyEnabledNote)

	select {
	case result := <-done:
		require.Contains(t, result.ForLLM, "promoted-done")
		require.Contains(t, result.ForLLM, "exitCode: 0")
	case <-time.After(3 * time.Second):
		t.Fatal("completion notification did not arrive")
	}
}

func TestShellTool_AutoNotifyAfterSilentPolls(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	done := make(chan *ToolResult, 1)
	runResult := tool.ExecuteAsync(ctx, map[string]any{
		"action":     "run",
		"command":    "sh -c 'sleep 1; echo auto-done'",
		"background": true,
	}, func(_ context.Context, result *ToolResult) {
		done <- result
	})
	require.False(t, runResult.IsError, runResult.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(runResult.ForLLM), &started))

	first := execResp(t, tool.Execute(ctx, map[string]any{"action": "poll", "sessionId": started.SessionID}))
	require.Empty(t, first.Note)

	second := execResp(t, tool.Execute(ctx, map[string]any{"action": "poll", "sessionId": started.SessionID}))
	require.Contains(t, second.Note, "completion notification")
	require.Contains(t, second.Note, "follow-up message")

	select {
	case result := <-done:
		require.Contains(t, result.ForLLM, "auto-done")
	case <-time.After(4 * time.Second):
		t.Fatal("auto-enabled completion notification did not arrive")
	}
}

func TestShellTool_NotifyRequiresBackground(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	result := tool.Execute(context.Background(), map[string]any{
		"action":  "run",
		"command": "echo foreground",
		"notify":  true,
	})
	require.True(t, result.IsError)
	require.Contains(t, result.ForLLM, "background=true")
}

func TestShellTool_NotifyNoteRequiresAsyncCallback(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 5",
		"background": true,
		"notify":     true,
	})
	require.False(t, result.IsError, result.ForLLM)

	var resp ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &resp))
	t.Cleanup(func() {
		tool.Execute(ctx, map[string]any{"action": "kill", "sessionId": resp.SessionID})
	})
	require.Empty(t, resp.Note, "direct Execute cannot deliver a completion callback")

	session, err := sm.Get(resp.SessionID)
	require.NoError(t, err)
	require.False(t, session.NotifyEnabled())
}

func TestShellTool_ReadIsPagedAndNonDestructive(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sh -c 'printf abcdefghij; sleep 5'")
	time.Sleep(200 * time.Millisecond)

	first := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "read", "sessionId": id, "offset": float64(0), "limit": float64(3),
	}))
	require.Equal(t, "abc", first.Output)
	require.Equal(t, int64(3), first.NextOffset)
	require.True(t, first.HasMore)

	second := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "read", "sessionId": id, "offset": float64(3), "limit": float64(4),
	}))
	require.Equal(t, "defg", second.Output)
	require.Equal(t, int64(7), second.NextOffset)

	again := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "read", "sessionId": id, "offset": float64(0), "limit": float64(3),
	}))
	require.Equal(t, "abc", again.Output)
}

func TestShellTool_ReadReportsCumulativeDroppedBytes(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	session := &ProcessSession{ID: "paged", Status: "running"}
	payload := strings.Repeat("x", maxOutputBufferSize+100)
	session.appendOutput([]byte(payload))
	sm.Add(session)

	resp := execResp(t, tool.Execute(context.Background(), map[string]any{
		"action": "read", "sessionId": "paged",
	}))
	require.Equal(t, int64(len(payload)), resp.TotalBytes)
	require.Equal(t, int64(100), resp.DroppedBytes)
	require.Zero(t, resp.SkippedBytes)
	require.Equal(t, resp.TotalBytes-int64(len(resp.Output)), resp.Offset)

	resp = execResp(t, tool.Execute(context.Background(), map[string]any{
		"action": "read", "sessionId": "paged", "offset": float64(0),
	}))
	require.Equal(t, int64(100), resp.DroppedBytes)
	require.Equal(t, int64(100), resp.SkippedBytes)
	require.Equal(t, int64(100), resp.Offset)
	require.Equal(t, int64(100+len(resp.Output)), resp.NextOffset)
	require.True(t, resp.HasMore)
	require.Contains(t, resp.Note, "skipped 100 bytes")
}

func TestShellTool_PollReturnsOutputPreview(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sh -c 'echo preview-line; sleep 5'")
	time.Sleep(200 * time.Millisecond)

	resp := execResp(t, tool.Execute(ctx, map[string]any{"action": "poll", "sessionId": id}))
	require.Equal(t, "running", resp.Status)
	require.Contains(t, resp.Output, "preview-line")
	require.Positive(t, resp.TotalBytes)
}

func TestShellTool_WaitReturnsPartialOutputOnTimeout(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sh -c 'echo before-timeout; sleep 5'")
	time.Sleep(200 * time.Millisecond)

	resp := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "wait", "sessionId": id, "timeout": float64(1),
	}))
	require.Equal(t, "running", resp.Status)
	require.Contains(t, resp.Output, "before-timeout")
	require.Contains(t, resp.Note, "not an error")
}

func TestShellTool_KillRetainsOutput(t *testing.T) {
	tool, ctx, id := startBackgroundSession(t, "sh -c 'echo before-stop; sleep 5'")
	time.Sleep(200 * time.Millisecond)

	killResp := execResp(t, tool.Execute(ctx, map[string]any{"action": "kill", "sessionId": id}))
	require.Equal(t, "done", killResp.Status)
	require.Contains(t, killResp.Output, "before-stop")

	readResp := execResp(t, tool.Execute(ctx, map[string]any{"action": "read", "sessionId": id}))
	require.Contains(t, readResp.Output, "before-stop")
}

func TestShellTool_CustomDenyCannotBeOverriddenByCustomAllow(t *testing.T) {
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns:  true,
				CustomAllowPatterns: []string{`\bgit\s+push\b`},
				CustomDenyPatterns:  []string{`\bgit\s+push\b`},
			},
		},
	}

	tool, err := NewExecToolWithConfig("", false, cfg)
	if err != nil {
		t.Fatalf("unable to configure exec tool: %s", err)
	}

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "git push origin main",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "dangerous pattern") {
		t.Fatalf("custom deny pattern must win over custom allow, got: %s", result.ForLLM)
	}
}

func TestShellTool_CustomDenyPatternsApplyWhenDefaultsDisabled(t *testing.T) {
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns: false,
				CustomDenyPatterns: []string{`\bgit\s+push\b`},
			},
		},
	}

	tool, err := NewExecToolWithConfig("", false, cfg)
	require.NoError(t, err)

	if got := tool.guardCommand("git push origin main", ""); !strings.Contains(got, "dangerous pattern") {
		t.Fatalf("custom deny pattern must apply when default patterns are disabled, got: %q", got)
	}
	if got := tool.guardCommand("echo safely", ""); got != "" {
		t.Fatalf("unrelated command should pass the guard, got: %q", got)
	}
}

func TestShellTool_InvalidCustomDenyPatternRejectedWhenDefaultsDisabled(t *testing.T) {
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns: false,
				CustomDenyPatterns: []string{"[invalid-regex"},
			},
		},
	}

	_, err := NewExecToolWithConfig("", false, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid custom deny pattern")
}

func TestShellTool_DefaultDenyPatternsCanBeDisabled(t *testing.T) {
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{EnableDenyPatterns: false},
		},
	}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	require.NoError(t, err)

	if got := tool.guardCommand("rm -rf ./build", t.TempDir()); got != "" {
		t.Fatalf("default deny patterns should remain optional, got: %q", got)
	}
}

func TestShellTool_HardlinePatternsAlwaysApply(t *testing.T) {
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				EnableDenyPatterns:  false,
				CustomAllowPatterns: []string{`.*`},
			},
		},
	}

	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	require.NoError(t, err)

	blocked := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -r -f /",
		"rm -rf -- /",
		"mkfs.ext4 /dev/sda1",
		"format /q c:",
		"dd if=/dev/zero of=/dev/sda",
		"echo x > /dev/sda",
		"shutdown -h now",
		":(){ :|:& };:",
	}
	for _, command := range blocked {
		if got := tool.guardCommand(command, t.TempDir()); !strings.Contains(got, "dangerous pattern") {
			t.Errorf("hardline command %q should be blocked, got: %q", command, got)
		}
	}
}

func TestShellTool_BackticksInsideQuotedHeredocAreAllowed(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	command := "gh pr comment 2763 --body-file - <<'TXT'\n" +
		"Fixed `pkg/tools/integration/web_test.go` and `brave` is now expected.\n" +
		"TXT"
	if got := tool.guardCommand(command, ""); got != "" {
		t.Fatalf("quoted heredoc body should not be blocked, got: %s", got)
	}
}

func TestShellTool_QuotedHeredocDoesNotHideDangerousHeader(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	command := "rm -rf /tmp/x <<'TXT'\n" + "markdown `body`\n" + "TXT"
	if got := tool.guardCommand(command, ""); got == "" {
		t.Fatal("dangerous command before a quoted heredoc should stay blocked")
	}
}

func TestShellTool_BackticksOutsideQuotedHeredocRemainBlocked(t *testing.T) {
	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	if got := tool.guardCommand("echo `whoami`", ""); got == "" {
		t.Fatal("raw backtick command substitution should be blocked")
	}
	if got := tool.guardCommand("echo hi <<TXT\n`whoami`\nTXT", ""); got == "" {
		t.Fatal("unquoted heredoc body can still substitute commands and should be blocked")
	}
}
