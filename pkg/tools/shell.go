package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/logger"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
)

// maxOutputBufferSize caps the rolling output retained per session.
const maxOutputBufferSize = 1 * 1024 * 1024 // 1MB

var (
	globalSessionManager = NewSessionManager()
	sessionManagerMu     sync.RWMutex
)

func getSessionManager() *SessionManager {
	sessionManagerMu.RLock()
	defer sessionManagerMu.RUnlock()
	return globalSessionManager
}

type ExecTool struct {
	workingDir           string
	timeout              time.Duration
	denyPatterns         []*regexp.Regexp
	allowPatterns        []*regexp.Regexp
	customAllowPatterns  []*regexp.Regexp
	allowedPathPatterns  []*regexp.Regexp
	restrictToWorkspace  bool
	allowRemote          bool
	maxOutputChars       int
	maxInlineResultChars int
	sessionManager       *SessionManager
}

var _ AsyncExecutor = (*ExecTool)(nil)

var (
	defaultDenyPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		// Match disk wiping commands (must be followed by space/args)
		regexp.MustCompile(
			`(^|[^-\w])\b(format|mkfs|diskpart)\b\s`,
		),
		regexp.MustCompile(`\bdd\s+if=`),
		// Block writes to block devices (all common naming schemes).
		regexp.MustCompile(
			`>\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
		),
		regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
		regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
		regexp.MustCompile(`\$\([^)]+\)`),
		regexp.MustCompile(`\$\{[^}]+\}`),
		regexp.MustCompile("`[^`]+`"),
		regexp.MustCompile(`\|\s*sh\b`),
		regexp.MustCompile(`\|\s*bash\b`),
		regexp.MustCompile(`;\s*rm\s+-[rf]`),
		regexp.MustCompile(`&&\s*rm\s+-[rf]`),
		regexp.MustCompile(`\|\|\s*rm\s+-[rf]`),
		regexp.MustCompile(`<<\s*EOF`),
		regexp.MustCompile(`\$\(\s*cat\s+`),
		regexp.MustCompile(`\$\(\s*curl\s+`),
		regexp.MustCompile(`\$\(\s*wget\s+`),
		regexp.MustCompile(`\$\(\s*which\s+`),
		regexp.MustCompile(`\bsudo\b`),
		regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
		regexp.MustCompile(`\bchown\b`),
		regexp.MustCompile(`\bpkill\b`),
		regexp.MustCompile(`\bkillall\b`),
		regexp.MustCompile(`\bkill\b`),
		regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),
		regexp.MustCompile(`\bnpm\s+install\s+-g\b`),
		regexp.MustCompile(`\bpip\s+install\s+--user\b`),
		regexp.MustCompile(`\bapt\s+(install|remove|purge)\b`),
		regexp.MustCompile(`\byum\s+(install|remove)\b`),
		regexp.MustCompile(`\bdnf\s+(install|remove)\b`),
		regexp.MustCompile(`\bdocker\s+run\b`),
		regexp.MustCompile(`\bdocker\s+exec\b`),
		regexp.MustCompile(`\bgit\s+push\b`),
		regexp.MustCompile(`\bgit\s+force\b`),
		regexp.MustCompile(`\bssh\b.*@`),
		regexp.MustCompile(`\beval\b`),
		regexp.MustCompile(`\bsource\s+.*\.sh\b`),
	}

	// windowsDenyPatterns contains PowerShell-specific deny patterns that only
	// apply on Windows, where commands are executed via powershell -Command.
	windowsDenyPatterns = []*regexp.Regexp{
		// [Text.Encoding] used to construct command strings at runtime.
		// Matches [Text.Encoding] and [System.Text.Encoding] variants.
		regexp.MustCompile(`\[(?:\w+\.)?text\.encoding\]`),
		// PowerShell -EncodedCommand flag (base64-encoded command) and all short forms.
		// Matches: -e, -ec, -enc, -en, -EncodedCommand (all with space prefix)
		regexp.MustCompile(` -e(?:$|\s)| -ec(?:$|\s)| -enc(?:$|\s)| -en(?:$|\s)| -encodedcommand\b`),
		// .GetString called on byte array to decode commands.
		regexp.MustCompile(`\.getstring\s*\(\s*\[byte\[\]`),
		// FromBase64String used in command construction chain.
		regexp.MustCompile(`frombase64string\(`),
		// PowerShell variable holding byte array used in GetString.
		regexp.MustCompile(`\$[a-zA-Z_]\w*\s*=\s*\[byte\[\]`),
		// Unicode escape sequences that could be used to construct commands.
		// Matches \uXXXX format used to represent characters like i = "i"
		regexp.MustCompile(`\\u[0-9a-fA-F]{4}`),
	}

	// absolutePathPattern matches absolute file paths in commands (Unix and Windows).
	absolutePathPattern = regexp.MustCompile(`[A-Za-z]:\\[^\\\"']+|/[^\s\"']+`)

	// safePaths are kernel pseudo-devices that are always safe to reference in
	// commands, regardless of workspace restriction. They contain no user data
	// and cannot cause destructive writes.
	safePaths = map[string]bool{
		"/dev/null":    true,
		"/dev/zero":    true,
		"/dev/random":  true,
		"/dev/urandom": true,
		"/dev/stdin":   true,
		"/dev/stdout":  true,
		"/dev/stderr":  true,
	}
)

func NewExecTool(workingDir string, restrict bool, allowPaths ...[]*regexp.Regexp) (*ExecTool, error) {
	return NewExecToolWithConfig(workingDir, restrict, nil, allowPaths...)
}

