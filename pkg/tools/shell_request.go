package tools

import (
	"context"
	"runtime"
	"time"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// maxTimeoutSeconds is the largest timeout that still fits a time.Duration.
const maxTimeoutSeconds = (1<<63 - 1) / int64(time.Second)

// runRequest is the validated form of the arguments of action=run.
type runRequest struct {
	command    string
	pty        bool
	background bool
	notify     bool
	// cwd is the explicit working directory; empty means reuse the remembered
	// one.
	cwd string
	// timeoutSeconds is meaningful only when hasTimeout is set.
	timeoutSeconds int64
	hasTimeout     bool
}

// runPlan is how one run call will execute.
type runPlan struct {
	// command is what the shell runs. It differs from runRequest.command when a
	// trailing '&' was removed; the guard still inspects the written command.
	command string
	// background is set for explicit background runs, a converted trailing '&'
	// and foreground runs promoted past the cap.
	background bool
	timeout    time.Duration
	// note explains to the model why the run is not what it asked for.
	note string
}

// executeRun handles action=run. A call passes through these stages in order;
// the first one that fails returns an error result and nothing is executed.
//
//  1. parseRunRequest: arguments are validated. The remote gate already ran in
//     execute, so a run never reaches here from an untrusted caller.
//  2. planRun: a trailing unquoted '&' becomes background=true so the process
//     is tracked, and a foreground request above the foreground cap, or
//     timeout=0, is promoted to a tracked background session instead of being
//     clamped or rejected.
//  3. admitRun: the working directory is the explicit cwd, else the remembered
//     one (revalidated), else the workspace; the layered command guard runs;
//     symlinks are resolved again right before execution to narrow the TOCTOU
//     window; and the cwd is remembered only after the guard passed.
//     Environment variables are not per-call state; the child always starts
//     from the sanitized base environment.
//  4. Launch: background and promoted runs go to runBackground, which returns
//     a sessionId; everything else goes to runSync. Both build the child env
//     from the sanitized base (allowlisted parent variables plus env_set) and
//     PICOCLAW_EXEC_*, and start the process through the isolation layer.
//     runSync bounds the output (head and tail, with the full stream spilled to
//     an artifact), kills the process tree on timeout and prefixes the result
//     with an "[exec] {...}" status header.
//
// Secret redaction is not done here. After the tool returns, the registry
// spills oversized results to an artifact that keeps the raw output on disk,
// and the agent loop then replaces known secrets in the model-facing content
// with [FILTERED] (tools.filter_sensitive_data).
func (t *ExecTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	req, errResult := parseRunRequest(args)
	if errResult != nil {
		return errResult
	}

	// Parse once. The trailing-'&' converter, detach check, command guard and
	// exit note all consume the same AST instead of re-parsing the text.
	analysis := shellguard.Analyze(req.command)

	plan, errResult := t.planRun(req, analysis)
	if errResult != nil {
		return errResult
	}

	cwd, errResult := t.admitRun(ctx, req, plan, analysis)
	if errResult != nil {
		return errResult
	}

	if plan.background {
		return t.runBackground(ctx, plan.command, cwd, req.pty, plan.note, req.notify, cb)
	}
	return t.runSync(ctx, plan.command, cwd, plan.timeout, analysis)
}

func parseRunRequest(args map[string]any) (runRequest, *ToolResult) {
	command, ok := args["command"].(string)
	if !ok {
		return runRequest{}, ErrorResult("command is required")
	}
	req := runRequest{
		command:    command,
		pty:        boolArg(args, "pty"),
		background: boolArg(args, "background"),
		notify:     boolArg(args, "notify"),
	}
	req.cwd, _ = args["cwd"].(string)

	if value, provided, valid := numberArg(args, "timeout", 0); provided {
		if !valid || value > float64(maxTimeoutSeconds) {
			return runRequest{}, ErrorResult("timeout must be a non-negative integer number of seconds")
		}
		req.hasTimeout = true
		req.timeoutSeconds = int64(value)
	}
	return req, nil
}

// planRun decides whether the run is foreground or tracked background and
// which timeout applies, and rejects argument combinations that cannot work.
func (t *ExecTool) planRun(req runRequest, analysis shellguard.Analysis) (runPlan, *ToolResult) {
	plan := runPlan{command: req.command, background: req.background, timeout: t.timeout}

	// On Unix, `sh -c "server &"` returns as soon as the job starts: a
	// foreground call would report success while the process keeps running
	// untracked and its output pipe is closed. Normalize the trailing operator
	// into a tracked background session instead. Windows shells do not use '&'
	// as a background operator, so the helper is a no-op there.
	trailingConverted := false
	if normalized, converted := normalizeTrailingBackgroundAmpersand(req.command, analysis, runtime.GOOS); converted {
		trailingConverted = true
		plan.command = normalized
		if !plan.background {
			plan.background = true
			plan.note = "Trailing '&' was converted to background=true so the process is tracked."
		}
	}

	timeoutDisabled := false
	if req.hasTimeout {
		// timeout=0 means "no timeout", which is already how background sessions behave.
		if plan.background && req.timeoutSeconds != 0 {
			if req.background {
				return runPlan{}, ErrorResult("timeout is only supported for foreground commands")
			}
			// The caller asked for a foreground timeout, but the trailing '&'
			// already moved the command to the background, where timeouts do
			// not apply. Keep the command and explain the ignored argument.
			plan.note = withNote(
				plan.note,
				"The requested timeout was ignored because background sessions are not time-limited.",
			)
		} else {
			timeoutDisabled = req.timeoutSeconds == 0
			plan.timeout = time.Duration(req.timeoutSeconds) * time.Second
		}
	}

	// The exec tool caps a foreground call at t.foregroundCap. Requests that
	// would exceed it (including timeout=0, which disables the limit) are
	// promoted to a tracked background session instead of being clamped or
	// rejected.
	promoted := !plan.background && t.foregroundCap > 0 &&
		(timeoutDisabled || plan.timeout > t.foregroundCap)

	// A foreground nohup/setsid/disown or an inner '&' leaves a process the
	// session manager cannot track. The trailing '&' converter above already
	// turned the unambiguous single-operator form into a tracked run.
	if reason := untrackedDetachReason(
		plan.command, analysis, plan.background || promoted, trailingConverted,
	); reason != "" {
		return runPlan{}, ErrorResult(reason)
	}

	if req.pty && !ptySupported {
		return runPlan{}, ErrorResult("PTY is not supported on Windows. Use background=true without pty.")
	}

	if req.notify && !plan.background && !promoted {
		return runPlan{}, ErrorResult(
			"notify is only supported with background=true or when the run is promoted to background",
		)
	}

	if promoted {
		plan.background = true
		plan.note = t.foregroundPromotionNote(timeoutDisabled, plan.timeout)
	}
	return plan, nil
}
