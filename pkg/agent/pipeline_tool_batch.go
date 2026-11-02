// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// toolStep tells the batch driver what to do after a call was handled.
type toolStep uint8

const (
	// toolStepContinue goes on with the next call.
	toolStepContinue toolStep = iota
	// toolStepStop leaves the batch: the calls that did not run are already
	// persisted as skipped.
	toolStepStop
	// toolStepBreak ends the tool phase at once (ToolControlBreak, without
	// publishing the batch messages to the turn).
	toolStepBreak
)

type preparedKind uint8

const (
	preparedRun     preparedKind = iota // execute the tool
	preparedRespond                     // a BeforeTool hook answered instead of the tool
	preparedDenied                      // the turn profile, a hook or an approval refused the call
)

// preparedCall is one tool call after the prepare phase. Only run touches the
// tool concurrently; everything else happens on the turn goroutine, in the
// original call order.
type preparedCall struct {
	index int
	tc    providers.ToolCall
	kind  preparedKind

	// name and args are the call after BeforeTool hooks. loopArgs is the copy
	// recorded for loop detection, taken before the tool can mutate args.
	name     string
	args     map[string]any
	loopArgs map[string]any

	denyContent   string            // preparedDenied
	hookResult    *tools.ToolResult // preparedRespond
	execCtx       context.Context   // preparedRun
	asyncCallback tools.AsyncCallback

	result   *tools.ToolResult // set by run
	duration time.Duration
}

// toolBatch executes the tool calls of one LLM response. A call goes through
// prepare (hooks, approval, feedback), run (the tool itself) and finish (after
// hooks, delivery, history). The planner groups calls so that only run of
// consecutive, compatible calls overlaps; prepare and finish always stay on the
// turn goroutine in call order, so history, loop detection and events do not
// depend on completion order.
type toolBatch struct {
	al        *AgentLoop
	ctx       context.Context
	turnCtx   context.Context
	ts        *turnState
	exec      *turnExecution
	iteration int
	calls     []providers.ToolCall

	messages           []providers.Message
	handledAttachments []providers.Attachment
	feedback           *toolFeedbackBatch

	loopStatus loopStatus
	loopCount  int
	loopTool   string
}

func (b *toolBatch) recordLoopCall(toolName string, toolArgs map[string]any) bool {
	status, count := b.ts.recordToolCall(toolName, toolArgs)
	if status > b.loopStatus {
		b.loopStatus = status
		b.loopCount = count
		b.loopTool = toolName
	}
	return status == loopStatusCritical
}

// persistSkippedToolCall emits the skip event, appends the synthetic tool
// message to the turn and persists it to the session store.
func (b *toolBatch) persistSkippedToolCall(tc providers.ToolCall, reason, message string) {
	b.al.emitEvent(
		runtimeevents.KindAgentToolExecSkipped,
		b.ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{Tool: tc.Name, Reason: reason},
	)
	b.appendToolMessage(providers.Message{
		Role:       "tool",
		Content:    message,
		ToolCallID: tc.ID,
	}, false)
}

// abandon closes call i when the turn ends before it is finished (hard abort
// or hook abort): the call gets a terminal event, so no ToolExecStart stays
// open, and a tool message, so its tool_call ID is not left unanswered in the
// history that a hook abort keeps.
func (b *toolBatch) abandon(i int) {
	b.persistSkippedToolCall(b.calls[i], "turn aborted", "No result recorded: the turn was aborted.")
}

func (b *toolBatch) skipRemainingForLoop(start int) {
	const reason = "critical repeated tool-call loop detected"
	const message = "Skipped because a repeated tool-call loop was detected."
	for j := start; j < len(b.calls); j++ {
		b.persistSkippedToolCall(b.calls[j], reason, message)
	}
}

// appendToolMessage adds msg to the turn and, unless the turn is history-less,
// to the session store. ingest also hands it to the context manager.
func (b *toolBatch) appendToolMessage(msg providers.Message, ingest bool) {
	b.messages = append(b.messages, msg)
	if b.ts.opts.NoHistory {
		return
	}
	warnSessionStoreError(
		b.ts.sessionKey,
		"add full message",
		b.ts.agent.Sessions.AddFullMessage(b.ctx, b.ts.sessionKey, msg),
	)
	b.ts.recordPersistedMessage(msg)
	if ingest {
		b.ts.ingestMessage(b.turnCtx, b.al, msg)
	}
}

