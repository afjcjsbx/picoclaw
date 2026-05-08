package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type pendingAskUserCall struct {
	ToolCallID string
	Question   string
	Options    []string
}

func findPendingAskUserCall(history []providers.Message) *pendingAskUserCall {
	if len(history) == 0 {
		return nil
	}

	pending := make(map[string]providers.ToolCall)
	order := make([]string, 0)

	for _, message := range history {
		switch message.Role {
		case "assistant":
			for _, toolCall := range message.ToolCalls {
				toolCallID := strings.TrimSpace(toolCall.ID)
				if toolCallID == "" {
					continue
				}
				if _, exists := pending[toolCallID]; !exists {
					order = append(order, toolCallID)
				}
				pending[toolCallID] = toolCall
			}
		case "tool":
			toolCallID := strings.TrimSpace(message.ToolCallID)
			if toolCallID == "" {
				continue
			}
			delete(pending, toolCallID)
		}
	}

	for i := len(order) - 1; i >= 0; i-- {
		toolCallID := order[i]
		toolCall, ok := pending[toolCallID]
		if !ok {
			continue
		}
		if toolCallName(toolCall) != tools.AskUserToolName {
			continue
		}
		args := toolCallArguments(toolCall)
		question, _ := args["question"].(string)
		question = strings.TrimSpace(question)
		if question == "" {
			question = "Please reply to continue."
		}
		return &pendingAskUserCall{
			ToolCallID: toolCallID,
			Question:   question,
			Options:    normalizePendingAskUserOptions(args["options"]),
		}
	}

	return nil
}

func toolCallName(toolCall providers.ToolCall) string {
	if toolCall.Function != nil {
		if name := strings.TrimSpace(toolCall.Function.Name); name != "" {
			return name
		}
	}
	return strings.TrimSpace(toolCall.Name)
}

func toolCallArguments(toolCall providers.ToolCall) map[string]any {
	if len(toolCall.Arguments) > 0 {
		return cloneStringAnyMap(toolCall.Arguments)
	}
	if toolCall.Function == nil {
		return map[string]any{}
	}

	raw := strings.TrimSpace(toolCall.Function.Arguments)
	if raw == "" {
		return map[string]any{}
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]any{}
	}
	return parsed
}

func normalizePendingAskUserOptions(raw any) []string {
	var options []string
	switch values := raw.(type) {
	case []string:
		options = append(options, values...)
	case []any:
		options = make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				continue
			}
			options = append(options, text)
		}
	default:
		return nil
	}

	normalized := options[:0]
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		normalized = append(normalized, option)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizePendingAskUserReply(content string, options []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(options) == 0 {
		return content
	}

	index, err := strconv.Atoi(content)
	if err != nil {
		return content
	}
	if index < 1 || index > len(options) {
		return content
	}
	return options[index-1]
}

func (al *AgentLoop) resumePendingAskUser(
	ctx context.Context,
	agent *AgentInstance,
	msg bus.InboundMessage,
	opts *processOptions,
	pending *pendingAskUserCall,
) (string, error) {
	if agent == nil || opts == nil || pending == nil {
		return "", fmt.Errorf("invalid ask_user resume state")
	}

	replyContent := normalizePendingAskUserReply(msg.Content, pending.Options)
	if replyContent == "" {
		return "", fmt.Errorf("ask_user reply requires non-empty text input")
	}

	toolResultMsg := providers.Message{
		Role:       "tool",
		Content:    replyContent,
		ToolCallID: pending.ToolCallID,
	}

	agent.Sessions.AddFullMessage(opts.Dispatch.SessionKey, toolResultMsg)
	if al.contextManager != nil {
		if err := al.contextManager.Ingest(ctx, &IngestRequest{
			SessionKey: opts.Dispatch.SessionKey,
			Message:    toolResultMsg,
		}); err != nil {
			logger.WarnCF("agent", "Context manager ingest failed for ask_user reply", map[string]any{
				"session_key": opts.Dispatch.SessionKey,
				"error":       err.Error(),
			})
		}
	}
	if err := agent.Sessions.Save(opts.Dispatch.SessionKey); err != nil {
		return "", err
	}

	opts.Dispatch.UserMessage = ""
	opts.Dispatch.Media = nil
	opts.UserMessage = ""
	opts.Media = nil

	logger.InfoCF("agent", "Resuming pending ask_user tool call",
		map[string]any{
			"agent_id":     agent.ID,
			"session_key":  opts.Dispatch.SessionKey,
			"tool_call_id": pending.ToolCallID,
			"content_len":  len(replyContent),
		})

	return al.runAgentLoop(ctx, agent, *opts)
}
