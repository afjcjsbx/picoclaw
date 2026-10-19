// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func toolErrorSummary(result *tools.ToolResult) string {
	if result == nil || !result.IsError {
		return ""
	}
	content := strings.TrimSpace(result.ContentForLLM())
	if content == "" && result.Err != nil {
		content = strings.TrimSpace(result.Err.Error())
	}
	return utils.Truncate(content, 200)
}

func inferSkillNamesFromToolCall(ts *turnState, toolName string, toolArgs map[string]any) []string {
	if ts == nil || toolName != "read_file" {
		return nil
	}

	rawPath, ok := toolArgs["path"].(string)
	if !ok {
		return nil
	}
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return nil
	}

	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(ts.workspace, cleanPath)
	}
	if filepath.Base(cleanPath) != "SKILL.md" {
		return nil
	}

	var roots []string
	if ts.agent != nil && ts.agent.ContextBuilder != nil {
		roots = ts.agent.ContextBuilder.skillRoots()
	}
	if len(roots) == 0 && strings.TrimSpace(ts.workspace) != "" {
		roots = []string{filepath.Join(ts.workspace, "skills")}
	}

	found := make(map[string]struct{})
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), cleanPath)
		if err != nil {
			continue
		}
		if rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 2 || parts[1] != "SKILL.md" {
			continue
		}

		skillName := strings.TrimSpace(parts[0])
		if skillName == "" {
			continue
		}
		if ts.agent != nil && ts.agent.ContextBuilder != nil {
			if canonical, ok := ts.agent.ContextBuilder.ResolveSkillName(skillName); ok {
				skillName = canonical
			}
		}
		found[skillName] = struct{}{}
	}

	if len(found) == 0 {
		return nil
	}

	names := make([]string, 0, len(found))
	for skillName := range found {
		names = append(names, skillName)
	}
	sort.Strings(names)
	return names
}