func NewExecToolWithConfig(
	workingDir string,
	restrict bool,
	cfg *config.Config,
	allowPaths ...[]*regexp.Regexp,
) (*ExecTool, error) {
	denyPatterns := make([]*regexp.Regexp, 0)
	customAllowPatterns := make([]*regexp.Regexp, 0)
	var allowedPathPatterns []*regexp.Regexp
	allowRemote := true
	if len(allowPaths) > 0 {
		allowedPathPatterns = allowPaths[0]
	}

	if cfg != nil {
		execConfig := cfg.Tools.Exec
		enableDenyPatterns := execConfig.EnableDenyPatterns
		allowRemote = execConfig.AllowRemote
		if enableDenyPatterns {
			denyPatterns = append(denyPatterns, defaultDenyPatterns...)
			if runtime.GOOS == "windows" {
				denyPatterns = append(denyPatterns, windowsDenyPatterns...)
			}
			if len(execConfig.CustomDenyPatterns) > 0 {
				logger.InfoCF("tools", "using custom deny patterns", map[string]any{
					"patterns": execConfig.CustomDenyPatterns,
				})
				for _, pattern := range execConfig.CustomDenyPatterns {
					re, err := regexp.Compile(pattern)
					if err != nil {
						return nil, fmt.Errorf("invalid custom deny pattern %q: %w", pattern, err)
					}
					denyPatterns = append(denyPatterns, re)
				}
			}
		} else {
			// If deny patterns are disabled, we won't add any patterns, allowing all commands.
			logger.WarnCF("tools", "deny patterns are disabled, all commands will be allowed", nil)
		}
		for _, pattern := range execConfig.CustomAllowPatterns {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("invalid custom allow pattern %q: %w", pattern, err)
			}
			customAllowPatterns = append(customAllowPatterns, re)
		}
	} else {
		denyPatterns = append(denyPatterns, defaultDenyPatterns...)
		if runtime.GOOS == "windows" {
			denyPatterns = append(denyPatterns, windowsDenyPatterns...)
		}
	}

	var timeout time.Duration
	if cfg != nil && cfg.Tools.Exec.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Tools.Exec.TimeoutSeconds) * time.Second
	}

	maxOutputChars := config.DefaultExecMaxOutputChars
	maxInlineResultChars := 0
	if cfg != nil {
		maxOutputChars = cfg.Tools.Exec.GetMaxOutputChars()
		maxInlineResultChars = cfg.Tools.GetMaxInlineResultChars()
	}

	return &ExecTool{
		workingDir:           workingDir,
		timeout:              timeout,
		denyPatterns:         denyPatterns,
		allowPatterns:        nil,
		customAllowPatterns:  customAllowPatterns,
		allowedPathPatterns:  allowedPathPatterns,
		restrictToWorkspace:  restrict,
		allowRemote:          allowRemote,
		maxOutputChars:       maxOutputChars,
		maxInlineResultChars: maxInlineResultChars,
		sessionManager:       getSessionManager(),
	}, nil
}

func (t *ExecTool) Name() string {
	return "exec"
}

// execDescription only depends on runtime.GOOS, so build it once instead of on every call.
var execDescription = buildExecDescription()

func (t *ExecTool) Description() string {
	return execDescription
}