// denyByProfile marks pc denied when the active turn profile forbids the tool.
func (b *toolBatch) denyByProfile(pc *preparedCall) bool {
	if turnProfileToolAllowed(b.ts.profile, pc.name) {
		return false
	}
	pc.kind = preparedDenied
	pc.denyContent = fmt.Sprintf("Tool %q is not allowed by the active turn profile.", pc.name)
	return true
}

func (b *toolBatch) deny(pc *preparedCall, content string) {
	pc.kind = preparedDenied
	pc.denyContent = content
}

// prepare runs everything that must happen before the tool executes: turn
// profile, BeforeTool and ApproveTool hooks, start event and feedback. It
// returns toolStepBreak when a hook or a hard abort ends the turn.
func (b *toolBatch) prepare(i int) (*preparedCall, toolStep) {
	al, ts, exec := b.al, b.ts, b.exec
	if ts.hardAbortRequested() {
		exec.abortedByHardAbort = true
		return nil, toolStepBreak
	}

	tc := b.calls[i]
	pc := &preparedCall{index: i, tc: tc, name: tc.Name, args: cloneStringAnyMap(tc.Arguments)}

	if b.denyByProfile(pc) {
		return pc, toolStepContinue
	}

	if al.hooks != nil {
		toolReq, decision := al.hooks.BeforeTool(b.turnCtx, &ToolCallHookRequest{
			Meta:      ts.eventMeta("runTurn", "turn.tool.before"),
			Context:   cloneTurnContext(ts.turnCtx),
			Tool:      pc.name,
			Arguments: pc.args,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if toolReq != nil {
				pc.name = toolReq.Tool
				pc.args = toolReq.Arguments
			}
		case HookActionRespond:
			if toolReq != nil && toolReq.HookResult != nil {
				b.announceHookRespond(pc)
				pc.kind = preparedRespond
				pc.hookResult = toolReq.HookResult
				return pc, toolStepContinue
			}
			logger.WarnCF("agent", "Hook returned respond action but no HookResult provided",
				map[string]any{
					"agent_id": ts.agent.ID,
					"tool":     pc.name,
					"action":   "respond",
				})
		case HookActionDenyTool:
			b.deny(pc, hookDeniedToolContent("Tool execution denied by hook", decision.Reason))
			return pc, toolStepContinue
		case HookActionAbortTurn:
			exec.abortedByHook = true
			return nil, toolStepBreak
		case HookActionHardAbort:
			_ = ts.requestHardAbort()
			exec.abortedByHardAbort = true
			return nil, toolStepBreak
		}
	}

	if al.hooks != nil {
		approval := al.hooks.ApproveTool(b.turnCtx, &ToolApprovalRequest{
			Meta:      ts.eventMeta("runTurn", "turn.tool.approve"),
			Context:   cloneTurnContext(ts.turnCtx),
			Tool:      pc.name,
			Arguments: pc.args,
		})
		if !approval.Approved {
			b.deny(pc, hookDeniedToolContent("Tool execution denied by approval hook", approval.Reason))
			return pc, toolStepContinue
		}
	}

	if b.denyByProfile(pc) {
		return pc, toolStepContinue
	}

	logger.InfoCF("agent", fmt.Sprintf("Tool call: %s", pc.name),
		map[string]any{
			"agent_id":  ts.agent.ID,
			"tool":      pc.name,
			"arg_keys":  utils.SortedArgKeys(pc.args),
			"arg_count": len(pc.args),
			"iteration": b.iteration,
		})
	al.emitEvent(
		runtimeevents.KindAgentToolExecStart,
		ts.eventMeta("runTurn", "turn.tool.start"),
		ToolExecStartPayload{
			Tool:      pc.name,
			Arguments: cloneEventArguments(pc.args),
		},
	)
	b.feedback.publish(b.turnCtx, i, tc, pc.name, pc.args, b.messages)

	pc.asyncCallback = b.asyncCallback(pc.name)
	pc.loopArgs = cloneStringAnyMap(pc.args)
	pc.execCtx = tools.WithToolInboundContext(
		b.turnCtx,
		ts.channel,
		ts.chatID,
		ts.opts.Dispatch.MessageID(),
		ts.opts.Dispatch.ReplyToMessageID(),
	)
	pc.execCtx = tools.WithToolSessionContext(
		pc.execCtx,
		ts.agent.ID,
		ts.sessionKey,
		ts.opts.Dispatch.SessionScope,
	)
	if ts.parentTurnState != nil {
		// SubTurns join their async children before finishing; root turns
		// keep the fire-and-forget follow-up via the bus.
		pc.execCtx = tools.WithChildTracker(pc.execCtx, ts)
	}
	return pc, toolStepContinue
}

// announceHookRespond logs and publishes the start of a call that a hook
// answers instead of the tool.
func (b *toolBatch) announceHookRespond(pc *preparedCall) {
	ts := b.ts
	// respond bypasses approve_tool by design (used for plugin
	// tool injection). Surface the risky case where a hook
	// shadows a tool that is actually registered, so operators
	// can audit which sensitive tools were answered by a hook.
	if _, registered := ts.agent.Tools.Get(pc.name); registered {
		logger.WarnCF("agent", "Hook respond shadows a registered tool and bypasses approval",
			map[string]any{
				"agent_id": ts.agent.ID,
				"tool":     pc.name,
				"channel":  ts.channel,
			})
	}

	logger.InfoCF("agent", fmt.Sprintf("Tool call (hook respond): %s", pc.name),
		map[string]any{
			"agent_id":  ts.agent.ID,
			"tool":      pc.name,
			"arg_keys":  utils.SortedArgKeys(pc.args),
			"arg_count": len(pc.args),
			"iteration": b.iteration,
		})

	b.al.emitEvent(
		runtimeevents.KindAgentToolExecStart,
		ts.eventMeta("runTurn", "turn.tool.start"),
		ToolExecStartPayload{
			Tool:      pc.name,
			Arguments: cloneEventArguments(pc.args),
		},
	)

	b.feedback.publish(b.turnCtx, pc.index, pc.tc, pc.name, pc.args, b.messages)
}

func (b *toolBatch) asyncCallback(toolName string) tools.AsyncCallback {
	al, ts := b.al, b.ts
	iteration := b.iteration
	return func(_ context.Context, result *tools.ToolResult) {
		if !result.Silent && result.ForUser != "" {
			outCtx, outCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer outCancel()
			_ = al.bus.PublishOutbound(outCtx, outboundMessageForTurn(ts, result.ForUser))
		}

		content := result.ContentForLLM()
		if content == "" {
			return
		}

		content = al.cfg.FilterSensitiveData(content)

		logger.InfoCF("agent", "Async tool completed, publishing result",
			map[string]any{
				"tool":        toolName,
				"content_len": len(content),
				"channel":     ts.channel,
			})
		al.emitEvent(
			runtimeevents.KindAgentFollowUpQueued,
			ts.scope.meta(iteration, "runTurn", "turn.follow_up.queued"),
			FollowUpQueuedPayload{
				SourceTool: toolName,
				ContentLen: len(content),
			},
		)
		pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pubCancel()
		_ = al.bus.PublishInbound(pubCtx, bus.InboundMessage{
			Context: bus.InboundContext{
				Channel:  "system",
				ChatID:   fmt.Sprintf("%s:%s", ts.channel, ts.chatID),
				ChatType: "direct",
				SenderID: fmt.Sprintf("async:%s", toolName),
			},
			Content: content,
		})
	}
}

// run executes the tool. It is the only phase that may overlap with other
// calls, so it touches nothing but pc and the (thread-safe) tool registry.
func (b *toolBatch) run(pc *preparedCall) {
	start := time.Now()
	pc.result = b.ts.agent.Tools.ExecuteWithContext(
		pc.execCtx,
		pc.name,
		pc.args,
		b.ts.channel,
		b.ts.chatID,
		pc.asyncCallback,
	)
	pc.duration = time.Since(start)
}

// stillParallel reports whether the calls of a planned parallel segment are
// still plain, mutually compatible executions after the hooks ran. Hooks can
// rename a tool, change its arguments or answer in its place; they may only
// degrade a segment to sequential, never promote anything.
func (b *toolBatch) stillParallel(pcs []*preparedCall) bool {
	calls := make([]providers.ToolCall, len(pcs))
	for i, pc := range pcs {
		if pc.kind != preparedRun {
			return false
		}
		calls[i] = providers.ToolCall{Name: pc.name, Arguments: pc.args}
	}
	segments := tools.PlanBatch(b.ts.agent.Tools, calls)
	return len(segments) == 1 && segments[0].Parallel
}

// runOne prepares, runs and finishes a single call.
func (b *toolBatch) runOne(i int) toolStep {
	pc, step := b.prepare(i)
	if step != toolStepContinue {
		return step
	}
	if pc.kind == preparedRun {
		b.run(pc)
	}
	return b.conclude([]*preparedCall{pc})
}

// runParallel prepares every call of a parallel segment in order, runs them
// concurrently and finishes them in order.
func (b *toolBatch) runParallel(indexes []int, maxParallel int) toolStep {
	pcs := make([]*preparedCall, 0, len(indexes))
	for _, i := range indexes {
		pc, step := b.prepare(i)
		if step != toolStepContinue {
			// Nothing of the segment has run: close the calls prepared so far.
			for _, j := range indexes {
				b.abandon(j)
			}
			return step
		}
		pcs = append(pcs, pc)
	}

	if !b.stillParallel(pcs) {
		// A hook changed the plan: execute the prepared calls one by one.
		for _, pc := range pcs {
			if pc.kind == preparedRun {
				b.run(pc)
			}
			if step := b.conclude([]*preparedCall{pc}); step != toolStepContinue {
				return step
			}
		}
		return toolStepContinue
	}

	tools.RunConcurrent(len(pcs), maxParallel, func(n int) { b.run(pcs[n]) })
	return b.conclude(pcs)
}

// conclude finishes already-run calls in order, then applies the checkpoint
// once for the last of them: a parallel segment is atomic, its results exist
// and every tool call needs its tool message.
func (b *toolBatch) conclude(pcs []*preparedCall) toolStep {
	critical := false
	checkpoint := false
	for n, pc := range pcs {
		step, c := b.finish(pc)
		if step == toolStepBreak {
			for _, rest := range pcs[n:] {
				b.abandon(rest.index)
			}
			return step
		}
		critical = critical || c
		checkpoint = checkpoint || pc.kind != preparedDenied
	}
	last := pcs[len(pcs)-1].index
	if critical {
		b.skipRemainingForLoop(last + 1)
		return toolStepStop
	}
	if checkpoint && b.checkpoint(last) {
		return toolStepStop
	}
	return toolStepContinue
}

// checkpoint polls steering and graceful interrupts after call i. When one is
// pending it persists every later call as skipped and reports true.
func (b *toolBatch) checkpoint(i int) bool {
	al, ts, exec := b.al, b.ts, b.exec
	if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
		exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
	}

	skipReason := ""
	skipMessage := ""
	if len(exec.pendingMessages) > 0 {
		skipReason = "queued user steering message"
		skipMessage = "Skipped due to queued user message."
	} else if gracefulPending, _ := ts.gracefulInterruptRequested(); gracefulPending {
		skipReason = "graceful interrupt requested"
		skipMessage = "Skipped due to graceful interrupt."
	}

	if skipReason != "" {
		remaining := len(b.calls) - i - 1
		if remaining > 0 {
			logger.InfoCF("agent", "Turn checkpoint: skipping remaining tools",
				map[string]any{
					"agent_id":  ts.agent.ID,
					"completed": i + 1,
					"skipped":   remaining,
					"reason":    skipReason,
				})
			for j := i + 1; j < len(b.calls); j++ {
				b.persistSkippedToolCall(b.calls[j], skipReason, skipMessage)
			}
		}
		return true
	}

	if ts.pendingResults != nil {
		select {
		case result, ok := <-ts.pendingResults:
			if ok && result != nil && result.ForLLM != "" {
				content := al.cfg.FilterSensitiveData(result.ForLLM)
				msg := subTurnResultPromptMessage(content)
				b.messages = append(b.messages, msg)
				if !ts.opts.NoHistory {
					warnSessionStoreError(
						ts.sessionKey,
						"add full message",
						ts.agent.Sessions.AddFullMessage(b.ctx, ts.sessionKey, msg),
					)
				}
			}
		default:
		}
	}
	return false
}

