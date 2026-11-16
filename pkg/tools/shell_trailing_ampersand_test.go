package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestStripTrailingBackgroundAmpersand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
		strip   bool
	}{
		{
			name:    "plain trailing operator",
			command: "sleep 1 &",
			want:    "sleep 1",
			strip:   true,
		},
		{
			name:    "no space before operator",
			command: "sleep 1&",
			want:    "sleep 1",
			strip:   true,
		},
		{
			name:    "trailing whitespace and newline",
			command: "sleep 1 & \t\n",
			want:    "sleep 1",
			strip:   true,
		},
		{
			name:    "and list without operator",
			command: "echo a && echo b",
			want:    "echo a && echo b",
			strip:   false,
		},
		{
			name:    "and list with operator",
			command: "echo a && echo b &",
			want:    "echo a && echo b",
			strip:   true,
		},
		{
			name:    "stderr redirect",
			command: "echo hi 2>&1",
			want:    "echo hi 2>&1",
			strip:   false,
		},
		{
			name:    "stdout redirect",
			command: "echo hi >&2",
			want:    "echo hi >&2",
			strip:   false,
		},
		{
			name:    "pipe both",
			command: "echo hi |&",
			want:    "echo hi |&",
			strip:   false,
		},
		{
			name:    "single quoted",
			command: `echo 'a &'`,
			want:    `echo 'a &'`,
			strip:   false,
		},
		{
			name:    "double quoted",
			command: `echo "a &"`,
			want:    `echo "a &"`,
			strip:   false,
		},
		{
			name:    "escaped operator",
			command: `echo a \&`,
			want:    `echo a \&`,
			strip:   false,
		},
		{
			name:    "comment",
			command: "echo hi # &",
			want:    "echo hi # &",
			strip:   false,
		},
		{
			name:    "comment before real operator",
			command: "echo hi # comment\nsleep 1 &",
			want:    "echo hi # comment\nsleep 1",
			strip:   true,
		},
		{
			name:    "heredoc body quote does not leak",
			command: "cat <<'EOF'\n'\nEOF\necho hi &",
			want:    "cat <<'EOF'\n'\nEOF\necho hi",
			strip:   true,
		},
		{
			name:    "unterminated heredoc",
			command: "cat <<EOF\nbody &",
			want:    "cat <<EOF\nbody &",
			strip:   false,
		},
		{
			name:    "delimiter with operator is body text",
			command: "cat <<EOF\nbody\nEOF &",
			want:    "cat <<EOF\nbody\nEOF &",
			strip:   false,
		},
		{
			name:    "operator alone on a line",
			command: "echo hi\n&",
			want:    "echo hi\n&",
			strip:   false,
		},
		{
			name:    "subshell close then operator",
			command: "{ echo hi; } &",
			want:    "{ echo hi; }",
			strip:   true,
		},
		{
			name:    "only operator",
			command: "&",
			want:    "&",
			strip:   false,
		},
		{
			name:    "only whitespace and operator",
			command: "  & ",
			want:    "  & ",
			strip:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, stripped := stripTrailingBackgroundAmpersand(tt.command)
			require.Equal(t, tt.strip, stripped)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeTrailingBackgroundAmpersandOSGate(t *testing.T) {
	t.Run("unix", func(t *testing.T) {
		got, converted := normalizeTrailingBackgroundAmpersand("sleep 1 &", "linux")
		require.True(t, converted)
		require.Equal(t, "sleep 1", got)
	})

	t.Run("windows", func(t *testing.T) {
		got, converted := normalizeTrailingBackgroundAmpersand("sleep 1 &", "windows")
		require.False(t, converted)
		require.Equal(t, "sleep 1 &", got)
	})
}

func newTrailingAmpersandTool(t *testing.T) (*ExecTool, context.Context) {
	t.Helper()

	tool, err := NewExecTool("", false)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	return tool, WithToolContext(context.Background(), "cli", "test")
}

func TestShellTool_TrailingAmpersandStartsTrackedSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trailing '&' is Unix shell syntax")
	}

	tool, ctx := newTrailingAmpersandTool(t)

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo ampersand-done &",
	})
	require.False(t, result.IsError, result.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)
	require.Contains(t, started.Note, "Trailing '&' was converted to background=true")

	list := execResp(t, tool.Execute(ctx, map[string]any{"action": "list"}))
	require.Len(t, list.Sessions, 1)
	require.Equal(t, "echo ampersand-done", list.Sessions[0].Command,
		"the tracked process must be the command itself, not a shell that exits immediately")

	waited := execResp(t, tool.Execute(ctx, map[string]any{
		"action":    "wait",
		"sessionId": started.SessionID,
		"timeout":   float64(5),
	}))
	require.Equal(t, "done", waited.Status)
	require.Contains(t, waited.Output, "ampersand-done")
}