func buildExecDescription() string {
	shell := "sh"
	chainGuidance := "If commands depend on each other, chain them with && so a failure stops the sequence."
	if runtime.GOOS == "windows" {
		shell = "PowerShell"
		chainGuidance = "PowerShell does not support &&; use cmd1; if ($?) { cmd2 } when cmd2 depends on cmd1 succeeding."
	}

	return fmt.Sprintf(`Executes shell commands with PicoClaw's configured safety guards.

Be aware: OS: %s, Shell: %s

All commands run in the configured working directory by default. Use the cwd parameter to select another directory; do not use cd to change directories inside a command.

IMPORTANT: Use this tool for terminal operations such as git, go, npm, and docker. For listing, searching, reading, or editing files, use PicoClaw's filesystem tools (list_dir, search_files, read_file, edit_file, append_file, and write_file) when available.

Before a command explicitly creates files or directories, use list_dir when available to verify that the parent exists and is the intended location. Always quote paths containing spaces. Command output is returned by the tool.

Usage notes:
- action is required; command is required for action=run.
- The optional timeout parameter is in seconds and overrides the configured timeout for foreground commands; 0 disables the timeout. It cannot be combined with background=true. Foreground output longer than the configured limit (default 50,000 chars) is truncated to the first 40%% and last 60%% around an omission notice; background session output is limited to 1 MB.
- Use background=true for long-running commands. The result includes a sessionId. Use action=list to list sessions; use poll, wait, read, write, send-keys, or kill with sessionId to manage one. poll returns immediately with a short output preview. read is non-destructive and accepts offset/limit byte ranges; by default it returns the most recent output. wait returns a partial output tail when its window expires. kill returns the final tail and keeps the session readable for 30 minutes. To wait for a session to finish use wait instead of polling in a loop. write sends stdin; send-keys sends keys to a PTY. Set notify=true for a job that ends so its completion arrives as a follow-up message; after two silent poll/wait calls the tool enables that notification automatically.
- Combine background=true with pty=true for interactive sessions when supported. PTY is not supported on Windows.
- Run independent commands in separate tool calls. %s Use semicolons only when later commands may run even if an earlier one fails. Do not use newlines to separate commands; newlines inside quoted strings are fine.`, runtime.GOOS, shell, chainGuidance)
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
				"type":        "string",
				"description": "Run in background immediately",
			},
			"pty": map[string]any{
				"type":        "string",
				"description": "Run in a pseudo-terminal (PTY) when available",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Working directory for the command",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in seconds for foreground commands; overrides the configured timeout, and 0 disables the timeout. Not supported with background=true. For wait: maximum seconds to block (default and cap: the configured timeout).",
			},
			"notify": map[string]any{
				"type":        "boolean",
				"description": "With background=true, send a completion message when the process exits. Use it for jobs with a defined end; do not poll or wait in a loop.",
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

func boolArg(args map[string]any, key string) bool {
	switch v := args[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	action, _ := args["action"].(string)
	if action == "" {
		return ErrorResult("action is required")
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args)
	case "list":
		return t.executeList()
	case "poll":
		return t.executePoll(args)
	case "wait":
		return t.executeWait(ctx, args)
	case "read":
		return t.executeRead(args)
	case "write":
		return t.executeWrite(args)
	case "kill":
		return t.executeKill(args)
	case "send-keys":
		return t.executeSendKeys(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

// ExecuteAsync installs the completion callback for background runs. The
// callback is stored even when notify is false so repeated silent poll/wait
// calls can promote the session to notify-on-complete later.
func (t *ExecTool) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	result := t.Execute(ctx, args)
	if result == nil || result.IsError || cb == nil {
		return result
	}
	if action, _ := args["action"].(string); action != "run" || !boolArg(args, "background") {
		return result
	}

	var resp ExecResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &resp); err != nil || resp.SessionID == "" {
		return result
	}
	session, err := t.sessionManager.Get(resp.SessionID)
	if err != nil {
		return result
	}
	session.SetCompletion(func() {
		cb(context.Background(), t.completionResult(session))
	})
	if boolArg(args, "notify") && session.StartNotify() {
		// Only advertise the notification after the watcher is armed. Direct
		// Execute calls have no callback, so they must not promise one.
		resp.Note = notifyEnabledNote
		if data, err := json.Marshal(resp); err == nil {
			result.ForLLM = string(data)
		}
	}
	return result
}

func (t *ExecTool) completionResult(session *ProcessSession) *ToolResult {
	status := session.GetStatus()
	output := session.Tail(completionOutputChars)
	msg := fmt.Sprintf(
		"Background command completed.\nsessionId: %s\ncommand: %s\nstatus: %s\nexitCode: %d",
		session.ID, session.Command, status, session.GetExitCode(),
	)
	if output != "" {
		msg += "\n\nOutput tail:\n" + output
	}
	return NewToolResult(msg)
}

func (t *ExecTool) executeRun(ctx context.Context, args map[string]any) *ToolResult {
	command, ok := args["command"].(string)
	if !ok {
		return ErrorResult("command is required")
	}

	// GHSA-pv8c-p6jf-3fpp: block exec from remote channels (e.g. Telegram webhooks)
	// unless explicitly opted-in via config. Fail-closed: empty channel = blocked.
	if !t.allowRemote {
		channel := ToolChannel(ctx)
		if channel == "" {
			channel, _ = args["__channel"].(string)
		}
		channel = strings.TrimSpace(channel)
		if channel == "" || !constants.IsInternalChannel(channel) {
			return ErrorResult("exec is restricted to internal channels")
		}
	}

	isPty := boolArg(args, "pty")
	isBackground := boolArg(args, "background")
	notify := boolArg(args, "notify")
	timeout := t.timeout
	if raw, ok := args["timeout"]; ok {
		const maxTimeoutSeconds = (1<<63 - 1) / int64(time.Second)
		// JSON tool arguments always decode numbers as float64.
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 0 || value > float64(maxTimeoutSeconds) {
			return ErrorResult("timeout must be a non-negative integer number of seconds")
		}
		seconds := int64(value)
		// timeout=0 means "no timeout", which is already how background sessions behave.
		if isBackground && seconds != 0 {
			return ErrorResult("timeout is only supported for foreground commands")
		}
		timeout = time.Duration(seconds) * time.Second
	}

	if isPty {
		if runtime.GOOS == "windows" {
			return ErrorResult("PTY is not supported on Windows. Use background=true without pty.")
		}
	}

	if notify && !isBackground {
		return ErrorResult("notify is only supported with background=true")
	}

	cwd := t.workingDir
	if wd, ok := args["cwd"].(string); ok && wd != "" {
		if t.restrictToWorkspace && t.workingDir != "" {
			resolvedWD, err := fstools.ValidatePathWithAllowPaths(wd, t.workingDir, true, t.allowedPathPatterns)
			if err != nil {
				return ErrorResult("Command blocked by safety guard (" + err.Error() + ")")
			}
			cwd = resolvedWD
		} else {
			cwd = wd
		}
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err == nil {
			cwd = wd
		}
	}

	if guardError := t.guardCommand(command, cwd); guardError != "" {
		return ErrorResult(guardError)
	}

	// Re-resolve symlinks immediately before execution to shrink the TOCTOU window
	// between validation and cmd.Dir assignment.
	if t.restrictToWorkspace && t.workingDir != "" && cwd != t.workingDir {
		resolved, err := filepath.EvalSymlinks(cwd)
		if err != nil {
			return ErrorResult(fmt.Sprintf("Command blocked by safety guard (path resolution failed: %v)", err))
		}
		if fstools.IsAllowedPath(resolved, t.allowedPathPatterns) {
			cwd = resolved
		} else {
			absWorkspace, _ := filepath.Abs(t.workingDir)
			wsResolved, _ := filepath.EvalSymlinks(absWorkspace)
			if wsResolved == "" {
				wsResolved = absWorkspace
			}
			rel, err := filepath.Rel(wsResolved, resolved)
			if err != nil || !filepath.IsLocal(rel) {
				return ErrorResult("Command blocked by safety guard (working directory escaped workspace)")
			}
			cwd = resolved
		}
	}

	if isBackground {
		return t.runBackground(ctx, command, cwd, isPty)
	}

	return t.runSync(ctx, command, cwd, timeout)
}

func (t *ExecTool) runSync(ctx context.Context, command, cwd string, timeout time.Duration) *ToolResult {
	// timeout == 0 means no timeout
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	prepareCommandForTermination(cmd)

	maxOutputChars := t.maxOutputChars
	if maxOutputChars <= 0 {
		maxOutputChars = config.DefaultExecMaxOutputChars
	}
	// The inline view also respects the global tool-result cap. The artifact
	// keeps the full stream, so a smaller in-memory window does not lose the
	// omitted middle.
	if t.maxInlineResultChars > 0 && t.maxInlineResultChars < maxOutputChars {
		maxOutputChars = t.maxInlineResultChars
	}
	collector := newBoundedOutput(maxOutputChars, t.outputSpillDir())

	// stdout and stderr share one pipe so the result preserves the order in
	// which the child wrote them and a large stderr stream cannot block a
	// separate pipe reader.
	outputReader, outputWriter, pipeErr := os.Pipe()
	if pipeErr != nil {
		return ErrorResult(fmt.Sprintf("failed to create output pipe: %v", pipeErr))
	}
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter

	// Route shell execution through the shared isolation entry point so exec tool
	// subprocesses receive the same isolation policy as other integrations.
	if err := isolation.Start(cmd); err != nil {
		_ = outputWriter.Close()
		_ = outputReader.Close()
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	// The child inherited its own copy of the write end. Release the parent's
	// copy so the reader observes EOF when the child exits.
	_ = outputWriter.Close()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		_, _ = io.Copy(collector, outputReader)
		_ = outputReader.Close()
	}()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("shell", "cmd.Wait goroutine panic recovered",
					map[string]any{
						"panic": fmt.Sprintf("%v", r),
						"stack": string(debug.Stack()),
					})
				done <- fmt.Errorf("panic in cmd.Wait: %v", r)
			}
		}()
		done <- cmd.Wait()
	}()

	var err error
	select {
	case err = <-done:
	case <-cmdCtx.Done():
		_ = terminateProcessTree(cmd)
		select {
		case err = <-done:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			err = <-done
		}
	}

	// Drain the merged stream. A surviving grandchild can keep the write end
	// open after the direct child exits, so bound the wait instead of hanging
	// the tool call.
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		_ = outputReader.Close()
		<-readDone
	}

	output, truncated := collector.Render()
	artifactPath := collector.SpillPath()
	spillCapped := collector.SpillCapped()
	_ = collector.Close()

	if err != nil {
		if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			msg := fmt.Sprintf("Command timed out after %v", timeout)
			if output != "" {
				msg += "\n\nPartial output before timeout:\n" + output
			}
			return newExecResult(
				msg, true, fmt.Errorf("command timeout: %w", err), truncated, artifactPath, spillCapped,
			)
		}

		// Extract detailed exit information
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode := exitErr.ExitCode()
			output += fmt.Sprintf("\n\n[Command exited with code %d]", exitCode)

			// Add signal information if killed by signal (Unix)
			if exitCode == -1 {
				output += " (killed by signal)"
			}
		} else {
			output += fmt.Sprintf("\n\n[Command failed: %v]", err)
		}
	}

	if output == "" {
		output = "(no output)"
	}

	return newExecResult(output, err != nil, nil, truncated, artifactPath, spillCapped)
}