// finish records the outcome of one prepared call. The bool reports a critical
// repeated-call loop.
func (b *toolBatch) finish(pc *preparedCall) (toolStep, bool) {
	switch pc.kind {
	case preparedDenied:
		b.finishDenied(pc)
		return toolStepContinue, false
	case preparedRespond:
		return toolStepContinue, b.finishRespond(pc)
	default:
		return b.finishRun(pc)
	}
}

func (b *toolBatch) finishDenied(pc *preparedCall) {
	b.exec.allResponsesHandled = false
	b.al.emitEvent(
		runtimeevents.KindAgentToolExecSkipped,
		b.ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{
			Tool:   pc.name,
			Reason: pc.denyContent,
		},
	)
	b.appendToolMessage(providers.Message{
		Role:       "tool",
		Content:    pc.denyContent,
		ToolCallID: pc.tc.ID,
	}, false)
}

// outboundMediaMessage builds the channel message that delivers refs to the
// user of the current turn.
func (b *toolBatch) outboundMediaMessage(refs []string) bus.OutboundMediaMessage {
	al, ts := b.al, b.ts
	parts := make([]bus.MediaPart, 0, len(refs))
	for _, ref := range refs {
		part := bus.MediaPart{Ref: ref}
		if al.mediaStore != nil {
			if _, meta, err := al.mediaStore.ResolveWithMeta(ref); err == nil {
				part.Filename = meta.Filename
				part.ContentType = meta.ContentType
				part.Type = inferMediaType(meta.Filename, meta.ContentType)
			}
		}
		parts = append(parts, part)
	}
	return bus.OutboundMediaMessage{
		Channel: ts.channel,
		ChatID:  ts.chatID,
		Context: outboundContextFromInbound(
			ts.opts.Dispatch.InboundContext,
			ts.channel,
			ts.chatID,
			ts.opts.Dispatch.ReplyToMessageID(),
		),
		AgentID:    ts.agent.ID,
		SessionKey: ts.sessionKey,
		Scope:      outboundScopeFromSessionScope(ts.opts.Dispatch.SessionScope),
		Parts:      parts,
	}
}