func TestShellTool_TrailingAmpersandCapturesOutputPastDrainWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trailing '&' is Unix shell syntax")
	}

	tool, ctx := newTrailingAmpersandTool(t)

	// The old foreground path closed the output pipe after a 2s drain, so a
	// background job that logged later lost the output. A tracked session must
	// keep reading until the process exits.
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "sh -c 'sleep 3; echo survived' &",
	})
	require.False(t, result.IsError, result.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)

	waited := execResp(t, tool.Execute(ctx, map[string]any{
		"action":    "wait",
		"sessionId": started.SessionID,
		"timeout":   float64(10),
	}))
	require.Equal(t, "done", waited.Status)
	require.Contains(t, waited.Output, "survived")
}

func TestShellTool_TrailingAmpersandStillPassesGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trailing '&' is Unix shell syntax")
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.CustomDenyPatterns = []string{`&\s*$`}

	tool, err := NewExecToolWithConfig("", false, cfg)
	require.NoError(t, err)

	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	tool.sessionManager = sm

	ctx := WithToolContext(context.Background(), "cli", "test")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo guarded &",
	})
	require.True(t, result.IsError, result.ForLLM)
	require.Contains(t, result.ForLLM, "safety guard")

	list := execResp(t, tool.Execute(ctx, map[string]any{"action": "list"}))
	require.Empty(t, list.Sessions)
}

func TestShellTool_TrailingAmpersandIgnoresForegroundTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trailing '&' is Unix shell syntax")
	}

	tool, ctx := newTrailingAmpersandTool(t)

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "sleep 1 &",
		"timeout": float64(5),
	})
	require.False(t, result.IsError, result.ForLLM)

	var started ExecResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &started))
	require.NotEmpty(t, started.SessionID)
	require.Contains(t, started.Note, "timeout was ignored")

	t.Cleanup(func() {
		tool.Execute(ctx, map[string]any{"action": "kill", "sessionId": started.SessionID})
	})
}

func TestShellTool_TrailingAmpersandOnlyIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("trailing '&' is Unix shell syntax")
	}

	tool, ctx := newTrailingAmpersandTool(t)

	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": " & ",
	})
	// There is no command to background, so the operator is left to the shell,
	// which rejects it. It must not start an empty tracked session.
	require.True(t, result.IsError, result.ForLLM)
}

func TestShellTool_NonTrailingAmpersandStaysForeground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell quoting and redirection syntax")
	}

	tool, ctx := newTrailingAmpersandTool(t)

	commands := []string{
		`echo "a &"`,
		`echo 'a &'`,
		`echo a 2>&1`,
		`echo a && echo b`,
		`echo a \&`,
		`echo a # &`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{
				"action":  "run",
				"command": command,
			})
			require.False(t, result.IsError, result.ForLLM)
			require.True(t, strings.HasPrefix(result.ForLLM, execRunMetaPrefix),
				"expected a foreground result, got: %s", result.ForLLM)
		})
	}

	list := execResp(t, tool.Execute(ctx, map[string]any{"action": "list"}))
	require.Empty(t, list.Sessions)
}
