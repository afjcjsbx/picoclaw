// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type toolCallAction int

const (
	toolCallActionExecute toolCallAction = iota
	toolCallActionRespond
	toolCallActionSkip
	toolCallActionAbortTurn
	toolCallActionHardAbort
)

type preparedToolCall struct {
	index  int
	tc     providers.ToolCall
	name   string
	args   map[string]any
	action toolCallAction

	// result is the synthetic result for toolCallActionRespond.
	result *tools.ToolResult

	skipContent string
}

// toolCallProcessor owns the per-iteration bookkeeping of tool execution.
// Tool calls can be prepared (hooks, approval, turn profile), executed either
// sequentially or as a bounded concurrent batch, and then finalized in the
// original model order.
type toolCallProcessor struct {
	al        *AgentLoop
	ts        *turnState
	exec      *turnExecution
	feedback  *toolFeedbackBatch
	iteration int

	messages           []providers.Message
	handledAttachments []providers.Attachment

	loopDetectionStatus loopStatus
	loopDetectionCount  int
	loopDetectionTool   string
}

func newToolCallProcessor(p *Pipeline, ts *turnState, exec *turnExecution, iteration int) *toolCallProcessor {
	return &toolCallProcessor{
		al:        p.al,
		ts:        ts,
		exec:      exec,
		feedback:  newToolFeedbackBatch(p.al, ts, exec),
		iteration: iteration,
		messages:  exec.messages,
	}
}

func (proc *toolCallProcessor) isConcurrencySafe(toolName string) bool {
	return proc.ts.agent.Tools.IsConcurrencySafe(toolName)
}

func (proc *toolCallProcessor) executionContext(turnCtx context.Context) context.Context {
	ts := proc.ts
	execCtx := tools.WithToolInboundContext(
		turnCtx,
		ts.channel,
		ts.chatID,
		ts.opts.Dispatch.MessageID(),
		ts.opts.Dispatch.ReplyToMessageID(),
	)
	execCtx = tools.WithToolSessionContext(
		execCtx,
		ts.agent.ID,
		ts.sessionKey,
		ts.opts.Dispatch.SessionScope,
	)
	if ts.parentTurnState != nil {
		// SubTurns join their async children before finishing; root turns
		// keep the fire-and-forget follow-up via the bus.
		execCtx = tools.WithChildTracker(execCtx, ts)
	}
	return execCtx
}

