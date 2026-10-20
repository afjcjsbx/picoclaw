package messageutil

import (
	"slices"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

// IsTransientAssistantThoughtMessage reports whether msg is an invalid
// reasoning-only assistant history record. These "hanging" thought messages
// are not a canonical persisted format and should be discarded instead of
// replayed or reconstructed.
func IsTransientAssistantThoughtMessage(msg protocoltypes.Message) bool {
	return msg.Role == "assistant" &&
		strings.TrimSpace(msg.Content) == "" &&
		strings.TrimSpace(msg.ReasoningContent) != "" &&
		len(msg.ToolCalls) == 0 &&
		len(msg.Media) == 0 &&
		len(msg.Attachments) == 0 &&
		strings.TrimSpace(msg.ToolCallID) == ""
}

// FilterInvalidHistoryMessages removes invalid persisted history records such
// as transient assistant thought-only messages.
func FilterInvalidHistoryMessages(history []protocoltypes.Message) []protocoltypes.Message {
	if len(history) == 0 {
		return []protocoltypes.Message{}
	}

	filtered := make([]protocoltypes.Message, 0, len(history))
	for _, msg := range history {
		if IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

// CloneHistory returns a fully isolated copy of history. The slice header, the
// message structs and every nested field a caller could otherwise share with
// the stored history (CreatedAt, Media, Attachments, SystemParts, ToolCalls
// including function, arguments and extra content) are cloned. An empty input
// yields a non-nil empty slice.
func CloneHistory(history []protocoltypes.Message) []protocoltypes.Message {
	if len(history) == 0 {
		return []protocoltypes.Message{}
	}

	cloned := make([]protocoltypes.Message, len(history))
	for i, msg := range history {
		cloned[i] = cloneMessage(msg)
	}
	return cloned
}

func cloneMessage(msg protocoltypes.Message) protocoltypes.Message {
	cloned := msg

	if msg.CreatedAt != nil {
		createdAt := *msg.CreatedAt
		cloned.CreatedAt = &createdAt
	}
	if len(msg.Media) > 0 {
		cloned.Media = slices.Clone(msg.Media)
	}
	if len(msg.Attachments) > 0 {
		cloned.Attachments = slices.Clone(msg.Attachments)
	}
	if len(msg.SystemParts) > 0 {
		cloned.SystemParts = make([]protocoltypes.ContentBlock, len(msg.SystemParts))
		for i, part := range msg.SystemParts {
			cloned.SystemParts[i] = part
			if part.CacheControl != nil {
				cacheControl := *part.CacheControl
				cloned.SystemParts[i].CacheControl = &cacheControl
			}
		}
	}
	if len(msg.ToolCalls) > 0 {
		cloned.ToolCalls = cloneToolCalls(msg.ToolCalls)
	}
	return cloned
}

func cloneToolCalls(calls []protocoltypes.ToolCall) []protocoltypes.ToolCall {
	cloned := make([]protocoltypes.ToolCall, len(calls))
	for i, call := range calls {
		cloned[i] = call
		if call.Function != nil {
			fn := *call.Function
			cloned[i].Function = &fn
		}
		if call.Arguments != nil {
			cloned[i].Arguments = cloneArguments(call.Arguments)
		}
		if call.ExtraContent != nil {
			extra := *call.ExtraContent
			if call.ExtraContent.Google != nil {
				google := *call.ExtraContent.Google
				extra.Google = &google
			}
			cloned[i].ExtraContent = &extra
		}
	}
	return cloned
}

// cloneArguments recursively copies maps and slices so nested structures in
// tool call arguments are not shared. Scalar values are immutable and shared.
func cloneArguments(src map[string]any) map[string]any {
	cloned := make(map[string]any, len(src))
	for key, value := range src {
		cloned[key] = cloneArgumentValue(value)
	}
	return cloned
}

func cloneArgumentValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if typed == nil {
			return nil
		}
		return cloneArguments(typed)
	case []any:
		if typed == nil {
			return nil
		}
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = cloneArgumentValue(item)
		}
		return cloned
	default:
		return value
	}
}