// canDeliverMedia reports whether handled media goes straight to the channel
// manager instead of through the bus.
func (b *toolBatch) canDeliverMedia() bool {
	return b.al.channelManager != nil && b.ts.channel != "" && !constants.IsInternalChannel(b.ts.channel)
}

// finishRespond completes a call that a BeforeTool hook answered. It reports a
// critical repeated-call loop.
func (b *toolBatch) finishRespond(pc *preparedCall) bool {
	al, ts, exec := b.al, b.ts, b.exec
	toolName, toolArgs, hookResult := pc.name, pc.args, pc.hookResult

	shouldSendForUser := !hookResult.Silent && hookResult.ForUser != "" &&
		(ts.opts.SendResponse || hookResult.ResponseHandled)
	if shouldSendForUser {
		al.bus.PublishOutbound(b.ctx, bus.OutboundMessage{
			Context: bus.InboundContext{
				Channel: ts.channel,
				ChatID:  ts.chatID,
				Raw: map[string]string{
					"is_tool_call": "true",
				},
			},
			Content: hookResult.ForUser,
		})
	}

	if len(hookResult.Media) > 0 && hookResult.ResponseHandled {
		outboundMedia := b.outboundMediaMessage(hookResult.Media)
		if b.canDeliverMedia() {
			if err := al.channelManager.SendMedia(b.ctx, outboundMedia); err != nil {
				logger.WarnCF("agent", "Failed to deliver hook media",
					map[string]any{
						"agent_id": ts.agent.ID,
						"tool":     toolName,
						"channel":  ts.channel,
						"chat_id":  ts.chatID,
						"error":    err.Error(),
					})
				hookResult.IsError = true
				hookResult.ForLLM = fmt.Sprintf("failed to deliver attachment: %v", err)
			} else {
				b.handledAttachments = append(
					b.handledAttachments,
					buildProviderAttachments(al.mediaStore, hookResult.Media)...,
				)
			}
		} else if al.bus != nil {
			al.bus.PublishOutboundMedia(b.ctx, outboundMedia)
			hookResult.ResponseHandled = false
		}
	}

	if !hookResult.ResponseHandled {
		exec.allResponsesHandled = false
	}

	// A hook-respond result bypasses tool execution, so apply the
	// large-result spill here too (no-op for already-bounded results).
	ts.agent.Tools.SpillLargeResult(hookResult, toolName)

	contentForLLM := hookResult.ContentForLLM()
	if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
		contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
	}

	var toolResultMedia []string
	if len(hookResult.Media) > 0 && !hookResult.ResponseHandled {
		hookResult.ArtifactTags = appendArtifactTags(
			hookResult.ArtifactTags,
			buildArtifactTags(al.mediaStore, hookResult.Media),
		)
		contentForLLM = hookResult.ContentForLLM()
		if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
			contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
		}
		toolResultMedia = append(toolResultMedia, hookResult.Media...)
	}
	toolResultMsg := toolResultPromptMessage(contentForLLM, pc.tc.ID, toolResultMedia)

	al.emitEvent(
		runtimeevents.KindAgentToolExecEnd,
		ts.eventMeta("runTurn", "turn.tool.end"),
		ToolExecEndPayload{
			Tool:       toolName,
			Duration:   0,
			ForLLMLen:  len(contentForLLM),
			ForUserLen: len(hookResult.ForUser),
			IsError:    hookResult.IsError,
			Async:      hookResult.Async,
		},
	)
	ts.recordToolExecution(
		toolName,
		!hookResult.IsError,
		toolErrorSummary(hookResult),
		inferSkillNamesFromToolCall(ts, toolName, toolArgs),
	)

	b.appendToolMessage(toolResultMsg, true)
	return b.recordLoopCall(toolName, toolArgs)
}