func (proc *toolCallProcessor) asyncCallback(toolName string) tools.AsyncCallback {
	ts := proc.ts
	al := proc.al
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
			ts.scope.meta(proc.iteration, "runTurn", "turn.follow_up.queued"),
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

// prepare runs all pre-execution gatekeeping for one tool call: turn profile,
// BeforeTool hooks, approval hooks, and the second profile check. It does not
// mutate message history; skip messages are appended later in original order.
func (proc *toolCallProcessor) prepare(
	turnCtx context.Context,
	index int,
	tc providers.ToolCall,
	iteration int,
) preparedToolCall {
	al := proc.al
	ts := proc.ts

	prepared := preparedToolCall{
		index: index,
		tc:    tc,
		name:  tc.Name,
		args:  cloneStringAnyMap(tc.Arguments),
	}

	denyByTurnProfile := func() (string, bool) {
		if turnProfileToolAllowed(ts.profile, prepared.name) {
			return "", false
		}
		return fmt.Sprintf("Tool %q is not allowed by the active turn profile.", prepared.name), true
	}

	if content, denied := denyByTurnProfile(); denied {
		prepared.action = toolCallActionSkip
		prepared.skipContent = content
		return prepared
	}

	if al.hooks != nil {
		toolReq, decision := al.hooks.BeforeTool(turnCtx, &ToolCallHookRequest{
			Meta:      ts.eventMeta("runTurn", "turn.tool.before"),
			Context:   cloneTurnContext(ts.turnCtx),
			Tool:      prepared.name,
			Arguments: prepared.args,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if toolReq != nil {
				prepared.name = toolReq.Tool
				prepared.args = toolReq.Arguments
			}
		case HookActionRespond:
			if toolReq != nil && toolReq.HookResult != nil {
				hookResult := toolReq.HookResult

				// respond bypasses approve_tool by design (used for plugin
				// tool injection). Surface the risky case where a hook
				// shadows a tool that is actually registered, so operators
				// can audit which sensitive tools were answered by a hook.
				if _, registered := ts.agent.Tools.Get(prepared.name); registered {
					logger.WarnCF("agent", "Hook respond shadows a registered tool and bypasses approval",
						map[string]any{
							"agent_id": ts.agent.ID,
							"tool":     prepared.name,
							"channel":  ts.channel,
						})
				}

				proc.logToolCall(prepared, iteration, true)
				proc.emitToolStart(turnCtx, prepared)

				prepared.action = toolCallActionRespond
				prepared.result = hookResult
				return prepared
			}
			logger.WarnCF("agent", "Hook returned respond action but no HookResult provided",
				map[string]any{
					"agent_id": ts.agent.ID,
					"tool":     prepared.name,
					"action":   "respond",
				})
		case HookActionDenyTool:
			prepared.action = toolCallActionSkip
			prepared.skipContent = hookDeniedToolContent("Tool execution denied by hook", decision.Reason)
			return prepared
		case HookActionAbortTurn:
			prepared.action = toolCallActionAbortTurn
			return prepared
		case HookActionHardAbort:
			_ = ts.requestHardAbort()
			prepared.action = toolCallActionHardAbort
			return prepared
		}
	}

	if al.hooks != nil {
		approval := al.hooks.ApproveTool(turnCtx, &ToolApprovalRequest{
			Meta:      ts.eventMeta("runTurn", "turn.tool.approve"),
			Context:   cloneTurnContext(ts.turnCtx),
			Tool:      prepared.name,
			Arguments: prepared.args,
		})
		if !approval.Approved {
			prepared.action = toolCallActionSkip
			prepared.skipContent = hookDeniedToolContent(
				"Tool execution denied by approval hook",
				approval.Reason,
			)
			return prepared
		}
	}

	if content, denied := denyByTurnProfile(); denied {
		prepared.action = toolCallActionSkip
		prepared.skipContent = content
		return prepared
	}

	proc.logToolCall(prepared, iteration, false)
	proc.emitToolStart(turnCtx, prepared)
	prepared.action = toolCallActionExecute
	return prepared
}

func (proc *toolCallProcessor) logToolCall(
	prepared preparedToolCall,
	iteration int,
	hookRespond bool,
) {
	message := fmt.Sprintf("Tool call: %s", prepared.name)
	if hookRespond {
		message = fmt.Sprintf("Tool call (hook respond): %s", prepared.name)
	}
	logger.InfoCF("agent", message,
		map[string]any{
			"agent_id":  proc.ts.agent.ID,
			"tool":      prepared.name,
			"arg_keys":  utils.SortedArgKeys(prepared.args),
			"arg_count": len(prepared.args),
			"iteration": iteration,
		})
}

func (proc *toolCallProcessor) emitToolStart(turnCtx context.Context, prepared preparedToolCall) {
	proc.al.emitEvent(
		runtimeevents.KindAgentToolExecStart,
		proc.ts.eventMeta("runTurn", "turn.tool.start"),
		ToolExecStartPayload{
			Tool:      prepared.name,
			Arguments: cloneEventArguments(prepared.args),
		},
	)
	proc.feedback.publish(
		turnCtx,
		prepared.index,
		prepared.tc,
		prepared.name,
		prepared.args,
		proc.messages,
	)
}

func (proc *toolCallProcessor) appendSkip(prepared preparedToolCall) {
	al := proc.al
	ts := proc.ts

	al.emitEvent(
		runtimeevents.KindAgentToolExecSkipped,
		ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{
			Tool:   prepared.name,
			Reason: prepared.skipContent,
		},
	)
	msg := providers.Message{
		Role:       "tool",
		Content:    prepared.skipContent,
		ToolCallID: prepared.tc.ID,
	}
	proc.messages = append(proc.messages, msg)
	if !ts.opts.NoHistory {
		ts.agent.Sessions.AddFullMessage(ts.sessionKey, msg)
		ts.recordPersistedMessage(msg)
	}
	proc.exec.allResponsesHandled = false
}

// execute runs one prepared tool call and returns its result and duration.
func (proc *toolCallProcessor) execute(
	turnCtx context.Context,
	prepared preparedToolCall,
) (*tools.ToolResult, time.Duration) {
	start := time.Now()
	result := proc.ts.agent.Tools.ExecuteWithContext(
		proc.executionContext(turnCtx),
		prepared.name,
		prepared.args,
		proc.ts.channel,
		proc.ts.chatID,
		proc.asyncCallback(prepared.name),
	)
	return result, time.Since(start)
}

// finish runs the post-execution pipeline for one result: AfterTool hooks,
// large-result spill, media delivery, persistence and turn bookkeeping. It
// returns abort signals, but loop detection and steering checks stay with the
// caller so they can be applied after this result is recorded.
func (proc *toolCallProcessor) finish(
	ctx context.Context,
	turnCtx context.Context,
	prepared preparedToolCall,
	result *tools.ToolResult,
	duration time.Duration,
	runAfterHook bool,
) (abort bool, hardAbort bool) {
	al := proc.al
	ts := proc.ts
	toolName := prepared.name
	toolArgs := prepared.args

	if runAfterHook && al.hooks != nil {
		toolResp, decision := al.hooks.AfterTool(turnCtx, &ToolResultHookResponse{
			Meta:      ts.eventMeta("runTurn", "turn.tool.after"),
			Context:   cloneTurnContext(ts.turnCtx),
			Tool:      toolName,
			Arguments: toolArgs,
			Result:    result,
			Duration:  duration,
		})
		switch decision.normalizedAction() {
		case HookActionContinue, HookActionModify:
			if toolResp != nil {
				if toolResp.Tool != "" {
					toolName = toolResp.Tool
				}
				if toolResp.Result != nil {
					result = toolResp.Result
				}
			}
		case HookActionAbortTurn:
			proc.exec.abortedByHook = true
			return true, false
		case HookActionHardAbort:
			_ = ts.requestHardAbort()
			proc.exec.abortedByHardAbort = true
			return false, true
		}
	}

	if result == nil {
		result = tools.ErrorResult("hook returned nil tool result")
	}

	// Re-apply the large-result spill: the registry already spilled the
	// original result, but an AfterTool hook may have replaced it with a
	// fresh oversized payload. Spilling here keeps every code path bounded.
	ts.agent.Tools.SpillLargeResult(result, toolName)

	if len(result.Media) > 0 && result.ResponseHandled {
		parts := make([]bus.MediaPart, 0, len(result.Media))
		for _, ref := range result.Media {
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
		outboundMedia := bus.OutboundMediaMessage{
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
		if al.channelManager != nil && ts.channel != "" && !constants.IsInternalChannel(ts.channel) {
			if err := al.channelManager.SendMedia(ctx, outboundMedia); err != nil {
				logger.WarnCF("agent", "Failed to deliver handled tool media",
					map[string]any{
						"agent_id": ts.agent.ID,
						"tool":     toolName,
						"channel":  ts.channel,
						"chat_id":  ts.chatID,
						"error":    err.Error(),
					})
				result = tools.ErrorResult(fmt.Sprintf("failed to deliver attachment: %v", err)).WithError(err)
			} else {
				proc.handledAttachments = append(
					proc.handledAttachments,
					buildProviderAttachments(al.mediaStore, result.Media)...,
				)
			}
		} else if al.bus != nil {
			al.bus.PublishOutboundMedia(ctx, outboundMedia)
			result.ResponseHandled = false
		}
	}

	if len(result.Media) > 0 && !result.ResponseHandled {
		result.ArtifactTags = appendArtifactTags(
			result.ArtifactTags,
			buildArtifactTags(al.mediaStore, result.Media),
		)
	}

	if !result.ResponseHandled {
		proc.exec.allResponsesHandled = false
	}

	shouldSendForUser := !result.Silent &&
		result.ForUser != "" &&
		(ts.opts.SendResponse || result.ResponseHandled)
	if shouldSendForUser {
		al.bus.PublishOutbound(ctx, outboundMessageForTurn(ts, result.ForUser))
		logger.DebugCF("agent", "Sent tool result to user",
			map[string]any{
				"tool":        toolName,
				"content_len": len(result.ForUser),
			})
	}
	contentForLLM := result.ContentForLLM()

	if al.cfg.Tools.IsFilterSensitiveDataEnabled() {
		contentForLLM = al.cfg.FilterSensitiveData(contentForLLM)
	}

	var toolResultMedia []string
	if len(result.Media) > 0 && !result.ResponseHandled {
		toolResultMedia = append(toolResultMedia, result.Media...)
	}
	toolResultMsg := toolResultPromptMessage(contentForLLM, prepared.tc.ID, toolResultMedia)
	al.emitEvent(
		runtimeevents.KindAgentToolExecEnd,
		ts.eventMeta("runTurn", "turn.tool.end"),
		ToolExecEndPayload{
			Tool:       toolName,
			Duration:   duration,
			ForLLMLen:  len(contentForLLM),
			ForUserLen: len(result.ForUser),
			IsError:    result.IsError,
			Async:      result.Async,
		},
	)
	ts.recordToolExecution(
		toolName,
		!result.IsError,
		toolErrorSummary(result),
		inferSkillNamesFromToolCall(ts, toolName, toolArgs),
	)
	proc.messages = append(proc.messages, toolResultMsg)
	if !ts.opts.NoHistory {
		ts.agent.Sessions.AddFullMessage(ts.sessionKey, toolResultMsg)
		ts.recordPersistedMessage(toolResultMsg)
		ts.ingestMessage(turnCtx, al, toolResultMsg)
	}
	return false, false
}

func (proc *toolCallProcessor) recordLoopCall(toolName string, toolArgs map[string]any) bool {
	status, count := proc.ts.recordToolCall(toolName, toolArgs)
	if status > proc.loopDetectionStatus {
		proc.loopDetectionStatus = status
		proc.loopDetectionCount = count
		proc.loopDetectionTool = toolName
	}
	return status == loopStatusCritical
}

func (proc *toolCallProcessor) skipRemaining(start int, reason, content string) {
	al := proc.al
	ts := proc.ts
	for j := start; j < len(proc.exec.normalizedToolCalls); j++ {
		skippedTC := proc.exec.normalizedToolCalls[j]
		al.emitEvent(
			runtimeevents.KindAgentToolExecSkipped,
			ts.eventMeta("runTurn", "turn.tool.skipped"),
			ToolExecSkippedPayload{Tool: skippedTC.Name, Reason: reason},
		)
		skippedMsg := providers.Message{
			Role:       "tool",
			Content:    content,
			ToolCallID: skippedTC.ID,
		}
		proc.messages = append(proc.messages, skippedMsg)
		if !ts.opts.NoHistory {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, skippedMsg)
			ts.recordPersistedMessage(skippedMsg)
		}
	}
}

func (proc *toolCallProcessor) steeringSkipReason() (string, string) {
	if len(proc.exec.pendingMessages) > 0 {
		return "queued user steering message", "Skipped due to queued user message."
	}
	if gracefulPending, _ := proc.ts.gracefulInterruptRequested(); gracefulPending {
		return "graceful interrupt requested", "Skipped due to graceful interrupt."
	}
	return "", ""
}

func (proc *toolCallProcessor) drainPendingResults() {
	ts := proc.ts
	if ts.pendingResults == nil {
		return
	}
	select {
	case result, ok := <-ts.pendingResults:
		if ok && result != nil && result.ForLLM != "" {
			content := proc.al.cfg.FilterSensitiveData(result.ForLLM)
			msg := subTurnResultPromptMessage(content)
			proc.messages = append(proc.messages, msg)
			if !ts.opts.NoHistory {
				ts.agent.Sessions.AddFullMessage(ts.sessionKey, msg)
			}
		}
	default:
	}
}
