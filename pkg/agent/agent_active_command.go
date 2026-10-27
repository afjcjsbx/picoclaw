// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const activeTurnCommandTimeout = 30 * time.Second

// tryHandleActiveTurnCommand executes registered read-only commands without
// interrupting the turn that currently owns the session. Mutating commands are
// rejected instead of being queued as steering messages.
func (al *AgentLoop) tryHandleActiveTurnCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	sessionKey, agentID string,
) bool {
	cmdName, ok := commands.CommandName(msg.Content)
	if !ok || cmdName == "stop" {
		return false
	}
	if al.cmdRegistry == nil {
		return false
	}

	def, found := al.cmdRegistry.Lookup(cmdName)
	if !found {
		// Unknown slash-prefixed text keeps the idle passthrough behavior, so
		// during an active turn it remains a normal steering message.
		return false
	}
	if !def.ReadOnly {
		al.publishActiveTurnCommandReply(
			ctx,
			msg,
			sessionKey,
			fmt.Sprintf(
				"Command /%s is unavailable while a task is running. Use /stop to cancel the current task first.",
				def.Name,
			),
		)
		return true
	}

	go al.runActiveTurnCommand(ctx, msg, sessionKey, agentID)
	return true
}

func (al *AgentLoop) runActiveTurnCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	sessionKey, agentID string,
) {
	agent, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || agent == nil {
		agent = al.GetRegistry().GetDefaultAgent()
	}
	if agent == nil {
		al.publishActiveTurnCommandReply(
			ctx,
			msg,
			sessionKey,
			"Command unavailable: no agent is registered for this session.",
		)
		return
	}

	opts := processOptions{
		Dispatch: DispatchRequest{
			SessionKey:     sessionKey,
			InboundContext: cloneInboundContext(&msg.Context),
			UserMessage:    msg.Content,
			Media:          append([]string(nil), msg.Media...),
		},
		SenderID:                msg.SenderID,
		SenderDisplayName:       msg.Sender.DisplayName,
		EnableSummary:           false,
		SendResponse:            false,
		AllowInterimPicoPublish: false,
	}
	normalizeProcessOptionsInPlace(&opts)

	cmdCtx, cancel := context.WithTimeout(ctx, activeTurnCommandTimeout)
	defer cancel()

	response, handled := al.handleCommand(cmdCtx, msg, agent, &opts)
	if !handled {
		return
	}
	al.publishActiveTurnCommandReply(cmdCtx, msg, sessionKey, response)
}

// publishActiveTurnCommandReply publishes directly to the bus instead of using
// publishResponseIfNeeded, because a running turn may already have sent a
// message through the message tool and must not suppress command output.
func (al *AgentLoop) publishActiveTurnCommandReply(
	ctx context.Context,
	msg bus.InboundMessage,
	sessionKey, response string,
) {
	if strings.TrimSpace(response) == "" {
		return
	}

	out := bus.OutboundMessage{
		Context:    bus.NewOutboundContext(msg.Channel, msg.ChatID, ""),
		SessionKey: sessionKey,
		Content:    response,
	}
	markFinalOutbound(&out)
	if err := al.bus.PublishOutbound(ctx, out); err != nil {
		logger.WarnCF("agent", "Failed to publish active-turn command reply",
			map[string]any{
				"channel":     msg.Channel,
				"chat_id":     msg.ChatID,
				"session_key": sessionKey,
				"error":       err.Error(),
			})
	}
}
