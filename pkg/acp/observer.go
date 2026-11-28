package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const maxToolPayloadBytes = 64 << 10

func (r *Runtime) OnText(ctx context.Context, sessionID, turnID, content string) error {
	state := r.sessionByID(sessionID)
	if state == nil {
		return nil
	}
	turn, emit := r.outputTurn(state, turnID)
	if !emit {
		return nil
	}
	messageID := uuid.NewString()
	if turn != nil {
		turn.mu.Lock()
		if turn.textMessageID == "" {
			turn.textMessageID = messageID
		}
		messageID = turn.textMessageID
		turn.text += content
		turn.streamed = true
		turn.mu.Unlock()
	}
	return r.emitMessageChunk(ctx, sessionID, "agent_message_chunk", messageID, content)
}

func (r *Runtime) OnThought(ctx context.Context, sessionID, turnID, content string) error {
	state := r.sessionByID(sessionID)
	if state == nil {
		return nil
	}
	turn, emit := r.outputTurn(state, turnID)
	if !emit {
		return nil
	}
	messageID := uuid.NewString()
	if turn != nil {
		turn.mu.Lock()
		if turn.thoughtID == "" {
			turn.thoughtID = messageID
		}
		messageID = turn.thoughtID
		turn.thought += content
		turn.mu.Unlock()
	}
	return r.emitMessageChunk(ctx, sessionID, "agent_thought_chunk", messageID, content)
}