// finishRun completes an executed call: AfterTool hook, media and user
// delivery, end event, history and loop detection. It reports a critical
// repeated-call loop, or toolStepBreak when the turn is aborted.
func (b *toolBatch) finishRun(pc *preparedCall) (toolStep, bool) {
	al, ts, exec := b.al, b.ts, b.exec
	toolName, toolArgs := pc.name, pc.args
	toolResult, toolDuration := pc.result, pc.duration

	if ts.hardAbortRequested() {
		exec.abortedByHardAbort = true
		return toolStepBreak, false
	}

	if toolResult == nil {
		// RunConcurrent recovered a panic before the tool produced a result.
		toolResult = tools.ErrorResult(fmt.Sprintf("tool %q did not return a result", toolName))
	}

	if al.hooks != nil {
		toolResp, decision := al.hooks.AfterTool(b.turnCtx, &ToolResultHookResponse{
			Meta:      ts.eventMeta("runTurn", "turn.tool.after"),
			Context:   cloneTurnContext(ts.turnCtx),
			Tool:      toolName,
			Arguments: toolArgs,
			Result:    toolResult,
			Duration:  toolDuration,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if toolResp != nil {
				if toolResp.Tool != "" {
					toolName = toolResp.Tool
				}
				if toolResp.Result != nil {
					toolResult = toolResp.Result
				}
			}
		case HookActionAbortTurn:
			exec.abortedByHook = true
			return toolStepBreak, false
		case HookActionHardAbort:
			_ = ts.requestHardAbort()
			exec.abortedByHardAbort = true
			return toolStepBreak, false
		}
	}

	if toolResult == nil {
		toolResult = tools.ErrorResult("hook returned nil tool result")
	}

	// Re-apply the large-result spill: the registry already spilled the
	// original result, but an AfterTool hook may have replaced it with a
	// fresh oversized payload. Spilling here keeps every code path bounded.
	ts.agent.Tools.SpillLargeResult(toolResult, toolName)

	if len(toolResult.Media) > 0 && toolResult.ResponseHandled {
		outboundMedia := b.outboundMediaMessage(toolResult.Media)
		if b.canDeliverMedia() {
			if err := al.channelManager.SendMedia(b.ctx, outboundMedia); err != nil {
				logger.WarnCF("agent", "Failed to deliver handled tool media",
					map[string]any{
						"agent_id": ts.agent.ID,
						"tool":     toolName,
						"channel":  ts.channel,
						"chat_id":  ts.chatID,
						"error":    err.Error(),
					})
				toolResult = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
			} else {
				b.handledAttachments = append(
					b.handledAttachments,
					buildProviderAttachments(al.mediaStore, toolResult.Media)...,
				)
			}
		} else if al.bus != nil {
			al.bus.PublishOutboundMedia(b.ctx, outboundMedia)
			toolResult.ResponseHandled = false
		}
	}

	if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
		toolResult.ArtifactTags = appendArtifactTags(
			toolResult.ArtifactTags,
			buildArtifactTags(al.mediaStore, toolResult.Media),
		)
	}

	if !toolResult.ResponseHandled {
		exec.allResponsesHandled = false
	}

	shouldSendForUser := !toolResult.Silent &&
		toolResult.ForUser != "" &&
		(ts.opts.SendResponse || toolResult.ResponseHandled)
	if shouldSendForUser {
		al.bus.PublishOutbound(b.ctx, outboundMessageForTurn(ts, toolResult.ForUser))
		logger.DebugCF("agent", "Sent tool result to user",
			map[string]any{
				"tool":        toolName,
				"content_len": len(toolResult.ForUser),
			})
	}
	contentForLLM := toolResult.ContentForLLM()

	if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
		contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
	}

	var toolResultMedia []string
	if len(toolResult.Media) > 0 && !toolResult.ResponseHandled {
		toolResultMedia = append(toolResultMedia, toolResult.Media...)
	}
	toolResultMsg := toolResultPromptMessage(contentForLLM, pc.tc.ID, toolResultMedia)
	al.emitEvent(
		runtimeevents.KindAgentToolExecEnd,
		ts.eventMeta("runTurn", "turn.tool.end"),
		ToolExecEndPayload{
			Tool:       toolName,
			Duration:   toolDuration,
			ForLLMLen:  len(contentForLLM),
			ForUserLen: len(toolResult.ForUser),
			IsError:    toolResult.IsError,
			Async:      toolResult.Async,
		},
	)
	ts.recordToolExecution(
		toolName,
		!toolResult.IsError,
		toolErrorSummary(toolResult),
		inferSkillNamesFromToolCall(ts, toolName, toolArgs),
	)
	b.appendToolMessage(toolResultMsg, true)
	return toolStepContinue, b.recordLoopCall(pc.name, pc.loopArgs)
}