func newExecResult(
	forLLM string,
	isError bool,
	err error,
	truncated bool,
	artifactPath string,
	spillCapped bool,
) *ToolResult {
	result := &ToolResult{
		ForLLM:  forLLM,
		ForUser: forLLM,
		IsError: isError,
		Err:     err,
	}
	if truncated {
		// The tool already reduced the output to a bounded view. Prevent the
		// generic large-result spill from persisting that view as if it were
		// the full payload.
		result.LargeResultSpilled = true
	}
	if artifactPath != "" {
		result.ArtifactTags = append(result.ArtifactTags, "[file:"+artifactPath+"]")
	}
	if spillCapped {
		note := fmt.Sprintf(
			"\n\n[Full output artifact capped at %s bytes]",
			formatCount64(maxOutputSpillBytes),
		)
		result.ForLLM += note
		result.ForUser += note
	}
	return result
}

func (t *ExecTool) outputSpillDir() string {
	if t.workingDir == "" {
		return ""
	}
	return filepath.Join(t.workingDir, ".artifacts", "tool")
}

func (t *ExecTool) runBackground(ctx context.Context, command, cwd string, ptyEnabled bool) *ToolResult {
	sessionID := generateSessionID()
	session := &ProcessSession{
		ID:         sessionID,
		Command:    command,
		PTY:        ptyEnabled,
		Background: true,
		StartTime:  time.Now().Unix(),
		Status:     "running",
		ptyKeyMode: PtyKeyModeCSI,
		// Initialize the output buffer before the session is published to the
		// manager: readers (session_read/list_sessions) must never observe a
		// session whose buffer is still nil.
		outputBuffer: &bytes.Buffer{},
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	prepareCommandForTermination(cmd)

	var outputReader io.ReadCloser
	var outputWriter *os.File
	var stdinWriter io.WriteCloser
	var ptySlave *os.File

	if ptyEnabled {
		ptmx, tty, err := pty.Open()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to create PTY: %v", err))
		}

		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		// For PTY, we need Setsid to create a new session.
		// Note: Setsid and Setpgid conflict, so we must replace SysProcAttr entirely.
		setSysProcAttrForPty(cmd)

		session.ptyMaster = ptmx
		ptySlave = tty
	} else {
		var err error
		outputReader, outputWriter, err = os.Pipe()
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to create output pipe: %v", err))
		}
		cmd.Stdout = outputWriter
		cmd.Stderr = outputWriter
		stdinWriter, err = cmd.StdinPipe()
		if err != nil {
			_ = outputReader.Close()
			_ = outputWriter.Close()
			return ErrorResult(fmt.Sprintf("failed to create stdin pipe: %v", err))
		}
		session.stdoutPipe = outputReader
		session.stdinWriter = stdinWriter
	}

	// Background sessions use the same startup path so isolation stays consistent
	// with synchronous exec runs.
	if err := isolation.Start(cmd); err != nil {
		if outputReader != nil {
			_ = outputReader.Close()
		}
		if outputWriter != nil {
			_ = outputWriter.Close()
		}
		if ptySlave != nil {
			_ = ptySlave.Close()
		}
		if session.ptyMaster != nil {
			_ = session.ptyMaster.Close()
		}
		return ErrorResult(fmt.Sprintf("failed to start command: %v", err))
	}

	// The child inherited its own copies of the pipe/PTY write ends. The parent
	// must release its copy now, otherwise the readers never observe EOF/EIO
	// when the child exits and the goroutines would block forever.
	if outputWriter != nil {
		_ = outputWriter.Close()
	}
	if ptySlave != nil {
		_ = ptySlave.Close()
	}

	session.PID = cmd.Process.Pid
	t.sessionManager.Add(session)

	// PTY mode: read from ptyMaster and wait for process
	// Note: On Linux, closing ptyMaster doesn't interrupt blocking Read() calls,
	// so we need cmd.Wait() in a separate goroutine to detect process exit.
	if session.PTY && session.ptyMaster != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("shell", "PTY cmd.Wait goroutine panic recovered",
						map[string]any{
							"panic": fmt.Sprintf("%v", r),
							"stack": string(debug.Stack()),
						})
					session.finish(-1)
				}
			}()
			cmd.Wait() // Wait for process to exit
			exitCode := -1
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			session.finish(exitCode)
		}()

		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("shell", "PTY read goroutine panic recovered",
						map[string]any{
							"panic": fmt.Sprintf("%v", r),
							"stack": string(debug.Stack()),
						})
				}
			}()
			buf := make([]byte, 4096)
			for {
				n, err := session.ptyMaster.Read(buf)
				if n > 0 {
					raw := string(buf[:n])
					if mode := detectPtyKeyMode(raw); mode != PtyKeyModeNotFound && mode != session.GetPtyKeyMode() {
						session.SetPtyKeyMode(mode)
					}

					session.appendOutput(buf[:n])
				}
				if err != nil {
					break
				}
			}
			// The child side is gone (EOF/EIO) or the session was killed:
			// release the master descriptor so no fd survives the session.
			_ = session.ptyMaster.Close()
		}()
	} else {
		// Non-PTY mode: one goroutine reads the merged stdout/stderr pipe.
		// When process exits, OS closes the pipe write end → Read() returns EOF.
		go func() {
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("shell", "pipe read goroutine panic recovered",
						map[string]any{
							"panic": fmt.Sprintf("%v", r),
							"stack": string(debug.Stack()),
						})
				}
			}()
			buf := make([]byte, 4096)

			for {
				n, err := outputReader.Read(buf)
				if n > 0 {
					session.appendOutput(buf[:n])
				}
				if err != nil {
					break
				}
			}

			_ = outputReader.Close()
			if stdinWriter != nil {
				_ = stdinWriter.Close()
			}
			cmd.Wait()

			exitCode := -1
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			session.finish(exitCode)
		}()
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "running",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s started", sessionID),
		IsError: false,
	}
}

