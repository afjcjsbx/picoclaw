package tools

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"
)

func (t *ExecTool) Name() string {
	return "exec"
}

// execDescription only depends on runtime.GOOS, so build it once instead of on every call.
var execDescription = buildExecDescription()

func (t *ExecTool) Description() string {
	return execDescription
}

func buildExecDescription() string {
	return execDescriptionFor(runtime.GOOS)
}

// execDescriptionFor renders the description for one OS. Windows gets its own
// wording because PowerShell has no && and no trailing-& backgrounding, so
// teaching those forms would send the model into rejected commands.
func execDescriptionFor(goos string) string {
	osName := goos
	shell := "sh"
	sessionGuidance := "Use `pty=true` for interactive programs. Trailing `&` implies background. " +
		"Never detach untracked processes via `nohup`, `setsid` or `disown`."
	chainGuidance := "chain dependent ones with `&&`."
	if goos == "windows" {
		shell = "PowerShell"
		sessionGuidance = "PTY mode is not supported on Windows. Do not start untracked background processes."
		chainGuidance = "PowerShell has no `&&`, so use `cmd1; if ($?) { cmd2 }`."
	} else if goos == "darwin" {
		osName = "macOS"
	}

	capSeconds := int64(maxForegroundTimeout / time.Second)
	return strings.Join([]string{
		fmt.Sprintf("Executes shell commands with PicoClaw safety guards. OS: %s; shell: %s.", osName, shell),
		"",
		"Each foreground call starts a fresh shell in the remembered cwd (use `cwd`, not `cd`). Prefer `list_dir`, `search_files`, `read_file`, `edit_file`, `write_file` over shell equivalents.",
		"",
		"Environment is sanitized; only safe and allowlisted variables are inherited. Set extras inline (`FOO=bar cmd`).",
		"",
		"Results contain `[exec]` metadata (exitCode, timedOut, truncated, durationMs, error), then stdout/stderr with ANSI escape sequences removed. Null exitCode means signal termination; 128+N may also indicate signals (137=SIGKILL, 143=SIGTERM). Treat these as failures. Some non-zero codes are normal (grep:1=no match; diff:1=differences).",
		"",
		"Truncated output is saved as an artifact; use `read_file` on its path.",
		"",
		fmt.Sprintf(
			"`run` requires `command`. Timeout is in seconds; foreground max is %ds. "+
				"`timeout=0` or above %ds starts a tracked background session. "+
				"`timeout` and `background=true` are incompatible.",
			capSeconds, capSeconds,
		),
		"",
		fmt.Sprintf(
			"Use background only when needed; it returns `sessionId`. "+
				"Use `list`, `poll`, `wait` (preferred; `notify=true` alerts on completion), "+
				"`read` (byte offset/limit), `write` (stdin), `send-keys` or `kill`. %s",
			sessionGuidance,
		),
		"",
		"Run independent commands separately; " + chainGuidance + " Avoid newline-separated commands.",
	}, "\n")
}

var execActions = []string{"run", "list", "poll", "wait", "read", "write", "kill", "send-keys"}

func (t *ExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        execActions,
				"description": "Action: run (execute command), list (show sessions), poll (check status, returns immediately), wait (block until a session exits or timeout), read (get output), write (send input), kill (terminate), send-keys (send keys to PTY)",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute (required for run)",
			},
			"sessionId": map[string]any{
				"type":        "string",
				"description": "Session ID (required for poll/wait/read/write/kill/send-keys)",
			},
			"keys": map[string]any{
				"type":        "string",
				"description": "Key names for send-keys: up, down, left, right, enter, tab, escape, backspace, ctrl-c, ctrl-d, home, end, pageup, pagedown, f1-f12",
			},
			"data": map[string]any{
				"type":        "string",
				"description": "Data to write to stdin (required for write)",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Run in background immediately",
			},
			"pty": map[string]any{
				"type":        "boolean",
				"description": "Run in a pseudo-terminal (PTY) when available",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory for the command. The last explicit value is remembered for later calls in the same conversation; omit cwd to reuse it. Defaults to the agent workspace when no value is remembered.",
			},
			"timeout": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Optional timeout in seconds for foreground commands; overrides the configured timeout. Foreground runs are capped at %ds: 0 (no timeout) and values above the cap start a tracked background session instead. Not supported with background=true. For wait: maximum seconds to block (default and cap: the configured timeout).",
					int64(maxForegroundTimeout/time.Second),
				),
			},
			"notify": map[string]any{
				"type":        "boolean",
				"description": "With background=true or a run promoted to background, send a completion message when the process exits. Use it for jobs with a defined end; do not poll or wait in a loop.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "With read: absolute byte offset into the retained session log. Defaults to the start of the most recent limit bytes; use 0 for the oldest retained byte.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "With read: maximum bytes to return. Defaults to the configured foreground output cap.",
			},
		},
		"required": []string{"action"},
	}
}

// boolArg reads a boolean flag. The schema declares these fields as booleans,
// but the string branch keeps calls working for models that still emit the
// "true"/"false" form the schema used to advertise.
func boolArg(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// numberArg reads an optional integer JSON number. JSON tool arguments decode
// every number as float64, so a fractional value is rejected. valid is false
// only when the key is present and is not an integer >= min.
func numberArg(args map[string]any, key string, minimum float64) (value float64, provided, valid bool) {
	raw, ok := args[key]
	if !ok {
		return 0, false, true
	}
	value, ok = raw.(float64)
	if !ok || value != math.Trunc(value) || value < minimum {
		return 0, true, false
	}
	return value, true, true
}

// foregroundPromotionNote explains why a requested foreground timeout was
// turned into a tracked background session.
func (t *ExecTool) foregroundPromotionNote(timeoutDisabled bool, timeout time.Duration) string {
	capSeconds := int64(t.foregroundCap / time.Second)
	reason := fmt.Sprintf(
		"Requested timeout of %ds exceeds the %ds foreground cap",
		int64(timeout/time.Second), capSeconds,
	)
	if timeoutDisabled {
		reason = fmt.Sprintf(
			"timeout=0 disables the timeout, which exceeds the %ds foreground cap",
			capSeconds,
		)
	}
	return reason + "; started as a tracked background session."
}
