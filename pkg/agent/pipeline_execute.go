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

// ExecuteTools executes the tool loop, handling BeforeTool/ApproveTool/AfterTool hooks,
// tool execution with async callbacks, media delivery, and steering injection.
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
	batch := &toolBatch{
		al:        al,
		ctx:       ctx,
		turnCtx:   turnCtx,
		ts:        ts,
		exec:      exec,
		iteration: iteration,
		calls:     normalizedToolCalls,
		messages:  exec.messages,
		feedback:  newToolFeedbackBatch(al, ts, exec),
	}

	maxParallel := al.cfg.Agents.Defaults.MaxParallelToolCalls
	segments := planToolBatch(ts, normalizedToolCalls, maxParallel)

toolLoop:
	for _, seg := range segments {
		var step toolStep
		if seg.Parallel {
			step = batch.runParallel(seg.Calls, maxParallel)
		} else {
			for _, i := range seg.Calls {
				if step = batch.runOne(i); step != toolStepContinue {
					break
				}
			}
		}
		switch step {
		case toolStepBreak:
			return ToolControlBreak
		case toolStepStop:
			break toolLoop
		}
	}

	messages := batch.messages
	handledAttachments := batch.handledAttachments
	loopDetectionStatus := batch.loopStatus
	loopDetectionCount := batch.loopCount
	loopDetectionTool := batch.loopTool
	exec.messages = messages
	if loopDetectionStatus == loopStatusCritical {
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
				"tool":     loopDetectionTool,
				"repeats":  loopDetectionCount,
			})
		exec.allResponsesHandled = false
		exec.finalContent = loopDetectionResponse
		return ToolControlBreak
	}
	if loopDetectionStatus == loopStatusWarning {
		warning := providers.Message{
			Role: "system",
			Content: fmt.Sprintf(
				"[Loop Warning] Tool %q was called %d consecutive times with identical arguments. If you are stuck, try a different approach.",
				loopDetectionTool,
				loopDetectionCount,
			),
		}
		// The warning is deliberately turn-local: it guides the next LLM call but
		// is not persisted into session history after the loop has been resolved.
		messages = append(messages, warning)
		exec.loopWarnings = append(exec.loopWarnings, warning)
		exec.messages = messages
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
			Attachments: append([]providers.Attachment(nil), handledAttachments...),
		}
		if !ts.opts.NoHistory {
			warnSessionStoreError(
				ts.sessionKey,
				"add full message",
				ts.agent.Sessions.AddFullMessage(ctx, ts.sessionKey, summaryMsg),
			)
			ts.recordPersistedMessage(summaryMsg)
			ts.ingestMessage(turnCtx, al, summaryMsg)
			if err := ts.agent.Sessions.Save(ctx, ts.sessionKey); err != nil {
				logger.WarnCF("agent", "Failed to save session after tool delivery",
					map[string]any{
						"agent_id": ts.agent.ID,
						"error":    err.Error(),
					})
			}
		}
		if !ts.opts.NoHistory && ts.opts.EnableSummary {
			al.contextManager.Compact(turnCtx, &CompactRequest{
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
