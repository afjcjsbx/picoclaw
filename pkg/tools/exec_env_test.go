package tools

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func execEnvMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			result[key] = value
		}
	}
	return result
}

func TestNewExecEnv_DefaultAllowlist(t *testing.T) {
	t.Setenv("EXEC_ENV_TEST_SECRET", "parent-secret")

	env := newExecEnv("", nil, nil)

	require.NotEmpty(t, env["PATH"])
	if runtime.GOOS != "windows" {
		require.Equal(t, os.Getenv("HOME"), env["HOME"])
	}
	require.NotContains(t, env, "EXEC_ENV_TEST_SECRET")
}

func TestNewExecEnv_ExtraAllowlist(t *testing.T) {
	t.Setenv("EXEC_ENV_TEST_CUSTOM", "custom-value")
	t.Setenv("EXEC_ENV_TEST_OTHER", "other-value")

	env := newExecEnv("", nil, []string{"EXEC_ENV_TEST_CUSTOM"})

	require.Equal(t, "custom-value", env["EXEC_ENV_TEST_CUSTOM"])
	require.NotContains(t, env, "EXEC_ENV_TEST_OTHER")
}

func TestNewExecEnv_EnvSetOverridesInherited(t *testing.T) {
	t.Setenv("PATH", "/parent/path")
	t.Setenv("EXEC_ENV_TEST_INHERITED", "parent-value")

	env := newExecEnv("", map[string]string{
		"PATH":                    "/config/path",
		"EXEC_ENV_TEST_INHERITED": "config-value",
	}, nil)

	require.Equal(t, "/config/path", env["PATH"])
	require.Equal(t, "config-value", env["EXEC_ENV_TEST_INHERITED"])
}

func TestNewExecEnv_EnvSetIsTrustedAndPicoclawVarsWin(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, "/custom/config.json")

	env := newExecEnv("/workspace", map[string]string{
		"MY_API_KEY":    "config-secret",
		"PICOCLAW_HOME": "/hijacked/home",
	}, nil)

	require.Equal(t, "config-secret", env["MY_API_KEY"])
	require.Equal(t, home, env["PICOCLAW_HOME"])
	require.Equal(t, "/custom/config.json", env["PICOCLAW_CONFIG"])
	require.Equal(t, "/workspace", env["PICOCLAW_AGENT_WORKSPACE"])
	require.Equal(t, "picoclaw", env["PICOCLAW_SERVICE_NAME"])
	require.NotEmpty(t, env["PICOCLAW_EXE"])
}

func TestExecTool_RunEnvCopiesBase(t *testing.T) {
	tool := &ExecTool{baseEnv: map[string]string{
		"PATH":          "/usr/bin",
		"HOME":          "/home/test",
		"PICOCLAW_HOME": "/original/home",
		"ALLOWED":       "base",
	}}

	env := execEnvMap(tool.runEnv(42 * time.Second))

	require.Equal(t, "/usr/bin", env["PATH"])
	require.Equal(t, "/home/test", env["HOME"])
	require.Equal(t, "/original/home", env["PICOCLAW_HOME"])
	require.Equal(t, "base", env["ALLOWED"])
	require.NotEmpty(t, env["PICOCLAW_EXEC_TIME"])
	require.Equal(t, "42s", env["PICOCLAW_EXEC_TIMEOUT"])
	require.Equal(t, "/original/home", tool.baseEnv["PICOCLAW_HOME"], "runEnv must not mutate the cached base")
}

func TestExecTool_EnvSanitizationReachesChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX env command")
	}

	t.Setenv("EXEC_ENV_TEST_SECRET", "parent-secret")
	t.Setenv("EXEC_ENV_TEST_ALLOWED", "parent-value")
	t.Setenv(config.EnvHome, t.TempDir())

	workspace := t.TempDir()
	cfg := &config.Config{}
	cfg.Tools.Exec.EnvSet = map[string]string{"EXEC_ENV_TEST_SET": "config-value"}
	cfg.Tools.Exec.EnvAllowlist = []string{"EXEC_ENV_TEST_ALLOWED"}

	tool, err := NewExecToolWithConfig(workspace, false, cfg)
	require.NoError(t, err)

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "env",
		"timeout": float64(5),
	})

	require.False(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, "EXEC_ENV_TEST_SET=config-value")
	require.Contains(t, result.ForLLM, "EXEC_ENV_TEST_ALLOWED=parent-value")
	require.NotContains(t, result.ForLLM, "EXEC_ENV_TEST_SECRET")
	require.Contains(t, result.ForLLM, "PICOCLAW_AGENT_WORKSPACE="+workspace)
	require.Contains(t, result.ForLLM, "PICOCLAW_EXEC_TIME=")
	require.Contains(t, result.ForLLM, "PICOCLAW_EXEC_TIMEOUT=5s")
}

func TestExecTool_PerCallEnvIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX env command")
	}

	tool, err := NewExecToolWithConfig("", false, nil)
	require.NoError(t, err)

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "env",
		"env": map[string]any{
			"PATH":             "/hacked",
			"LD_PRELOAD":       "/tmp/evil.so",
			"EXEC_ENV_TEST_OK": "ok",
		},
	})

	require.False(t, result.IsError, result.ForLLM)
	require.NotContains(t, result.ForLLM, "EXEC_ENV_TEST_OK=ok")
	require.NotContains(t, result.ForLLM, "PATH=/hacked")
	require.NotContains(t, result.ForLLM, "LD_PRELOAD")
}

func TestExecTool_EnvSanitizationInBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses the POSIX env command")
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnvSet = map[string]string{"EXEC_ENV_TEST_BG": "background-value"}

	tool, err := NewExecToolWithConfig("", false, cfg)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "env",
		"background": "true",
	})
	require.False(t, result.IsError, result.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)

	resp := execResp(t, tool.Execute(ctx, map[string]any{
		"action": "wait", "sessionId": started.SessionID, "timeout": float64(10),
	}))
	require.Equal(t, "done", resp.Status)
	require.Contains(t, resp.Output, "EXEC_ENV_TEST_BG=background-value")
	require.Contains(t, resp.Output, "PICOCLAW_EXEC_TIMEOUT=0s")
}
