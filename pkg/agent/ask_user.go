package agent

import (
	"context"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent/interfaces"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/tools"
	askusertools "github.com/sipeed/picoclaw/pkg/tools/askuser"
)

// newAskUserTool builds the agent-facing ask_user tool. The adapter renders
// questions in the current chat and waits for the next inbound message through
// the loop's ask_user broker.
func (al *AgentLoop) newAskUserTool(msgBus interfaces.MessageBus) *askusertools.Tool {
	timeout := askusertools.DefaultTimeout
	if al.cfg != nil {
		timeout = al.cfg.Tools.AskUser.GetTimeout()
	}
	adapter := askusertools.NewChatAdapter(
		al.askUserBroker,
		func(ctx context.Context, channel, chatID, content string) error {
			outboundCtx := bus.NewOutboundContext(channel, chatID, "")
			outboundAgentID, outboundSessionKey, outboundScope := outboundTurnMetadata(
				tools.ToolAgentID(ctx),
				tools.ToolSessionKey(ctx),
				tools.ToolSessionScope(ctx),
			)
			message := bus.OutboundMessage{
				Channel:    channel,
				ChatID:     chatID,
				Context:    outboundCtx,
				AgentID:    outboundAgentID,
				SessionKey: outboundSessionKey,
				Scope:      outboundScope,
				Content:    content,
			}
			if al.channelManager != nil && channel != "" {
				return al.channelManager.SendMessage(ctx, message)
			}
			publishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return msgBus.PublishOutbound(publishCtx, message)
		},
		timeout,
	)
	return askusertools.NewTool(adapter)
}

// deliverAskUserReply routes the current inbound message into a waiting
// ask_user interaction for the same conversation. It returns true when the
// message was consumed and must not start or steer a normal turn.
func (al *AgentLoop) deliverAskUserReply(sessionKey string, msg bus.InboundMessage) bool {
	al.mu.RLock()
	broker := al.askUserBroker
	al.mu.RUnlock()
	if broker == nil {
		return false
	}
	return broker.Deliver(askusertools.BrokerKey(msg.Channel, msg.ChatID, sessionKey), msg.Content)
}
