package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// withNote appends one model-facing note to another without leaving a double
// space when the first one is empty.
func withNote(base, add string) string {
	if base == "" {
		return add
	}
	return base + " " + add
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

// shellCommand returns the OS shell and its arguments for one exec run.
func shellCommand(command string) (string, []string, error) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", command}, nil
	}
	shellPath, err := findShellExecutable()
	if err != nil {
		return "", nil, fmt.Errorf("failed to locate shell: %w", err)
	}
	return shellPath, []string{"-c", command}, nil
}

// cwdProbeFile receives the final working directory from a wrapped foreground
// command. A temp file keeps the probe out of stdout, so output truncation,
// exit-status parsing and artifact tests see exactly the command's output.
func newCwdProbeFile() (string, error) {
	file, err := os.CreateTemp("", "picoclaw-cwd-*.txt")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// shellQuote single-quotes a path for the POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// wrapCommandForCwd appends a probe that writes `pwd -P` after the command and
// preserves its exit status. The wrapper runs in the same shell, so `cd` inside
// the command is observed. Commands that cannot safely take a suffix (Windows
// PowerShell and a trailing backslash continuation) are left unchanged.
func wrapCommandForCwd(command, probePath string) (string, bool) {
	if runtime.GOOS == "windows" || probePath == "" {
		return command, false
	}
	if strings.HasSuffix(strings.TrimRight(command, " \t\r\n"), "\\") {
		return command, false
	}
	wrapped := command + "\n__picoclaw_exec_status=$?\n" +
		"command pwd -P > " + shellQuote(probePath) + " 2>/dev/null\n" +
		"exit $__picoclaw_exec_status"
	return wrapped, true
}

// readObservedCwd reads the probe file written by the wrapper. A missing or
// empty file means the command exited through exec/exit or was killed before
// the probe ran; the previous remembered cwd then stays in place.
func readObservedCwd(probePath string) string {
	if probePath == "" {
		return ""
	}
	data, err := os.ReadFile(probePath)
	if err != nil {
		return ""
	}
	cwd := strings.TrimSpace(string(data))
	if cwd == "" || len(cwd) > 4096 {
		return ""
	}
	return cwd
}

// prepareExecCommand applies the settings shared by foreground, background and
// PTY executions.
func (t *ExecTool) prepareExecCommand(cmd *exec.Cmd, cwd string, env []string) {
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = env
	prepareCommandForTermination(cmd)
}

func (t *ExecTool) runSync(
	ctx context.Context,
	command, cwd string,
	timeout time.Duration,
	analysis shellguard.Analysis,
) *ToolResult {
	start := time.Now()
	// timeout == 0 means no timeout
	var cmdCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		cmdCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	probePath, probeErr := newCwdProbeFile()
	if probeErr != nil {
		probePath = ""
	}
	defer func() { _ = os.Remove(probePath) }()
	shellCommandText, observeCwd := wrapCommandForCwd(command, probePath)
	shellPath, shellArgs, shellErr := shellCommand(shellCommandText)
	if shellErr != nil {
		return ErrorResult(shellErr.Error())
	}
	// Do not use exec.CommandContext here: its automatic Process.Kill can reap
	// the shell before terminateProcessTree enumerates descendants, losing the
	// ancestry link to children that changed session (setsid). The timeout is
	// handled explicitly below so the process tree is still observable.
	cmd := exec.Command(shellPath, shellArgs...)
	t.prepareExecCommand(cmd, cwd, t.runEnv(timeout))

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
	terminatedByContext := false
	select {
	case err = <-done:
	case <-cmdCtx.Done():
		terminatedByContext = true
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

	view := collector.Render()
	output := view.Text
	artifactPath := collector.SpillPath()
	spillCapped := collector.SpillCapped()
	_ = collector.Close()

	observedCwd := ""
	if observeCwd {
		observedCwd = readObservedCwd(probePath)
	}

	// Keep the raw stdout/stderr intact for the model and put the structured
	// status in a one-line header instead. Wrapping the output in JSON would
	// escape every newline, tab and quote, which makes diffs, logs and source
	// snippets harder to read and costs context.
	exitCode := (*int)(nil)
	timedOut := false
	errorMessage := ""
	forUser := output
	resultErr := err

	switch {
	case terminatedByContext && errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
		timedOut = true
		errorMessage = fmt.Sprintf("command timed out after %v", timeout)
		forUser = fmt.Sprintf("Command timed out after %v", timeout)
		if output != "" {
			forUser += "\n\nPartial output before timeout:\n" + output
		}
		if err != nil {
			resultErr = fmt.Errorf("command timeout: %w", err)
		} else {
			resultErr = fmt.Errorf("command timed out after %v", timeout)
		}
	case terminatedByContext && errors.Is(cmdCtx.Err(), context.Canceled):
		errorMessage = "command canceled"
		forUser = output + "\n\n[Command canceled]"
		if err != nil {
			resultErr = fmt.Errorf("command canceled: %w", err)
		} else {
			resultErr = errors.New("command canceled")
		}
	case err == nil:
		code := 0
		exitCode = &code
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			if code >= 0 {
				exitCode = &code
				forUser = output + fmt.Sprintf("\n\n[Command exited with code %d]", code)
			} else {
				// A process killed by a signal has no meaningful exit status.
				// Surface the wait error instead of a synthetic -1.
				errorMessage = exitErr.Error()
				forUser = output + "\n\n[Command exited with code -1] (killed by signal)"
			}
		} else {
			errorMessage = err.Error()
			forUser = output + fmt.Sprintf("\n\n[Command failed: %v]", err)
		}
	}

	if output == "" {
		output = "(no output)"
	}
	if forUser == "" {
		forUser = "(no output)"
	}

	if observedCwd != "" && t.acceptObservedCwd(observedCwd) {
		t.rememberCwd(ctx, observedCwd)
	}

	exitNote := ""
	if exitCode != nil {
		exitNote = informativeExitNote(command, analysis, *exitCode)
	}
	meta := execRunMeta{
		ExitCode:     exitCode,
		TimedOut:     timedOut,
		Truncated:    view.Truncated,
		TotalBytes:   view.TotalBytes,
		OmittedBytes: view.OmittedBytes,
		ArtifactPath: artifactPath,
		DurationMs:   time.Since(start).Milliseconds(),
		Error:        errorMessage,
		Cwd:          observedCwd,
		ExitNote:     exitNote,
	}
	forLLM := formatExecRunMeta(meta) + "\n" + output
	if timedOut {
		// A timeout is the moment we know the command outlived a foreground
		// call, so point the model at tracked sessions instead of letting it
		// guess from the exit status alone.
		forLLM += "\n\n" + timeoutBackgroundGuidance
	}

	return newExecResult(forLLM, forUser, resultErr != nil, resultErr, artifactPath, spillCapped)
}

// execRunMeta is the model-facing status header for a foreground exec run.
// The header stays outside the output body so shell output is not JSON-escaped.
type execRunMeta struct {
	ExitCode     *int   `json:"exitCode"`
	TimedOut     bool   `json:"timedOut"`
	Truncated    bool   `json:"truncated"`
	TotalBytes   int64  `json:"totalBytes,omitempty"`
	OmittedBytes int64  `json:"omittedBytes,omitempty"`
	ArtifactPath string `json:"artifactPath,omitempty"`
	DurationMs   int64  `json:"durationMs"`
	Error        string `json:"error,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	ExitNote     string `json:"exitNote,omitempty"`
}

const execRunMetaPrefix = "[exec] "

// exitCodeNotes explains non-zero statuses that are commonly normal. The map
// is deliberately small: an unknown exit code must stay a real error hint.
var exitCodeNotes = map[string]map[int]string{
	"grep":  {1: "grep exit 1 means no matches (not an error)"},
	"egrep": {1: "grep exit 1 means no matches (not an error)"},
	"fgrep": {1: "grep exit 1 means no matches (not an error)"},
	"rg":    {1: "ripgrep exit 1 means no matches (not an error)"},
	"ag":    {1: "exit 1 means no matches (not an error)"},
	"ack":   {1: "exit 1 means no matches (not an error)"},
	"diff":  {1: "diff exit 1 means files differ (not an error)"},
	"colordiff": {
		1: "diff exit 1 means files differ (not an error)",
	},
	"test": {1: "test exit 1 means the condition is false (not an error)"},
	"[":    {1: "test exit 1 means the condition is false (not an error)"},
	"git":  {1: "git exit 1 is often normal, for example git diff with changes"},
}

// signalExitNotes explains the shell's 128+signal statuses that models most
// often misread. The table is curated, not exhaustive: an unknown code stays a
// real error hint. 130 (SIGINT) is omitted because an interactive interrupt is
// usually expected.
var signalExitNotes = map[int]string{
	129: "SIGHUP (terminal closed or hangup)",
	132: "SIGILL (illegal instruction)",
	134: "SIGABRT (abort)",
	135: "SIGBUS (bus error)",
	136: "SIGFPE (arithmetic error)",
	137: "SIGKILL (often the OOM killer or kill -9)",
	139: "SIGSEGV (segmentation fault)",
	141: "SIGPIPE (wrote to a closed pipe)",
	143: "SIGTERM (terminated by kill or shutdown)",
}

// informativeExitNote returns a model-facing note for a non-zero status that
// has a well-known non-error meaning. The last canonical simple command is the
// one that supplies the shell's exit status.
func informativeExitNote(command string, analysis shellguard.Analysis, code int) string {
	if code <= 0 {
		return ""
	}
	if signal, ok := signalExitNotes[code]; ok {
		return fmt.Sprintf(
			"exit code %d usually means the command was terminated by %s", code, signal,
		)
	}
	forms := analysis.Canonical
	if !analysis.CanonicalExact {
		forms = shellguard.CanonicalCommands(command)
	}
	if len(forms) == 0 {
		return ""
	}
	fields := strings.Fields(forms[len(forms)-1])
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
		name = name[slash+1:]
	}
	return exitCodeNotes[name][code]
}

func formatExecRunMeta(meta execRunMeta) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(meta); err != nil {
		return execRunMetaPrefix + "{}"
	}
	return execRunMetaPrefix + strings.TrimSuffix(buf.String(), "\n")
}

func newExecResult(
	forLLM string,
	forUser string,
	isError bool,
	err error,
	artifactPath string,
	spillCapped bool,
) *ToolResult {
	result := &ToolResult{
		ForLLM:  forLLM,
		ForUser: forUser,
		IsError: isError,
		Err:     err,
		// Foreground output is always bounded by boundedOutput, and the metadata
		// header adds a small fixed overhead. Mark the result as already reduced
		// so the generic spill does not persist header+preview as a new oversized
		// result when the body lands just under the inline cap.
		LargeResultSpilled: true,
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

// pumpOutput drains one session stream into its rolling log until the reader
// fails (EOF or a closed descriptor). onChunk runs before the bytes are
// appended, so the PTY path can update its key-mode detection per chunk.
func pumpOutput(label string, reader io.Reader, session *ProcessSession, onChunk func([]byte)) {
	defer recoverGoroutine(label)
	buf := make([]byte, 4096)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if onChunk != nil {
				onChunk(buf[:n])
			}
			session.appendOutput(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// recoverGoroutine is deferred at the top of a session goroutine so a panic is
// logged instead of crashing the process. onPanic runs after the log line.
func recoverGoroutine(label string, onPanic ...func()) {
	r := recover()
	if r == nil {
		return
	}
	logger.ErrorCF("shell", label+" goroutine panic recovered",
		map[string]any{
			"panic": fmt.Sprintf("%v", r),
			"stack": string(debug.Stack()),
		})
	for _, f := range onPanic {
		f()
	}
}

// processExitCode returns the exit status of a waited process, or -1 when the
// process never produced one (Wait failed before the state was recorded).
func processExitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	return state.ExitCode()
}

// sessionExitCode returns the process exit status only once the session has
// finished, so a running session is not reported as a successful exit.
func sessionExitCode(session *ProcessSession) *int {
	if session == nil || !session.IsDone() {
		return nil
	}
	code := session.GetExitCode()
	return &code
}

func (t *ExecTool) outputSpillDir() string {
	if t.workingDir == "" {
		return ""
	}
	return filepath.Join(t.workingDir, ".artifacts", "tool")
}

func (t *ExecTool) runBackground(
	ctx context.Context,
	command, cwd string,
	ptyEnabled bool,
	note string,
	notify bool,
	cb AsyncCallback,
) *ToolResult {
	sessionID := generateSessionID()
	session := &ProcessSession{
		ID:           sessionID,
		Command:      command,
		PTY:          ptyEnabled,
		Background:   true,
		StartTime:    time.Now().Unix(),
		Status:       "running",
		ownerAgentID: ToolAgentID(ctx),
		ownerChannel: strings.TrimSpace(ToolChannel(ctx)),
		ownerChatID:  ToolChatID(ctx),
		ptyKeyMode:   PtyKeyModeCSI,
		// Initialize the output buffer before the session is published to the
		// manager: readers (session_read/list_sessions) must never observe a
		// session whose buffer is still nil.
		outputBuffer: &bytes.Buffer{},
		spillDir:     t.outputSpillDir(),
	}

	shellPath, shellArgs, shellErr := shellCommand(command)
	if shellErr != nil {
		return ErrorResult(shellErr.Error())
	}
	cmd := exec.Command(shellPath, shellArgs...)
	t.prepareExecCommand(cmd, cwd, t.runEnv(0))

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
			defer recoverGoroutine("PTY cmd.Wait", func() { session.finish(-1) })
			cmd.Wait() // Wait for process to exit
			session.finish(processExitCode(cmd.ProcessState))
		}()

		go func() {
			pumpOutput("PTY read", session.ptyMaster, session, func(chunk []byte) {
				raw := string(chunk)
				if mode := detectPtyKeyMode(raw); mode != PtyKeyModeNotFound && mode != session.GetPtyKeyMode() {
					session.SetPtyKeyMode(mode)
				}
			})
			// The child side is gone (EOF/EIO) or the session was killed:
			// release the master descriptor so no fd survives the session.
			_ = session.ptyMaster.Close()
		}()
	} else {
		// Non-PTY mode: one goroutine reads the merged stdout/stderr pipe.
		// When process exits, OS closes the pipe write end → Read() returns EOF.
		go func() {
			pumpOutput("pipe read", outputReader, session, nil)

			_ = outputReader.Close()
			if stdinWriter != nil {
				_ = stdinWriter.Close()
			}
			cmd.Wait()

			session.finish(processExitCode(cmd.ProcessState))
		}()
	}

	// Install the completion callback before returning the session, so a
	// process that exits immediately cannot lose its notification. The
	// callback is stored even when notify is false, so a later poll/wait can
	// promote the session to notify-on-complete.
	const silentHint = "No completion notification is enabled for this background session; " +
		"use action=wait, poll, read, or kill, or start bounded jobs with notify=true."
	notified := false
	if cb != nil {
		session.SetCompletion(func() {
			cb(context.Background(), t.completionResult(session))
		})
		if notify {
			notified = session.StartNotify()
		}
	}
	if notified {
		note = withNote(note, notifyEnabledNote)
	} else if !notify {
		note = withNote(note, silentHint)
	}

	resp := ExecResponse{
		SessionID: sessionID,
		Status:    "running",
		Note:      note,
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
