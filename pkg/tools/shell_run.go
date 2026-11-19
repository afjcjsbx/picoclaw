package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/creack/pty"

	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// withNote appends one model-facing note to another without leaving a double
// space when the first one is empty.
func withNote(base, add string) string {
	if base == "" {
		return add
	}
	return base + " " + add
}

// findShellExecutable resolves sh through PATH the same way the shell would.
func findShellExecutable() (string, error) {
	path, err := exec.LookPath("sh")
	if err != nil {
		return "", fmt.Errorf("could not find executable sh in PATH: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve shell path: %w", err)
	}
	return absolute, nil
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

// pumpOutput drains one session stream into its rolling log until the reader
// fails (EOF or a closed descriptor). onChunk runs before the bytes are
// appended, so the PTY path can update its key-mode detection per chunk.
func pumpOutput(label string, reader io.Reader, session *ProcessSession, onChunk func([]byte)) {
	defer recoverGoroutine(label)
	buf := make([]byte, 4096)
	var strip ansiStripper
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if onChunk != nil {
				onChunk(buf[:n])
			}
			// Key-mode detection needs the raw smkx/rmkx sequences; strip ANSI
			// only after that decision. Stripping also removes escape bytes
			// from the rolling window and the spilled artifact.
			if clean := strip.Strip(buf[:n]); len(clean) > 0 {
				session.appendOutput(clean)
			}
		}
		if err != nil {
			return
		}
	}
}

// recoverGoroutine is deferred at the top of a session goroutine so a panic is
// logged instead of crashing the process. onPanic runs after the log line with
// the recovered value.
func recoverGoroutine(label string, onPanic ...func(recovered any)) {
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
		f(r)
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

// newShellCmd builds the shell command shared by foreground, background and
// PTY runs.
func (t *ExecTool) newShellCmd(command, cwd string, env []string) (*exec.Cmd, error) {
	shellPath, shellArgs, err := shellCommand(command)
	if err != nil {
		return nil, err
	}
	// Do not use exec.CommandContext: its automatic Process.Kill can reap the
	// shell before terminateProcessTree enumerates descendants, losing the
	// ancestry link to children that changed session (setsid). The timeout is
	// handled explicitly so the process tree is still observable.
	cmd := exec.Command(shellPath, shellArgs...)
	t.prepareExecCommand(cmd, cwd, env)
	return cmd, nil
}

func (t *ExecTool) runBackground(
	ctx context.Context,
	command, cwd string,
	ptyEnabled bool,
	note string,
	notify bool,
	cb AsyncCallback,
) *ToolResult {
	session := t.newBackgroundSession(ctx, command, ptyEnabled)

	cmd, err := t.newShellCmd(command, cwd, t.runEnv(0))
	if err != nil {
		return ErrorResult(err.Error())
	}
	stdio, err := attachSessionStdio(cmd, session)
	if err != nil {
		return ErrorResult(err.Error())
	}

	// Background sessions use the same startup path so isolation stays consistent
	// with synchronous exec runs.
	if startErr := isolation.Start(cmd); startErr != nil {
		stdio.abort(session)
		return ErrorResult(fmt.Sprintf("failed to start command: %v", startErr))
	}
	// The child inherited its own copies of the pipe/PTY write ends. The parent
	// must release its copy now, otherwise the readers never observe EOF/EIO
	// when the child exits and the goroutines would block forever.
	stdio.releaseChildEnd()

	session.PID = cmd.Process.Pid
	t.sessionManager.Add(session)
	watchSession(cmd, session, stdio)

	note = t.installCompletion(session, note, notify, cb)

	resp := ExecResponse{
		SessionID: session.ID,
		Status:    "running",
		Note:      note,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return &ToolResult{
		ForLLM:  string(data),
		ForUser: fmt.Sprintf("Session %s started", session.ID),
		IsError: false,
	}
}

func (t *ExecTool) newBackgroundSession(ctx context.Context, command string, ptyEnabled bool) *ProcessSession {
	return &ProcessSession{
		ID:           generateSessionID(),
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
		spill:        spillFile{dir: t.outputSpillDir(), pattern: "exec_bg_*.log"},
	}
}

// sessionStdio holds the parent-side descriptors of a background command's
// standard streams.
type sessionStdio struct {
	// reader is the read end of the merged output pipe; nil for a PTY.
	reader io.ReadCloser
	// childEnd is the pipe's write end or the PTY slave. Only the child should
	// hold it once the command started.
	childEnd *os.File
	// stdin is the command's stdin pipe; nil for a PTY.
	stdin io.WriteCloser
}

// attachSessionStdio connects cmd to a PTY or to pipes, depending on the
// session, and records the parent's ends on the session.
func attachSessionStdio(cmd *exec.Cmd, session *ProcessSession) (*sessionStdio, error) {
	if session.PTY {
		ptmx, tty, err := pty.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to create PTY: %w", err)
		}

		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		// For PTY, we need Setsid to create a new session.
		// Note: Setsid and Setpgid conflict, so we must replace SysProcAttr entirely.
		setSysProcAttrForPty(cmd)

		session.ptyMaster = ptmx
		return &sessionStdio{childEnd: tty}, nil
	}

	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create output pipe: %w", err)
	}
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter
	stdinWriter, err := cmd.StdinPipe()
	if err != nil {
		_ = outputReader.Close()
		_ = outputWriter.Close()
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	session.stdoutPipe = outputReader
	session.stdinWriter = stdinWriter
	return &sessionStdio{reader: outputReader, childEnd: outputWriter, stdin: stdinWriter}, nil
}

// abort releases every descriptor when the command could not start.
func (s *sessionStdio) abort(session *ProcessSession) {
	if s.reader != nil {
		_ = s.reader.Close()
	}
	s.releaseChildEnd()
	if session.ptyMaster != nil {
		_ = session.ptyMaster.Close()
	}
}

func (s *sessionStdio) releaseChildEnd() {
	if s.childEnd != nil {
		_ = s.childEnd.Close()
	}
}

// watchSession starts the goroutines that feed the session log and record the
// exit.
func watchSession(cmd *exec.Cmd, session *ProcessSession, stdio *sessionStdio) {
	// PTY mode: read from ptyMaster and wait for process
	// Note: On Linux, closing ptyMaster doesn't interrupt blocking Read() calls,
	// so we need cmd.Wait() in a separate goroutine to detect process exit.
	if session.PTY && session.ptyMaster != nil {
		go func() {
			defer recoverGoroutine("PTY cmd.Wait", func(any) { session.finish(-1) })
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
		return
	}

	// Non-PTY mode: one goroutine reads the merged stdout/stderr pipe.
	// When process exits, OS closes the pipe write end → Read() returns EOF.
	go func() {
		pumpOutput("pipe read", stdio.reader, session, nil)

		_ = stdio.reader.Close()
		if stdio.stdin != nil {
			_ = stdio.stdin.Close()
		}
		cmd.Wait()

		session.finish(processExitCode(cmd.ProcessState))
	}()
}

// installCompletion installs the completion callback before the session is
// returned, so a process that exits immediately cannot lose its notification.
// The callback is stored even when notify is false, so a later poll/wait can
// promote the session to notify-on-complete. It returns note extended with
// the notification state.
func (t *ExecTool) installCompletion(
	session *ProcessSession,
	note string,
	notify bool,
	cb AsyncCallback,
) string {
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
	switch {
	case notified:
		return withNote(note, notifyEnabledNote)
	case !notify:
		return withNote(note, silentHint)
	}
	return note
}
