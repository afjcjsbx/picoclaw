package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func buildPicoToolCallsMessage(
	msg bus.OutboundMessage,
	visibleToolCalls []utils.VisibleToolCall,
) (bus.OutboundMessage, error) {
	rawToolCalls, err := json.Marshal(visibleToolCalls)
	if err != nil {
		return bus.OutboundMessage{}, err
	}
	if msg.Context.Raw == nil {
		msg.Context.Raw = make(map[string]string, 2)
	}
	msg.Context.Raw[metadataKeyMessageKind] = messageKindToolCalls
	msg.Context.Raw[metadataKeyToolCalls] = string(rawToolCalls)
	return msg, nil
}

func buildPicoToolResultMessage(
	msg bus.OutboundMessage,
	toolCallID, output string,
) bus.OutboundMessage {
	msg.Content = output
	if msg.Context.Raw == nil {
		msg.Context.Raw = make(map[string]string, 2)
	}
	msg.Context.Raw[metadataKeyMessageKind] = messageKindToolResult
	msg.Context.Raw[metadataKeyToolCallID] = toolCallID
	return msg
}

func publishOutboundToolActivity(
	ctx context.Context,
	al *AgentLoop,
	msg bus.OutboundMessage,
) error {
	if al == nil || al.bus == nil {
		return nil
	}
	pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
	defer pubCancel()
	return al.bus.PublishOutbound(pubCtx, msg)
}