func (t *ExecTool) executeList() *ToolResult {
	sessions := t.sessionManager.List()
	resp := ExecResponse{
		Sessions: sessions,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("%d active sessions", len(sessions)),
		IsError: false,
	}
}

func (t *ExecTool) executePoll(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	resp := ExecResponse{
		SessionID:    sessionID,
		Status:       session.GetStatus(),
		ExitCode:     session.GetExitCode(),
		Output:       session.Tail(pollOutputChars),
		TotalBytes:   session.TotalBytes(),
		DroppedBytes: session.DroppedBytes(),
	}
	if !session.IsDone() && session.noteCheck() >= idleCheckLimit {
		resp.Note = pollIdleNote
		if session.StartNotify() || session.NotifyEnabled() {
			resp.Note += " " + autoNotifyNote
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: false,
	}
}

const (
	// idleCheckLimit is how many consecutive poll/wait calls on a silent running
	// session it takes to tell the model to stop checking.
	idleCheckLimit = 2
	pollIdleNote   = "Still running with no new output since your last checks. poll returns " +
		"immediately, so repeating it will not help: use action=wait (with timeout) to block " +
		"until it exits, or do other work."
	waitIdleNote = "No new output after repeated waits. If that is not expected, read the " +
		"output or kill the session instead of waiting again."
	// defaultWaitMax caps one wait when no exec timeout is configured.
	defaultWaitMax = 60 * time.Second
	// completionOutputChars bounds the output tail in a completion notice.
	completionOutputChars = 1000
	pollOutputChars       = 1000
	waitOutputChars       = 1000
	killOutputChars       = 1000

	notifyEnabledNote = "You will be notified when this session exits; do not poll or wait in a loop."
	autoNotifyNote    = "A completion notification is enabled; the result will arrive as a follow-up message."
)

// executeWait blocks until the session exits, the timeout elapses, or ctx is
// canceled. A wait that times out is not a failure: the response says so, since
// models otherwise misread it and re-issue the same call.
func (t *ExecTool) executeWait(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	maxWait := t.timeout
	if maxWait <= 0 {
		maxWait = defaultWaitMax
	}
	window, clampNote := maxWait, ""
	if raw, ok := args["timeout"]; ok {
		// JSON tool arguments always decode numbers as float64.
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 1 {
			return ErrorResult("timeout must be a positive integer number of seconds")
		}
		if requested := time.Duration(value) * time.Second; requested <= maxWait {
			window = requested
		} else {
			clampNote = fmt.Sprintf("Requested wait of %ds was clamped to the configured limit of %ds. ",
				int64(value), int64(maxWait/time.Second))
		}
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	// ponytail: polls IsDone every 100ms instead of a done channel; a queued
	// steering message does not release the wait, only ctx cancellation does.
	deadline := time.NewTimer(window)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	timedOut := false
	for !timedOut && !session.IsDone() {
		select {
		case <-ctx.Done():
			return ErrorResult(fmt.Sprintf("wait interrupted: %v", ctx.Err()))
		case <-deadline.C:
			timedOut = true
		case <-tick.C:
		}
	}

	resp := ExecResponse{
		SessionID:    sessionID,
		Status:       session.GetStatus(),
		ExitCode:     session.GetExitCode(),
		TotalBytes:   session.TotalBytes(),
		DroppedBytes: session.DroppedBytes(),
	}
	if session.IsDone() {
		resp.Output = session.Read()
	} else {
		resp.Output = session.Tail(waitOutputChars)
		resp.Note = clampNote + fmt.Sprintf(
			"Wait window of %ds elapsed. The process is still running; this is not an error.",
			int64(window/time.Second))
		if session.noteCheck() >= idleCheckLimit {
			resp.Note += " " + waitIdleNote
			if session.StartNotify() || session.NotifyEnabled() {
				resp.Note += " " + autoNotifyNote
			}
		}
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{ForLLM: string(data)}
}

func (t *ExecTool) executeRead(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	offset := int64(0)
	offsetProvided := false
	if raw, ok := args["offset"]; ok {
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 0 {
			return ErrorResult("offset must be a non-negative integer number of bytes")
		}
		offset = int64(value)
		offsetProvided = true
	}

	limit := int64(t.maxOutputChars)
	if limit <= 0 {
		limit = config.DefaultExecMaxOutputChars
	}
	if raw, ok := args["limit"]; ok {
		value, isNumber := raw.(float64)
		if !isNumber || value != math.Trunc(value) || value < 1 {
			return ErrorResult("limit must be a positive integer number of bytes")
		}
		limit = int64(value)
	}
	if !offsetProvided {
		if total := session.TotalBytes(); total > limit {
			offset = total - limit
		}
	}

	page := session.ReadRange(offset, limit)
	resp := ExecResponse{
		SessionID:    sessionID,
		Output:       page.Text,
		Status:       session.GetStatus(),
		TotalBytes:   page.TotalBytes,
		Offset:       page.Offset,
		NextOffset:   page.NextOffset,
		HasMore:      page.HasMore,
		DroppedBytes: page.DroppedBytes,
		SkippedBytes: page.SkippedBytes,
	}
	if page.SkippedBytes > 0 {
		resp.Note = fmt.Sprintf(
			"Requested offset %d predates the retained output window; skipped %d bytes, starting at byte %d.",
			offset, page.SkippedBytes, page.Offset,
		)
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		IsError: false,
	}
}

func (t *ExecTool) executeWrite(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	data, ok := args["data"].(string)
	if !ok {
		return ErrorResult("data is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err = session.Write(data); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
		}
		return ErrorResult(fmt.Sprintf("failed to write to session: %v", err))
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    session.GetStatus(),
	}
	respData, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(respData),
		IsError: false,
	}
}

func (t *ExecTool) executeKill(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err = session.Kill(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to kill session: %v", err))
	}

	resp := ExecResponse{
		SessionID:    sessionID,
		Status:       session.GetStatus(),
		ExitCode:     session.GetExitCode(),
		Output:       session.Tail(killOutputChars),
		TotalBytes:   session.TotalBytes(),
		DroppedBytes: session.DroppedBytes(),
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s killed; output retained for 30 minutes", sessionID),
		IsError: false,
	}
}

// keyMap maps key names to their escape sequences.
var keyMap = map[string]string{
	"enter":     "\r",
	"return":    "\r",
	"tab":       "\t",
	"escape":    "\x1b",
	"esc":       "\x1b",
	"space":     " ",
	"backspace": "\x7f",
	"bspace":    "\x7f",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[1~",
	"end":       "\x1b[4~",
	"pageup":    "\x1b[5~",
	"pagedown":  "\x1b[6~",
	"pgup":      "\x1b[5~",
	"pgdn":      "\x1b[6~",
	"insert":    "\x1b[2~",
	"ic":        "\x1b[2~",
	"delete":    "\x1b[3~",
	"del":       "\x1b[3~",
	"dc":        "\x1b[3~",
	"btab":      "\x1b[Z",
	"f1":        "\x1bOP",
	"f2":        "\x1bOQ",
	"f3":        "\x1bOR",
	"f4":        "\x1bOS",
	"f5":        "\x1b[15~",
	"f6":        "\x1b[17~",
	"f7":        "\x1b[18~",
	"f8":        "\x1b[19~",
	"f9":        "\x1b[20~",
	"f10":       "\x1b[21~",
	"f11":       "\x1b[23~",
	"f12":       "\x1b[24~",
}

// ss3KeysMap maps key names to SS3 escape sequences
var ss3KeysMap = map[string]string{
	"up":    "\x1bOA",
	"down":  "\x1bOB",
	"right": "\x1bOC",
	"left":  "\x1bOD",
	"home":  "\x1bOH",
	"end":   "\x1bOF",
}

func detectPtyKeyMode(raw string) PtyKeyMode {
	const SMKX = "\x1b[?1h"
	const RMKX = "\x1b[?1l"

	lastSmkx := strings.LastIndex(raw, SMKX)
	lastRmkx := strings.LastIndex(raw, RMKX)

	if lastSmkx == -1 && lastRmkx == -1 {
		return PtyKeyModeNotFound
	}

	if lastSmkx > lastRmkx {
		return PtyKeyModeSS3
	}
	return PtyKeyModeCSI
}

// encodeKeyToken encodes a single key token into its escape sequence.
// Supports:
//   - Named keys: "enter", "tab", "up", "ctrl-c", "alt-x", etc.
//   - Ctrl modifier: "ctrl-c" or "c-c" (sends Ctrl+char)
//   - Alt modifier: "alt-x" or "m-x" (sends ESC+char)
func encodeKeyToken(token string, ptyKeyMode PtyKeyMode) (string, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return "", nil
	}

	// Handle ctrl-X format (c-x)
	if strings.HasPrefix(token, "c-") {
		char := token[2]
		if char >= 'a' && char <= 'z' {
			return string(rune(char) & 0x1f), nil // ctrl-a through ctrl-z
		}
		return "", fmt.Errorf("invalid ctrl key: %s", token)
	}

	// Handle ctrl-X format (ctrl-x)
	if strings.HasPrefix(token, "ctrl-") {
		char := token[5]
		if char >= 'a' && char <= 'z' {
			return string(rune(char) & 0x1f), nil
		}
		return "", fmt.Errorf("invalid ctrl key: %s", token)
	}

	// Handle alt-X format (m-x or alt-x)
	if strings.HasPrefix(token, "m-") || strings.HasPrefix(token, "alt-") {
		var char string
		if strings.HasPrefix(token, "m-") {
			char = token[2:]
		} else {
			char = token[4:]
		}
		if len(char) == 1 {
			return "\x1b" + char, nil
		}
		return "", fmt.Errorf("invalid alt key: %s", token)
	}

	// Handle shift modifier for special keys (shift-up, shift-down, etc.)
	if strings.HasPrefix(token, "s-") || strings.HasPrefix(token, "shift-") {
		var key string
		if strings.HasPrefix(token, "s-") {
			key = token[2:]
		} else {
			key = token[6:]
		}
		// Apply shift modifier: for single-char keys, return uppercase
		if seq, ok := keyMap[key]; ok {
			// For escape sequences, we can't easily add shift
			// For single-char keys (letters), return uppercase
			if len(seq) == 1 {
				return strings.ToUpper(seq), nil
			}
			return seq, nil
		}
		return "", fmt.Errorf("unknown key with shift: %s", key)
	}

	if ptyKeyMode == PtyKeyModeSS3 {
		if seq, ok := ss3KeysMap[token]; ok {
			return seq, nil
		}
	}

	if seq, ok := keyMap[token]; ok {
		return seq, nil
	}

	return "", fmt.Errorf("unknown key: %s (use write action for text input)", token)
}

// encodeKeySequence encodes a slice of key tokens into a single string.
func encodeKeySequence(tokens []string, ptyKeyMode PtyKeyMode) (string, error) {
	var result string
	for _, token := range tokens {
		seq, err := encodeKeyToken(token, ptyKeyMode)
		if err != nil {
			return "", err
		}
		result += seq
	}
	return result, nil
}

func (t *ExecTool) executeSendKeys(args map[string]any) *ToolResult {
	sessionID, ok := args["sessionId"].(string)
	if !ok {
		return ErrorResult("sessionId is required")
	}

	keysStr, ok := args["keys"].(string)
	if !ok {
		return ErrorResult("keys must be a string")
	}

	if keysStr == "" {
		return ErrorResult("keys cannot be empty")
	}

	// Parse comma-separated key names
	keyNames := strings.Split(keysStr, ",")
	var keys []string
	for _, k := range keyNames {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}

	if len(keys) == 0 {
		return ErrorResult("keys cannot be empty")
	}

	session, err := t.sessionManager.Get(sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return ErrorResult(fmt.Sprintf("session not found: %s", sessionID))
		}
		return ErrorResult(err.Error())
	}

	ptyKeyMode := session.GetPtyKeyMode()

	data, err := encodeKeySequence(keys, ptyKeyMode)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid key: %v", err))
	}

	if session.IsDone() {
		return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
	}

	if err = session.Write(data); err != nil {
		if errors.Is(err, ErrSessionDone) {
			return ErrorResult(fmt.Sprintf("process already exited with code %d", session.GetExitCode()))
		}
		return ErrorResult(fmt.Sprintf("failed to send keys: %v", err))
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "running",
		Output:    fmt.Sprintf("Sent keys: %v", keys),
	}
	respData, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(respData),
		IsError: false,
	}
}

