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
)

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
	return reason + "; started as a tracked background session. Use action=wait, poll, read, or kill with this sessionId."
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
	extraEnv map[string]string,
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

	shellPath, shellArgs, shellErr := shellCommand(command)
	if shellErr != nil {
		return ErrorResult(shellErr.Error())
	}
	cmd := exec.CommandContext(cmdCtx, shellPath, shellArgs...)
	t.prepareExecCommand(cmd, cwd, t.runEnv(extraEnv, timeout))

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
	case err == nil:
		code := 0
		exitCode = &code
	case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
		timedOut = true
		errorMessage = fmt.Sprintf("command timed out after %v", timeout)
		forUser = fmt.Sprintf("Command timed out after %v", timeout)
		if output != "" {
			forUser += "\n\nPartial output before timeout:\n" + output
		}
		resultErr = fmt.Errorf("command timeout: %w", err)
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

	meta := execRunMeta{
		ExitCode:   exitCode,
		TimedOut:   timedOut,
		Truncated:  truncated,
		DurationMs: time.Since(start).Milliseconds(),
		Error:      errorMessage,
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
	ExitCode   *int   `json:"exitCode"`
	TimedOut   bool   `json:"timedOut"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"durationMs"`
	Error      string `json:"error,omitempty"`
}

const execRunMetaPrefix = "[exec] "

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
	extraEnv map[string]string,
) *ToolResult {
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

	shellPath, shellArgs, shellErr := shellCommand(command)
	if shellErr != nil {
		return ErrorResult(shellErr.Error())
	}
	cmd := exec.Command(shellPath, shellArgs...)
	t.prepareExecCommand(cmd, cwd, t.runEnv(extraEnv, 0))

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
			defer recoverGoroutine("PTY read")
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
			defer recoverGoroutine("pipe read")
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

			session.finish(processExitCode(cmd.ProcessState))
		}()
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