// planToolBatch splits the calls of one LLM response into execution segments.
// With max_parallel_tool_calls <= 1 the whole batch is one sequential segment,
// which is exactly the pre-planner behavior.
func planToolBatch(ts *turnState, calls []providers.ToolCall, maxParallel int) []tools.Segment {
	if maxParallel <= 1 || len(calls) < 2 {
		indexes := make([]int, len(calls))
		for i := range indexes {
			indexes[i] = i
		}
		return []tools.Segment{{Calls: indexes}}
	}
	segments := tools.PlanBatch(ts.agent.Tools, calls)
	logger.InfoCF("agent", "Tool batch planned",
		map[string]any{
			"agent_id": ts.agent.ID,
			"calls":    len(calls),
			"plan":     describeToolSegments(calls, segments),
			"max":      maxParallel,
		})
	return segments
}

// describeToolSegments renders a plan as `P[web_search,read_file] S[exec]`.
func describeToolSegments(calls []providers.ToolCall, segments []tools.Segment) string {
	var sb strings.Builder
	for n, seg := range segments {
		if n > 0 {
			sb.WriteByte(' ')
		}
		if seg.Parallel {
			sb.WriteString("P[")
		} else {
			sb.WriteString("S[")
		}
		for k, i := range seg.Calls {
			if k > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(calls[i].Name)
		}
		sb.WriteByte(']')
	}
	return sb.String()
}