// expandPowerShellEnvVars expands environment variable syntax used by both
// PowerShell ($env:VAR) and CMD (%VAR%) to their actual values.
func expandPowerShellEnvVars(cmd string) string {
	// Handle PowerShell style: $env:VAR and ${env:VAR}
	rePs := regexp.MustCompile(`\$\{?env:(\w+)\}?`)
	cmd = rePs.ReplaceAllStringFunc(cmd, func(match string) string {
		varName := rePs.FindStringSubmatch(match)[1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return match
	})

	// Handle CMD style: %VAR%
	reCmd := regexp.MustCompile(`%([^%]+)%`)
	return reCmd.ReplaceAllStringFunc(cmd, func(match string) string {
		varName := reCmd.FindStringSubmatch(match)[1]
		if val := os.Getenv(varName); val != "" {
			return val
		}
		return match
	})
}

func (t *ExecTool) commandMatchesAllowPattern(lower string) bool {
	for _, pattern := range t.allowPatterns {
		if pattern.MatchString(lower) {
			return true
		}
	}
	for _, pattern := range t.customAllowPatterns {
		if pattern.MatchString(lower) {
			return true
		}
	}
	return false
}

func (t *ExecTool) guardCommand(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	// Deny patterns always apply, even when a command matches a custom allow rule.
	// Custom allow rules can permit a command, but must not disable secret-safety
	// deny rules such as jq env access checks (#3079).
	for _, pattern := range t.denyPatterns {
		if pattern.MatchString(lower) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	if len(t.allowPatterns) > 0 {
		if !t.commandMatchesAllowPattern(lower) {
			return "Command blocked by safety guard (not in allowlist)"
		}
	}

	if t.restrictToWorkspace {
		// Block path traversal patterns including .../.../ variants
		if regexp.MustCompile(`\.\.(?:[\\/]\.\.)*[\\/]`).MatchString(cmd) {
			return "Command blocked by safety guard (path traversal detected)"
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}

		// Web URL schemes whose path components (starting with //) should be exempt
		// from workspace sandbox checks. file: is intentionally excluded so that
		// file:// URIs are still validated against the workspace boundary.
		webSchemes := []string{"http:", "https:", "ftp:", "ftps:", "sftp:", "ssh:", "git:"}

		// On Windows, expand ~ and PowerShell environment variables ($env:VAR) before path checking
		if runtime.GOOS == "windows" {
			// Expand PowerShell environment variables ($env:VAR and ${env:VAR})
			cmd = expandPowerShellEnvVars(cmd)
			// Also expand ~ for completeness
			if home, err := os.UserHomeDir(); err == nil {
				cmd = strings.ReplaceAll(cmd, "~", filepath.FromSlash(home))
			}
		}

		matchIndices := absolutePathPattern.FindAllStringIndex(cmd, -1)
		executableWords, parsedShell := shellExecutableWords(cmd)

		for _, loc := range matchIndices {
			raw := cmd[loc[0]:loc[1]]

			// Skip URL path components that look like they're from web URLs.
			// When a URL like "https://github.com" is parsed, the regex captures
			// "//github.com" as a match (the path portion after "https:").
			// Use the exact match position (loc[0]) so that duplicate //path substrings
			// in the same command are each evaluated at their own position.
			if strings.HasPrefix(raw, "//") && loc[0] > 0 {
				before := cmd[:loc[0]]
				isWebURL := false

				for _, scheme := range webSchemes {
					if strings.HasSuffix(before, scheme) {
						isWebURL = true
						break
					}
				}

				if isWebURL {
					continue
				}
			}

			// Skip scheme-less URL paths like "wttr.in/Beijing".
			// When a /path is immediately preceded by a token that looks
			// like a domain name and that token does NOT exist as a local
			// filesystem entry, treat the path as part of a URL and skip
			// workspace sandbox validation.
			//
			// The local-path-exists guard prevents symlink bypass: if
			// "foo.bar" exists as a local symlink or directory, the path
			// still undergoes full workspace validation (see #2965).
			if loc[0] > 0 && raw[0] == '/' {
				// Find the token immediately before the "/".
				j := loc[0] - 1
				for j >= 0 && !isShellTokenBoundary(cmd[j]) {
					j--
				}
				token := cmd[j+1 : loc[0]]
				if looksLikeDomain(token) && !localPathExists(cwd, token) {
					continue
				}
			}

			// A single regex match can contain several path tokens when shell
			// separators are not surrounded by whitespace ("2>/dev/null;echo b"),
			// so every token is checked instead of only the leading one.
			pathTexts := commandPathTextsFromMatch(cmd, loc[0], loc[1])
			searchFrom, _ := shellTokenBounds(cmd, loc[0])
			for _, pathText := range pathTexts {
				pathOffset := -1
				if idx := strings.Index(cmd[searchFrom:loc[1]], pathText); idx >= 0 {
					pathOffset = searchFrom + idx
					searchFrom = pathOffset + len(pathText)
				}

				p, err := commandPathAbs(pathText, cwdPath)
				if err != nil {
					continue
				}

				// Windows-specific: normalize paths to block ADS and extended-length paths
				if runtime.GOOS == "windows" {
					// Strip \\?\ prefix (extended-length path)
					p = strings.TrimPrefix(p, `\\?\`)
					// Strip NTFS alternate data streams (only if colon is not at position 1 = drive letter)
					if idx := strings.Index(p, ":"); idx > 1 {
						p = p[:idx]
					}
				}

				// Executables are resolved by the shell, not by the guard. A
				// workspace-local interpreter symlink (.venv/bin/python, nvm
				// shims, rustup proxies, ...) may legitimately point outside
				// the workspace, exactly like a bare command resolved through
				// PATH. Keep the symlink check for every non-executable operand.
				if pathOffset >= 0 && parsedShell &&
					isExecutableShellWord(executableWords, pathOffset) &&
					isPathWithin(cwdPath, p) {
					continue
				}

				// Check symlinks and junctions
				resolved, err := filepath.EvalSymlinks(p)
				if err == nil {
					p = resolved
				}

				if safePaths[p] {
					continue
				}
				if fstools.IsAllowedPath(p, t.allowedPathPatterns) {
					continue
				}

				rel, err := filepath.Rel(cwdPath, p)
				if err != nil {
					continue
				}

				if strings.HasPrefix(rel, "..") {
					return "Command blocked by safety guard (path outside working dir)"
				}
			}
		}
	}

	return ""
}

func commandPathAbs(pathText, cwdPath string) (string, error) {
	if filepath.IsAbs(pathText) {
		return filepath.Abs(pathText)
	}
	return filepath.Abs(filepath.Join(cwdPath, pathText))
}

func commandPathTextsFromMatch(cmd string, start, end int) []string {
	return splitShellPathMatch(commandPathTextFromMatchRaw(cmd, start, end))
}

// shellPathSeparators are bytes that end a path token in shell syntax.
const shellPathSeparators = ";|&<>`"

// splitShellPathMatch splits text that absolutePathPattern swallows because the
// regex only stops at whitespace or quotes. For "2>/dev/null;echo b" the match is
// "/dev/null;echo", which is not in safePaths and is therefore reported as a path
// outside the working directory (issue #435). Splitting on shell separators turns
// it into "/dev/null" and "echo", so the safe pseudo-device is recognized while
// the rest of the token is still validated: "/dev/null;cat</etc/shadow" also
// yields "/etc/shadow", which keeps the containment check effective.
//
// Trailing ",)]}:" is stripped from every segment, so a real file whose name ends
// in one of those characters (for example "/workspace/backup,") is checked as the
// name without it. Rare, and accepted in exchange for not blocking "(... /dev/null)".
//
// Windows-style matches (drive letter, backslashes) are left untouched because
// the same bytes can legitimately appear inside a Windows path.
func splitShellPathMatch(pathText string) []string {
	if !strings.HasPrefix(pathText, "/") {
		return []string{pathText}
	}

	segments := strings.FieldsFunc(pathText, func(r rune) bool {
		return strings.ContainsRune(shellPathSeparators, r)
	})
	paths := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment = strings.TrimRight(segment, ",)]}:"); segment != "" {
			paths = append(paths, segment)
		}
	}
	if len(paths) == 0 {
		return []string{pathText}
	}
	return paths
}

func commandPathTextFromMatchRaw(cmd string, start, end int) string {
	raw := cmd[start:end]
	if !strings.HasPrefix(raw, "/") || isUnixAbsolutePathMatchStart(cmd, start) {
		return raw
	}

	tokenStart, tokenEnd := shellTokenBounds(cmd, start)
	prefix := cmd[tokenStart:start]
	// For --flag=rel/path, validate the value. For ambiguous attached option
	// forms like -isystem/path, keep the slash-starting path conservative.
	if eq := strings.IndexByte(prefix, '='); eq >= 0 {
		return cmd[tokenStart+eq+1 : tokenEnd]
	}
	if strings.HasPrefix(prefix, "-") {
		return raw
	}
	return cmd[tokenStart:tokenEnd]
}

func shellTokenBounds(cmd string, idx int) (int, int) {
	start := idx
	for start > 0 && !isShellTokenBoundary(cmd[start-1]) {
		start--
	}
	end := idx
	for end < len(cmd) && !isShellTokenBoundary(cmd[end]) {
		end++
	}
	return start, end
}

// isUnixAbsolutePathMatchStart returns true when a regex match beginning with
// "/" is actually an absolute path token, not the separator inside a relative
// path such as "skills/foo.py".
func isUnixAbsolutePathMatchStart(cmd string, idx int) bool {
	if idx <= 0 {
		return true
	}

	prev := cmd[idx-1]
	if isShellTokenBoundary(prev) || prev == '=' || prev == ',' || prev == '(' || prev == '[' || prev == '{' {
		return true
	}

	j := idx - 1
	for j >= 0 && !isShellTokenBoundary(cmd[j]) {
		j--
	}
	prefix := cmd[j+1 : idx]

	return strings.HasPrefix(prefix, "-") && !strings.Contains(prefix, "=")
}

// isShellTokenBoundary returns true when b is a byte that separates
// tokens in a shell command (space, tab, colon, semicolon, pipe, etc.).
func isShellTokenBoundary(b byte) bool {
	switch b {
	case ' ', '\t', ':', ';', '|', '&', '<', '>', '\'', '"', '`', '\n', '\r':
		return true
	}
	return false
}

// looksLikeDomain returns true when s looks like a DNS domain name:
// it contains at least one dot, starts with an alphanumeric character,
// and does not end with a common file extension.
func looksLikeDomain(s string) bool {
	if len(s) < 3 || !strings.ContainsRune(s, '.') {
		return false
	}
	first := s[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	// Exclude tokens ending with common file/programming extensions,
	// e.g. "script.py", "main.go", "app.exe".
	if idx := strings.LastIndexByte(s, '.'); idx >= 0 {
		ext := strings.ToLower(s[idx+1:])
		if commonFileExtension(ext) {
			return false
		}
	}
	return true
}

// commonFileExtension returns true when ext is a file extension that
// strongly indicates a local file rather than a domain TLD.
func commonFileExtension(ext string) bool {
	switch ext {
	case "py", "js", "ts", "tsx", "jsx", "go", "rs", "rb", "php",
		"java", "c", "cpp", "h", "hpp", "cs", "swift", "kt", "scala",
		"sh", "bash", "zsh", "fish", "ps1", "bat", "cmd",
		"txt", "md", "rst", "log", "json", "yaml", "yml", "toml",
		"xml", "html", "css", "scss", "ini", "cfg", "conf", "env",
		"exe", "dll", "so", "dylib", "lib", "a", "o", "obj",
		"zip", "tar", "gz", "bz2", "xz", "7z", "rar",
		"png", "jpg", "jpeg", "gif", "svg", "ico", "bmp", "webp",
		"mp3", "mp4", "wav", "avi", "mov", "mkv", "flac",
		"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx",
		"pub", "pem", "key", "crt", "cer", "p12", "pfx",
		"bak", "tmp", "swp", "lock",
		"ttf", "otf", "woff", "woff2", "eot",
		"deb", "rpm", "apk", "msi", "dmg",
		"sql", "sqlite", "db":
		return true
	}
	return false
}

// localPathExists returns true when the given token resolves to an
// existing filesystem entry relative to cwd.
func localPathExists(cwd, token string) bool {
	info, err := os.Lstat(filepath.Join(cwd, token))
	return err == nil && info != nil
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

func (t *ExecTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetAllowPatterns(patterns []string) error {
	t.allowPatterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid allow pattern %q: %w", p, err)
		}
		t.allowPatterns = append(t.allowPatterns, re)
	}
	return nil
}
