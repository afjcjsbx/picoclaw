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

// foregroundOutputChars is the size of the inline output window of a
// foreground run.
func (t *ExecTool) foregroundOutputChars() int {
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
	return maxOutputChars
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
	cmd, err := t.newShellCmd(shellCommandText, cwd, t.runEnv(timeout))
	if err != nil {
		return ErrorResult(err.Error())
	}

	collector := newBoundedOutput(t.foregroundOutputChars(), t.outputSpillDir())
	run, err := startForeground(cmd, collector)
	if err != nil {
		return ErrorResult(err.Error())
	}
	interrupted, waitErr := run.wait(cmdCtx)
	run.drain()

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
	outcome := classifyRun(waitErr, interrupted, cmdCtx.Err(), output, timeout)
	forUser := outcome.forUser
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
	if outcome.exitCode != nil {
		exitNote = informativeExitNote(command, analysis, *outcome.exitCode)
	}
	meta := execRunMeta{
		ExitCode:     outcome.exitCode,
		TimedOut:     outcome.timedOut,
		Truncated:    view.Truncated,
		TotalBytes:   view.TotalBytes,
		OmittedBytes: view.OmittedBytes,
		ArtifactPath: artifactPath,
		DurationMs:   time.Since(start).Milliseconds(),
		Error:        outcome.errorMessage,
		Cwd:          observedCwd,
		ExitNote:     exitNote,
	}
	forLLM := formatExecRunMeta(meta) + "\n" + output
	if outcome.timedOut {
		// A timeout is the moment we know the command outlived a foreground
		// call, so point the model at tracked sessions instead of letting it
		// guess from the exit status alone.
		forLLM += "\n\n" + timeoutBackgroundGuidance
	}

	return newExecResult(forLLM, forUser, outcome.err != nil, outcome.err, artifactPath, spillCapped)
}

// foregroundRun is a started foreground command whose merged output is being
// collected.
type foregroundRun struct {
	cmd      *exec.Cmd
	reader   *os.File
	done     chan error    // result of cmd.Wait
	readDone chan struct{} // closed once the output pipe is drained
}

// startForeground starts cmd with stdout and stderr on one pipe, so the result
// preserves the order in which the child wrote them and a large stderr stream
// cannot block a separate pipe reader. The pipe is drained into collector.
func startForeground(cmd *exec.Cmd, collector *boundedOutput) (*foregroundRun, error) {
	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create output pipe: %w", err)
	}
	cmd.Stdout = outputWriter
	cmd.Stderr = outputWriter

	// Route shell execution through the shared isolation entry point so exec tool
	// subprocesses receive the same isolation policy as other integrations.
	if err := isolation.Start(cmd); err != nil {
		_ = outputWriter.Close()
		_ = outputReader.Close()
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	// The child inherited its own copy of the write end. Release the parent's
	// copy so the reader observes EOF when the child exits.
	_ = outputWriter.Close()

	run := &foregroundRun{
		cmd:      cmd,
		reader:   outputReader,
		done:     make(chan error, 1),
		readDone: make(chan struct{}),
	}
	go func() {
		defer close(run.readDone)
		// Strip ANSI sequences while draining so the in-memory window, the
		// spilled artifact and the truncation budget all see visible text.
		_, _ = io.Copy(collector, &ansiReader{src: outputReader})
		_ = outputReader.Close()
	}()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("shell", "cmd.Wait goroutine panic recovered",
					map[string]any{
						"panic": fmt.Sprintf("%v", r),
						"stack": string(debug.Stack()),
					})
				run.done <- fmt.Errorf("panic in cmd.Wait: %v", r)
			}
		}()
		run.done <- cmd.Wait()
	}()
	return run, nil
}

// wait blocks until the command exits or ctx ends. When ctx ends first, the
// whole process tree is terminated and interrupted is true.
func (r *foregroundRun) wait(ctx context.Context) (interrupted bool, err error) {
	select {
	case err = <-r.done:
		return false, err
	case <-ctx.Done():
	}
	_ = terminateProcessTree(r.cmd)
	select {
	case err = <-r.done:
	case <-time.After(2 * time.Second):
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
		err = <-r.done
	}
	return true, err
}

// drain waits for the merged output stream to end. A surviving grandchild can
// keep the write end open after the direct child exits, so the wait is bounded
// instead of hanging the tool call.
func (r *foregroundRun) drain() {
	select {
	case <-r.readDone:
	case <-time.After(2 * time.Second):
		_ = r.reader.Close()
		<-r.readDone
	}
}

// runOutcome is how a foreground run ended, as reported to the model and user.
type runOutcome struct {
	exitCode     *int
	timedOut     bool
	errorMessage string
	forUser      string
	err          error
}

// classifyRun maps the end of a foreground command to its outcome. interrupted
// reports that the context ended before the command did; ctxErr says why.
func classifyRun(
	waitErr error,
	interrupted bool,
	ctxErr error,
	output string,
	timeout time.Duration,
) runOutcome {
	outcome := runOutcome{forUser: output, err: waitErr}

	switch {
	case interrupted && errors.Is(ctxErr, context.DeadlineExceeded):
		outcome.timedOut = true
		outcome.errorMessage = fmt.Sprintf("command timed out after %v", timeout)
		outcome.forUser = fmt.Sprintf("Command timed out after %v", timeout)
		if output != "" {
			outcome.forUser += "\n\nPartial output before timeout:\n" + output
		}
		if waitErr != nil {
			outcome.err = fmt.Errorf("command timeout: %w", waitErr)
		} else {
			outcome.err = fmt.Errorf("command timed out after %v", timeout)
		}
	case interrupted && errors.Is(ctxErr, context.Canceled):
		outcome.errorMessage = "command canceled"
		outcome.forUser = output + "\n\n[Command canceled]"
		if waitErr != nil {
			outcome.err = fmt.Errorf("command canceled: %w", waitErr)
		} else {
			outcome.err = errors.New("command canceled")
		}
	case waitErr == nil:
		code := 0
		outcome.exitCode = &code
	default:
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			outcome.errorMessage = waitErr.Error()
			outcome.forUser = output + fmt.Sprintf("\n\n[Command failed: %v]", waitErr)
			break
		}
		if code := exitErr.ExitCode(); code >= 0 {
			outcome.exitCode = &code
			outcome.forUser = output + fmt.Sprintf("\n\n[Command exited with code %d]", code)
		} else {
			// A process killed by a signal has no meaningful exit status.
			// Surface the wait error instead of a synthetic -1.
			outcome.errorMessage = exitErr.Error()
			outcome.forUser = output + "\n\n[Command exited with code -1] (killed by signal)"
		}
	}
	return outcome
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