func (r *Runtime) OnToolCalls(ctx context.Context, sessionID, turnID, rawCalls string) error {
	state := r.sessionByID(sessionID)
	if state == nil {
		return nil
	}
	if _, emit := r.outputTurn(state, turnID); !emit {
		return nil
	}
	var calls []utils.VisibleToolCall
	if err := json.Unmarshal([]byte(rawCalls), &calls); err != nil {
		return fmt.Errorf("decode tool calls: %w", err)
	}
	for _, call := range calls {
		name := ""
		argsJSON := strings.TrimSpace(call.Input)
		if call.Function != nil {
			name = strings.TrimSpace(call.Function.Name)
			if argsJSON == "" {
				argsJSON = strings.TrimSpace(call.Function.Arguments)
			}
		}
		if name == "" {
			continue
		}
		toolCallID := strings.TrimSpace(call.ID)
		if toolCallID == "" {
			toolCallID = uuid.NewString()
		}
		update := ToolCallUpdate{
			ToolCallID: toolCallID,
			Title:      toolTitle(name, argsJSON),
			Name:       name,
			Kind:       toolKind(name),
			Status:     "pending",
			RawInput:   sanitizedToolValue(argsJSON),
		}
		if err := r.peer.Notify("session/update", SessionNotification{
			SessionID: sessionID,
			Update: map[string]any{
				"sessionUpdate": "tool_call",
				"toolCallId":    update.ToolCallID,
				"title":         update.Title,
				"name":          update.Name,
				"kind":          update.Kind,
				"status":        update.Status,
				"rawInput":      update.RawInput,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) OnToolResult(ctx context.Context, sessionID, turnID, toolCallID, output string, isError bool) error {
	state := r.sessionByID(sessionID)
	if state == nil || strings.TrimSpace(toolCallID) == "" {
		return nil
	}
	if _, emit := r.outputTurn(state, turnID); !emit {
		return nil
	}
	content := boundedRedactedText(output, maxToolPayloadBytes)
	status := "completed"
	if isError {
		status = "failed"
	}
	return r.peer.Notify("session/update", SessionNotification{
		SessionID: sessionID,
		Update: map[string]any{
			"sessionUpdate": "tool_call_update",
			"toolCallId":    toolCallID,
			"status":        status,
			"content":       []ToolCallContent{TextToolContent(content)},
			"rawOutput":     content,
		},
	})
}

func (r *Runtime) OnFinal(ctx context.Context, sessionID, turnID, content string, msg bus.OutboundMessage) error {
	state := r.sessionByID(sessionID)
	if state == nil {
		return nil
	}
	turn, emit := r.outputTurn(state, turnID)
	if !emit {
		return nil
	}

	if turn != nil {
		turn.mu.Lock()
		streamed := turn.streamed
		streamedText := turn.text
		messageID := turn.textMessageID
		turn.mu.Unlock()
		if !streamed || streamedText != content {
			if messageID == "" {
				messageID = uuid.NewString()
			}
			if content != "" {
				if err := r.emitMessageChunk(ctx, sessionID, "agent_message_chunk", messageID, content); err != nil {
					return err
				}
			}
		}
		if err := r.emitUsage(ctx, state, msg); err != nil {
			return err
		}
		turn.finish(turn.completionStopReason(), nil)
		return nil
	}

	if content != "" {
		if err := r.emitMessageChunk(ctx, sessionID, "agent_message_chunk", uuid.NewString(), content); err != nil {
			return err
		}
	}
	return r.emitUsage(ctx, state, msg)
}

func (r *Runtime) emitMessageChunk(ctx context.Context, sessionID, kind, messageID, content string) error {
	if content == "" {
		return nil
	}
	return r.peer.Notify("session/update", SessionNotification{
		SessionID: sessionID,
		Update: map[string]any{
			"sessionUpdate": kind,
			"messageId":     messageID,
			"content":       TextContent(content),
		},
	})
}

func (r *Runtime) emitUsage(ctx context.Context, state *acpSession, msg bus.OutboundMessage) error {
	if msg.ContextUsage == nil || msg.ContextUsage.TotalTokens <= 0 {
		return nil
	}
	used := int64(msg.ContextUsage.UsedTokens)
	if used == 0 && msg.Usage != nil {
		used = int64(msg.Usage.ContextTokens)
	}
	cost := 0.0
	if msg.Usage != nil {
		cost = msg.Usage.CostUSD
	}
	state.mu.Lock()
	state.cumulativeCost += cost
	cumulative := state.cumulativeCost
	state.mu.Unlock()
	update := UsageUpdate{Used: used, Size: int64(msg.ContextUsage.TotalTokens)}
	if cumulative > 0 {
		update.Cost = &Cost{Amount: cumulative, Currency: "USD"}
	}
	return r.peer.Notify("session/update", SessionNotification{
		SessionID: state.id,
		Update: map[string]any{
			"sessionUpdate": "usage_update",
			"used":          update.Used,
			"size":          update.Size,
			"cost":          update.Cost,
		},
	})
}

func (r *Runtime) handleTurnEnd(_ context.Context, event runtimeevents.Event) error {
	state := r.sessionByKey(event.Scope.SessionKey)
	if state == nil {
		return nil
	}
	turn := state.activeTurn()
	if turn == nil {
		return nil
	}
	if event.Kind == runtimeevents.KindAgentLLMResponse {
		var finishReason string
		switch payload := event.Payload.(type) {
		case agent.LLMResponsePayload:
			finishReason = payload.FinishReason
		case *agent.LLMResponsePayload:
			if payload != nil {
				finishReason = payload.FinishReason
			}
		}
		turn.mu.Lock()
		turn.finishReason = finishReason
		turn.mu.Unlock()
		return nil
	}
	var status agent.TurnEndStatus
	switch payload := event.Payload.(type) {
	case agent.TurnEndPayload:
		status = payload.Status
	case *agent.TurnEndPayload:
		if payload != nil {
			status = payload.Status
		}
	}
	switch status {
	case agent.TurnEndStatusAborted:
		turn.canceled.Store(true)
		turn.finish(StopReasonCancelled, nil)
	case agent.TurnEndStatusError:
		turn.finish("", fmt.Errorf("agent turn failed"))
	}
	return nil
}

func (r *Runtime) outputTurn(state *acpSession, turnID string) (*acpTurn, bool) {
	active := state.activeTurn()
	if active != nil && (turnID == "" || active.id == turnID) {
		return active, true
	}
	if turnID != "" && state.isCancelledTurn(turnID) {
		return nil, false
	}
	return nil, true
}

func toolTitle(name, argsJSON string) string {
	var args map[string]any
	if json.Unmarshal([]byte(argsJSON), &args) == nil {
		for _, key := range []string{"command", "path", "file_path", "query", "url"} {
			if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
				return name + ": " + boundedRedactedText(value, 240)
			}
		}
	}
	return name
}

func toolKind(name string) string {
	switch strings.ToLower(name) {
	case "exec", "shell":
		return "execute"
	case "read_file", "list_files", "read_file_lines":
		return "read"
	case "search_files", "glob", "grep":
		return "search"
	case "write_file", "edit_file", "apply_patch":
		return "edit"
	case "delete_file":
		return "delete"
	case "move_file", "rename_file":
		return "move"
	case "web_search", "web_fetch", "browser":
		return "fetch"
	case "todo":
		return "think"
	default:
		return "other"
	}
}

func sanitizedToolValue(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		value = raw
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	redacted := fstools.RedactSecrets(string(payload))
	if len(redacted) <= maxToolPayloadBytes {
		var result any
		if json.Unmarshal([]byte(redacted), &result) == nil {
			return result
		}
		return redacted
	}
	return redacted[:maxToolPayloadBytes]
}

func boundedRedactedText(text string, limit int) string {
	redacted := fstools.RedactSecrets(text)
	if len(redacted) <= limit {
		return redacted
	}
	return redacted[:limit] + "\n[truncated]"
}
