// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type toolGroupOutcome struct {
	next int

	abort     bool
	hardAbort bool

	criticalLoop        bool
	breakLoop           bool
	steeringSkipReason  string
	steeringSkipContent string
}

// runGroup prepares a run of concurrency-safe calls, executes them with a
// bounded fan-out, and finalizes the results in the original model order.
//
// A BeforeTool hook may rewrite a concurrency-safe call into a tool that is
// not concurrency-safe. In that case the group stops at that call, the safe
// prefix runs concurrently, and the rewritten call runs synchronously before
// the next group starts. An abort stops preparation at the aborting call;
// calls already prepared before it still execute, matching the sequential
// loop's behavior.
func (proc *toolCallProcessor) runGroup(
	ctx context.Context,
	turnCtx context.Context,
	start, end, iteration int,
) toolGroupOutcome {
	exec := proc.exec
	prepared := make([]preparedToolCall, 0, end-start)
	stop := end
	aborted := false
	hardAborted := false

prepLoop:
	for i := start; i < end; i++ {
		prep := proc.prepare(turnCtx, i, exec.normalizedToolCalls[i], iteration)
		switch prep.action {
		case toolCallActionAbortTurn:
			aborted = true
			stop = i
			break prepLoop
		case toolCallActionHardAbort:
			hardAborted = true
			stop = i
			break prepLoop
		}
		prepared = append(prepared, prep)
		if prep.action == toolCallActionExecute && !proc.isConcurrencySafe(prep.name) {
			// A hook rewrote this call to a tool that is not safe to run
			// concurrently. Run the safe prefix as a batch, then this call
			// synchronously, and let the outer loop handle the rest.
			stop = i + 1
			break prepLoop
		}
	}

	if aborted {
		exec.abortedByHook = true
	}
	if hardAborted {
		exec.abortedByHardAbort = true
	}

	concurrentCalls := 0
	for k := range prepared {
		if prepared[k].action == toolCallActionExecute && proc.isConcurrencySafe(prepared[k].name) {
			concurrentCalls++
		}
	}
	if concurrentCalls > 1 {
		logger.DebugCF("agent", "Executing concurrency-safe tool batch",
			map[string]any{
				"agent_id":   proc.ts.agent.ID,
				"call_count": concurrentCalls,
				"start":      start,
				"end":        stop,
				"iteration":  iteration,
			})
	}

	results := make([]*tools.ToolResult, len(prepared))
	durations := make([]time.Duration, len(prepared))

	var wg sync.WaitGroup
	sem := make(chan struct{}, tools.MaxParallelToolCalls)
	for k := range prepared {
		if prepared[k].action != toolCallActionExecute || !proc.isConcurrencySafe(prepared[k].name) {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					logger.ErrorCF("agent", "concurrent tool execution panic recovered",
						map[string]any{
							"tool":  prepared[k].name,
							"panic": fmt.Sprintf("%v", r),
							"stack": string(debug.Stack()),
						})
					results[k] = tools.ErrorResult(
						fmt.Sprintf("internal panic in tool %s", prepared[k].name),
					)
				}
			}()
			results[k], durations[k] = proc.execute(turnCtx, prepared[k])
		}(k)
	}
	wg.Wait()

	if !hardAborted && proc.ts.hardAbortRequested() {
		exec.abortedByHardAbort = true
		return toolGroupOutcome{next: stop, hardAbort: true}
	}

	// The rewritten unsafe call (if any) runs synchronously after the safe
	// prefix finished.
	for k := range prepared {
		if prepared[k].action != toolCallActionExecute || proc.isConcurrencySafe(prepared[k].name) {
			continue
		}
		results[k], durations[k] = proc.execute(turnCtx, prepared[k])
		if proc.ts.hardAbortRequested() {
			exec.abortedByHardAbort = true
			return toolGroupOutcome{next: stop, hardAbort: true}
		}
	}

	outcome := toolGroupOutcome{next: stop, abort: aborted, hardAbort: hardAborted}
	for k := range prepared {
		prep := prepared[k]
		switch prep.action {
		case toolCallActionSkip:
			proc.appendSkip(prep)
			continue
		case toolCallActionRespond:
			abort, hardAbort := proc.finish(ctx, turnCtx, prep, prep.result, 0, false)
			if abort || hardAbort {
				outcome.abort = abort
				outcome.hardAbort = hardAbort
				return outcome
			}
		case toolCallActionExecute:
			abort, hardAbort := proc.finish(ctx, turnCtx, prep, results[k], durations[k], true)
			if abort || hardAbort {
				outcome.abort = abort
				outcome.hardAbort = hardAbort
				return outcome
			}
		}

		if proc.recordLoopCall(prep.name, prep.args) {
			outcome.criticalLoop = true
			outcome.breakLoop = true
		} else if !outcome.breakLoop {
			if reason, content := proc.steeringSkipReason(); reason != "" {
				outcome.breakLoop = true
				outcome.steeringSkipReason = reason
				outcome.steeringSkipContent = content
			}
		}
	}

	if !outcome.breakLoop {
		proc.drainPendingResults()
	}
	return outcome
}
