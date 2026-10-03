// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/identity"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/security"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func (al *AgentLoop) buildContinuationTarget(msg bus.InboundMessage) (*continuationTarget, error) {
	if msg.Channel == "system" {
		return nil, nil
	}

	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		return nil, err
	}
	allocation := al.allocateRouteSession(route, msg)

	return &continuationTarget{
		SessionKey: resolveScopeKey(allocation.SessionKey, msg.SessionKey),
		Channel:    msg.Channel,
		ChatID:     msg.ChatID,
	}, nil
}

func (al *AgentLoop) ProcessDirect(
	ctx context.Context,
	content, sessionKey string,
) (string, error) {
	return al.ProcessDirectWithChannel(ctx, content, sessionKey, "cli", "direct")
}

func (al *AgentLoop) ProcessDirectWithChannel(
	ctx context.Context,
	content, sessionKey, channel, chatID string,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	msg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  channel,
			ChatID:   chatID,
			ChatType: "direct",
			SenderID: "cron",
		},
		Content:    content,
		SessionKey: sessionKey,
	}

	return al.processMessageWithTrust(ctx, msg, true)
}

func (al *AgentLoop) ProcessHeartbeat(
	ctx context.Context,
	content, channel, chatID string,
) (string, error) {
	if err := al.ensureHooksInitialized(ctx); err != nil {
		return "", err
	}
	if err := al.ensureMCPInitialized(ctx); err != nil {
		return "", err
	}

	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		return "", fmt.Errorf("no default agent for heartbeat")
	}
	dispatch := DispatchRequest{
		SessionKey:  "heartbeat",
		UserMessage: content,
	}
	if channel != "" || chatID != "" {
		dispatch.InboundContext = &bus.InboundContext{
			Channel:  channel,
			ChatID:   chatID,
			ChatType: "direct",
			SenderID: "heartbeat",
		}
	}
	return al.runAgentLoop(ctx, agent, processOptions{
		Dispatch:             dispatch,
		DefaultResponse:      defaultResponse,
		EnableSummary:        false,
		SendResponse:         false,
		SuppressToolFeedback: true,
		NoHistory:            true, // Don't load session history for heartbeat
	})
}

func (al *AgentLoop) prepareInboundMessageForAgent(
	ctx context.Context,
	msg bus.InboundMessage,
) bus.InboundMessage {
	msg = bus.NormalizeInboundMessage(msg)

	var hadAudio bool
	msg, hadAudio = al.transcribeAudioInMessage(ctx, msg)

	// For audio messages the placeholder was deferred by the channel.
	// Now that transcription (and optional feedback) is done, send it.
	if hadAudio && al.channelManager != nil {
		al.channelManager.SendPlaceholder(ctx, msg.Channel, msg.ChatID)
	}

	return msg
}

func (al *AgentLoop) processMessage(ctx context.Context, msg bus.InboundMessage) (string, error) {
	return al.processMessageWithTrust(ctx, msg, false)
}