// ExecuteTools executes the tool calls of one assistant response. Calls are
// normally executed sequentially in model order. Consecutive calls to tools
// that implement tools.ConcurrencySafe are executed concurrently with a
// bounded fan-out; their results are still finalized in model order.
//
// Returns ToolControl indicating what the coordinator should do next:
//   - ToolControlContinue: all tool results handled, pendingMessages or steering exists, continue turn
//   - ToolControlBreak: tool loop exited, proceed to coordinator's hardAbort/finalContent/finalize
func (p *Pipeline) ExecuteTools(
	ctx context.Context,
	turnCtx context.Context,
	ts *turnState,
	exec *turnExecution,
	iteration int,
) ToolControl {
	al := p.al
	normalizedToolCalls := exec.normalizedToolCalls

	ts.setPhase(TurnPhaseTools)
	proc := newToolCallProcessor(p, ts, exec, iteration)
	defer func() {
		exec.messages = proc.messages
	}()

	for i := 0; i < len(normalizedToolCalls); {
		if ts.hardAbortRequested() {
			exec.abortedByHardAbort = true
			return ToolControlBreak
		}

		// A group is a run of consecutive calls whose registered tool opted
		// into concurrent execution. A side-effectful tool in the middle
		// splits the batch, keeping its ordering with respect to both sides.
		groupEnd := i + 1
		if proc.isConcurrencySafe(normalizedToolCalls[i].Name) {
			for groupEnd < len(normalizedToolCalls) && proc.isConcurrencySafe(normalizedToolCalls[groupEnd].Name) {
				groupEnd++
			}
		}
		if groupEnd-i > 1 {
			outcome := proc.runGroup(ctx, turnCtx, i, groupEnd, iteration)
			i = outcome.next
			if outcome.abort || outcome.hardAbort {
				return ToolControlBreak
			}
			if outcome.breakLoop {
				if outcome.criticalLoop {
					proc.skipRemaining(
						i,
						"critical repeated tool-call loop detected",
						"Skipped because a repeated tool-call loop was detected.",
					)
				} else if outcome.steeringSkipReason != "" {
					proc.skipRemaining(i, outcome.steeringSkipReason, outcome.steeringSkipContent)
				}
				break
			}

			// Steering can arrive while the batch runs. The batch itself is
			// already finished, but the calls after it must still be skipped.
			if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
				exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
			}
			if reason, content := proc.steeringSkipReason(); reason != "" {
				proc.skipRemaining(i, reason, content)
				break
			}
			continue
		}

		prepared := proc.prepare(turnCtx, i, normalizedToolCalls[i], iteration)
		switch prepared.action {
		case toolCallActionAbortTurn:
			exec.abortedByHook = true
			return ToolControlBreak
		case toolCallActionHardAbort:
			_ = ts.requestHardAbort()
			exec.abortedByHardAbort = true
			return ToolControlBreak
		case toolCallActionSkip:
			proc.appendSkip(prepared)
			i++
			continue
		case toolCallActionRespond:
			abort, hardAbort := proc.finish(ctx, turnCtx, prepared, prepared.result, 0, false)
			if abort || hardAbort {
				return ToolControlBreak
			}
		case toolCallActionExecute:
			result, duration := proc.execute(turnCtx, prepared)

			if ts.hardAbortRequested() {
				exec.abortedByHardAbort = true
				return ToolControlBreak
			}

			abort, hardAbort := proc.finish(ctx, turnCtx, prepared, result, duration, true)
			if abort || hardAbort {
				return ToolControlBreak
			}
		}

		if proc.recordLoopCall(prepared.name, prepared.args) {
			proc.skipRemaining(
				i+1,
				"critical repeated tool-call loop detected",
				"Skipped because a repeated tool-call loop was detected.",
			)
			break
		}

		if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
			exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
		}

		if reason, content := proc.steeringSkipReason(); reason != "" {
			proc.skipRemaining(i+1, reason, content)
			break
		}

		proc.drainPendingResults()
		i++
	}

	if proc.loopDetectionStatus == loopStatusCritical {
		// A steering message that arrived while the repeated tool was running
		// takes precedence over loop termination: the user asked for a change
		// of direction, so let the model react to it. The loop guard still
		// applies on the next iteration if the model keeps repeating the call.
		if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
			logger.InfoCF("agent", "Steering arrived during critical loop detection; continuing turn",
				map[string]any{
					"agent_id":       ts.agent.ID,
					"steering_count": len(steerMsgs),
				})
			exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
			exec.allResponsesHandled = false
			return ToolControlContinue
		}
		logger.WarnCF("agent", "Critical repeated tool-call loop detected; stopping turn",
			map[string]any{
				"agent_id": ts.agent.ID,
				"turn_id":  ts.turnID,
				"tool":     proc.loopDetectionTool,
				"repeats":  proc.loopDetectionCount,
			})
		exec.allResponsesHandled = false
		exec.finalContent = loopDetectionResponse
		return ToolControlBreak
	}
	if proc.loopDetectionStatus == loopStatusWarning {
		warning := providers.Message{
			Role: "system",
			Content: fmt.Sprintf(
				"[Loop Warning] Tool %q was called %d consecutive times with identical arguments. If you are stuck, try a different approach.",
				proc.loopDetectionTool,
				proc.loopDetectionCount,
			),
		}
		// The warning is deliberately turn-local: it guides the next LLM call but
		// is not persisted into session history after the loop has been resolved.
		proc.messages = append(proc.messages, warning)
		exec.loopWarnings = append(exec.loopWarnings, warning)
	}

	// Continue if pending steering exists (regardless of allResponsesHandled).
	// This covers the case where tools were partially executed and skipped due to steering,
	// but one tool had ResponseHandled=false (so allResponsesHandled=false).
	if len(exec.pendingMessages) > 0 {
		logger.InfoCF("agent", "Pending steering after partial tool execution; continuing turn",
			map[string]any{
				"agent_id":            ts.agent.ID,
				"pending_count":       len(exec.pendingMessages),
				"allResponsesHandled": exec.allResponsesHandled,
			})
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// Poll for newly arrived steering
	if steerMsgs := al.dequeueSteeringMessagesForScope(ts.sessionKey); len(steerMsgs) > 0 {
		logger.InfoCF("agent", "Steering arrived after tool delivery; continuing turn",
			map[string]any{
				"agent_id":       ts.agent.ID,
				"steering_count": len(steerMsgs),
			})
		exec.pendingMessages = append(exec.pendingMessages, steerMsgs...)
		exec.allResponsesHandled = false
		return ToolControlContinue
	}

	// No pending steering: finalize or break depending on allResponsesHandled
	if exec.allResponsesHandled {
		summaryMsg := providers.Message{
			Role:        "assistant",
			Content:     handledToolResponseSummary,
			Attachments: append([]providers.Attachment(nil), proc.handledAttachments...),
		}
		if !ts.opts.NoHistory {
			ts.agent.Sessions.AddFullMessage(ts.sessionKey, summaryMsg)
			ts.recordPersistedMessage(summaryMsg)
			ts.ingestMessage(turnCtx, al, summaryMsg)
			if err := ts.agent.Sessions.Save(ts.sessionKey); err != nil {
				logger.WarnCF("agent", "Failed to save session after tool delivery",
					map[string]any{
						"agent_id": ts.agent.ID,
						"error":    err.Error(),
					})
			}
		}
		if !ts.opts.NoHistory && ts.opts.EnableSummary {
			al.contextManager.Compact(ctx, &CompactRequest{
				SessionKey: ts.sessionKey,
				Reason:     ContextCompressReasonSummarize,
				Budget:     ts.agent.ContextWindow,
			})
		}
		ts.setPhase(TurnPhaseCompleted)
		ts.setFinalContent("")
		if al.channelManager != nil && ts.channel != "" {
			al.channelManager.DismissToolFeedback(ctx, ts.channel, ts.chatID, ts.opts.InboundContext)
		}
		logger.InfoCF("agent", "Tool output satisfied delivery; ending turn without follow-up LLM",
			map[string]any{
				"agent_id":   ts.agent.ID,
				"iteration":  iteration,
				"tool_count": len(normalizedToolCalls),
			})
		return ToolControlBreak
	}

	// allResponsesHandled=false and no pending steering: continue so coordinator
	// makes another LLM call. The tool result is in messages and the LLM will
	// return it as finalContent in the next iteration.
	//
	// Discovery TTL is intentionally NOT ticked here: it advances once per
	// conversational turn in runTurn, so a tool discovered in this turn stays
	// callable across the remaining tool-loop iterations of the same turn.
	return ToolControlContinue
}
