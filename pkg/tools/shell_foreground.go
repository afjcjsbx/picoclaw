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
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

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
		defer recoverGoroutine("cmd.Wait", func(recovered any) {
			run.done <- fmt.Errorf("panic in cmd.Wait: %v", recovered)
		})
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