func (al *AgentLoop) processMessageWithTrust(
	ctx context.Context, msg bus.InboundMessage, trustedInternal bool,
) (string, error) {
	msg = al.prepareInboundMessageForAgent(ctx, msg)

	// Add message preview to log (show full content for error messages)
	var logContent string
	if strings.Contains(msg.Content, "Error:") || strings.Contains(msg.Content, "error") {
		logContent = msg.Content // Full content for errors
	} else {
		logContent = utils.Truncate(msg.Content, 80)
	}
	logger.InfoCF(
		"agent",
		fmt.Sprintf("Processing message from %s:%s: %s", msg.Channel, msg.SenderID, logContent),
		map[string]any{
			"channel":     msg.Channel,
			"chat_id":     msg.ChatID,
			"sender_id":   msg.SenderID,
			"session_key": msg.SessionKey,
		},
	)

	// Route system messages to processSystemMessage
	if msg.Channel == "system" {
		return al.processSystemMessage(ctx, msg)
	}

	route, agent, routeErr := al.resolveMessageRoute(msg)
	if routeErr != nil {
		return "", routeErr
	}

	allocation := al.allocateRouteSession(route, msg)

	// Resolve session key from the route allocation, while preserving explicit
	// agent-scoped keys supplied by the caller.
	scopeKey := resolveScopeKey(allocation.SessionKey, msg.SessionKey)
	sessionKey := scopeKey

	// Reset message-tool state for this round so we don't skip publishing due to a previous round.
	if tool, ok := agent.Tools.Get("message"); ok {
		if resetter, ok := tool.(interface{ ResetSentInRound(sessionKey string) }); ok {
			resetter.ResetSentInRound(sessionKey)
		}
	}

	logger.InfoCF("agent", "Routed message",
		map[string]any{
			"agent_id":           agent.ID,
			"scope_key":          scopeKey,
			"session_key":        sessionKey,
			"matched_by":         route.MatchedBy,
			"route_agent":        route.AgentID,
			"route_channel":      route.Channel,
			"route_main_session": allocation.MainSessionKey,
		})

	opts := processOptions{
		Dispatch: DispatchRequest{
			SessionKey:     sessionKey,
			SessionAliases: buildSessionAliases(sessionKey, append(allocation.SessionAliases, msg.SessionKey)...),
			InboundContext: cloneInboundContext(&msg.Context),
			RouteResult:    cloneResolvedRoute(&route),
			SessionScope:   session.CloneScope(&allocation.Scope),
			UserMessage:    msg.Content,
			Media:          append([]string(nil), msg.Media...),
		},
		SenderID:                msg.SenderID,
		SenderDisplayName:       msg.Sender.DisplayName,
		DefaultResponse:         defaultResponse,
		EnableSummary:           true,
		SendResponse:            false,
		AllowInterimPicoPublish: true,
	}
	var err error
	opts, err = resolveTurnProfileOptions(al.GetConfig(), opts)
	if err != nil {
		return "", err
	}

	// context-dependent commands check their own Runtime fields and report
	// "unavailable" when the required capability is nil.
	if response, handled := al.handleCommand(ctx, msg, agent, &opts); handled {
		return response, nil
	}

	if pending := al.takePendingSkills(opts.Dispatch.SessionKey); len(pending) > 0 {
		opts.ForcedSkills = append(opts.ForcedSkills, pending...)
		logger.InfoCF("agent", "Applying pending skill override",
			map[string]any{
				"session_key": opts.Dispatch.SessionKey,
				"skills":      strings.Join(pending, ","),
			})
	}

	if !trustedInternal {
		if content, wrapped := al.secureInboundContent(msg); wrapped {
			opts.Dispatch.UserMessage = content
		}
	}

	return al.runAgentLoop(ctx, agent, opts)
}

func (al *AgentLoop) secureInboundContent(msg bus.InboundMessage) (string, bool) {
	pi := al.GetConfig().Tools.PromptInjection
	if pi.Enabled && pi.WrapUntrustedChannels && !isTrustedDirectSender(al.GetConfig(), msg) {
		if pi.LogSuspicious {
			if hits := security.DetectSuspiciousPatterns(msg.Content); len(hits) > 0 {
				logger.WarnCF(
					"security",
					"suspicious pattern in inbound channel message",
					map[string]any{"source": msg.Channel, "patterns": hits},
				)
			}
		}
		content := security.TruncateSanitizedExternalContent(msg.Content, pi.MaxWrappedChars)
		return security.WrapSanitizedExternalContent(content, security.WrapOptions{
			Source: security.SourceChannelMetadata, Sender: msg.SenderID, IncludeWarning: true,
		}), true
	}
	return msg.Content, false
}

