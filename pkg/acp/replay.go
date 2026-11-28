package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func (r *Runtime) replaySession(ctx context.Context, state *acpSession, history []providers.Message) error {
	for index, message := range history {
		messageID := fmt.Sprintf("%s-message-%d", state.id, index)
		switch message.Role {
		case "user":
			if err := r.emitMessageChunk(ctx, state.id, "user_message_chunk", messageID, message.Content); err != nil {
				return err
			}
		case "assistant":
			if message.ReasoningContent != "" && message.ReasoningContent != message.Content {
				if err := r.emitMessageChunk(
					ctx,
					state.id,
					"agent_thought_chunk",
					messageID+"-thought",
					message.ReasoningContent,
				); err != nil {
					return err
				}
			}
			if err := r.emitMessageChunk(ctx, state.id, "agent_message_chunk", messageID, message.Content); err != nil {
				return err
			}
			for toolIndex, toolCall := range message.ToolCalls {
				if err := r.replayToolCall(
					ctx,
					state.id,
					toolCall,
					fmt.Sprintf("%s-tool-%d", messageID, toolIndex),
				); err != nil {
					return err
				}
			}
		case "tool":
			if strings.TrimSpace(message.ToolCallID) == "" {
				continue
			}
			content := boundedRedactedText(message.Content, maxToolPayloadBytes)
			if err := r.peer.Notify("session/update", SessionNotification{
				SessionID: state.id,
				Update: map[string]any{
					"sessionUpdate": "tool_call_update",
					"toolCallId":    message.ToolCallID,
					"status":        "completed",
					"content":       []ToolCallContent{TextToolContent(content)},
					"rawOutput":     content,
				},
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) replayToolCall(
	ctx context.Context,
	sessionID string,
	toolCall providers.ToolCall,
	fallbackID string,
) error {
	toolCallID := strings.TrimSpace(toolCall.ID)
	if toolCallID == "" {
		toolCallID = fallbackID
	}
	name := ""
	argsJSON := ""
	if toolCall.Function != nil {
		name = strings.TrimSpace(toolCall.Function.Name)
		argsJSON = strings.TrimSpace(toolCall.Function.Arguments)
	}
	if name == "" {
		name = strings.TrimSpace(toolCall.Name)
	}
	if argsJSON == "" && len(toolCall.Arguments) > 0 {
		argsJSON = string(mustMarshalToolArguments(toolCall.Arguments))
	}
	if name == "" {
		return nil
	}
	return r.peer.Notify("session/update", SessionNotification{
		SessionID: sessionID,
		Update: map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    toolCallID,
			"title":         toolTitle(name, argsJSON),
			"name":          name,
			"kind":          toolKind(name),
			"status":        "pending",
			"rawInput":      sanitizedToolValue(argsJSON),
		},
	})
}

func mustMarshalToolArguments(arguments map[string]any) []byte {
	payload, _ := json.Marshal(arguments)
	return payload
}