// isTrustedDirectSender mirrors the channel allow-list policy for direct messages.
// Group/channel messages and unknown direct senders remain untrusted.
func isTrustedDirectSender(cfg *config.Config, msg bus.InboundMessage) bool {
	channelName := msg.Channel
	if channelName == "" {
		channelName = msg.Context.Channel
	}
	senderID := msg.SenderID
	if senderID == "" {
		senderID = msg.Context.SenderID
	}
	if constants.IsInternalChannel(channelName) {
		return true
	}
	if cfg == nil || msg.Context.ChatType != "direct" || channelName == "" || senderID == "" {
		return false
	}
	channel := cfg.Channels[channelName]
	if channel == nil {
		return false
	}
	if len(channel.AllowFrom) == 0 {
		return true
	}

	sender := msg.Sender
	if sender.Platform == "" {
		sender.Platform = channelName
	}
	if sender.PlatformID == "" {
		sender.PlatformID = senderID
	}
	if platform, id, ok := identity.ParseCanonicalID(senderID); ok && strings.EqualFold(platform, channelName) {
		if sender.CanonicalID == "" {
			sender.CanonicalID = identity.BuildCanonicalID(platform, id)
		}
		if sender.PlatformID == senderID {
			sender.PlatformID = id
		}
	}
	for _, allowed := range channel.AllowFrom {
		if allowed == "*" || identity.MatchAllowed(sender, allowed) {
			return true
		}
	}
	return false
}

func (al *AgentLoop) resolveMessageRoute(msg bus.InboundMessage) (routing.ResolvedRoute, *AgentInstance, error) {
	registry := al.GetRegistry()
	inboundCtx := normalizedInboundContext(msg)
	route := registry.ResolveRoute(inboundCtx)

	agent, ok := registry.GetAgent(route.AgentID)
	if !ok {
		agent = registry.GetDefaultAgent()
	}
	if agent == nil {
		return routing.ResolvedRoute{}, nil, fmt.Errorf("no agent available for route (agent_id=%s)", route.AgentID)
	}

	return route, agent, nil
}

func (al *AgentLoop) allocateRouteSession(route routing.ResolvedRoute, msg bus.InboundMessage) session.Allocation {
	return session.AllocateRouteSession(session.AllocationInput{
		AgentID:       route.AgentID,
		Context:       normalizedInboundContext(msg),
		SessionPolicy: route.SessionPolicy,
	})
}

func (al *AgentLoop) processSystemMessage(
	ctx context.Context,
	msg bus.InboundMessage,
) (string, error) {
	if msg.Channel != "system" {
		return "", fmt.Errorf(
			"processSystemMessage called with non-system message channel: %s",
			msg.Channel,
		)
	}

	logger.InfoCF("agent", "Processing system message",
		map[string]any{
			"sender_id": msg.SenderID,
			"chat_id":   msg.ChatID,
		})

	// Parse origin channel from chat_id (format: "channel:chat_id")
	var originChannel, originChatID string
	if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
		originChannel = msg.ChatID[:idx]
		originChatID = msg.ChatID[idx+1:]
	} else {
		originChannel = "cli"
		originChatID = msg.ChatID
	}

	// Extract subagent result from message content
	// Format: "Task 'label' completed.\n\nResult:\n<actual content>"
	content := msg.Content
	if idx := strings.Index(content, "Result:\n"); idx >= 0 {
		content = content[idx+8:] // Extract just the result part
	}

	// Skip internal channels - only log, don't send to user
	if constants.IsInternalChannel(originChannel) {
		logger.InfoCF("agent", "Subagent completed (internal channel)",
			map[string]any{
				"sender_id":   msg.SenderID,
				"content_len": len(content),
				"channel":     originChannel,
			})
		return "", nil
	}

	// Use default agent for system messages
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		return "", fmt.Errorf("no default agent for system message")
	}

	// Use the origin session for context
	sessionKey := session.BuildMainSessionKey(agent.ID)
	dispatch := DispatchRequest{
		SessionKey:  sessionKey,
		UserMessage: fmt.Sprintf("[System: %s] %s", msg.SenderID, msg.Content),
	}
	if originChannel != "" || originChatID != "" {
		dispatch.InboundContext = &bus.InboundContext{
			Channel:  originChannel,
			ChatID:   originChatID,
			ChatType: "direct",
			SenderID: msg.SenderID,
		}
	}

	return al.runAgentLoop(ctx, agent, processOptions{
		Dispatch:        dispatch,
		DefaultResponse: "Background task completed.",
		EnableSummary:   false,
		SendResponse:    true,
	})
}
