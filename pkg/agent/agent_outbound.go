// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	integrationtools "github.com/sipeed/picoclaw/pkg/tools/integration"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func (al *AgentLoop) maybePublishError(ctx context.Context, channel, chatID, sessionKey string, err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	al.publishTurnFailureNotice(ctx, channel, chatID, sessionKey, formatProcessingError(err))
	return true
}

// publishTurnFailureNotice delivers a visible notice when a turn produced no
// reply for the user.
//
// It differs from PublishResponseIfNeeded in two ways:
//
//  1. It never suppresses the notice because the message tool already sent to
//     this chat. A failure notice carries different information than whatever
//     the tool delivered mid-turn, and suppressing it leaves the user staring
//     at silence with no way to tell a finished turn from a dead one.
//  2. It survives a canceled parent context. Turn failures often happen while
//     the surrounding context is already being torn down (provider timeout,
//     shutdown), which is exactly when the notice matters most.
//
// Internal channels (cli/system/subagent) have no human reader, so they are
// skipped to avoid noise.
func (al *AgentLoop) publishTurnFailureNotice(
	ctx context.Context,
	channel, chatID, sessionKey, notice string,
) {
	if strings.TrimSpace(notice) == "" {
		return
	}
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(chatID) == "" {
		return
	}
	if constants.IsInternalChannel(channel) {
		return
	}

	al.publishResponseIfNeeded(ctx, channel, chatID, sessionKey, notice, nil, publishResponseOptions{
		skipMessageToolSuppression: true,
		surviveCanceledContext:     true,
	})
}

func (al *AgentLoop) publishResponseOrError(
	ctx context.Context,
	channel, chatID, sessionKey string,
	response string,
	err error,
) {
	if err != nil {
		if !al.maybePublishError(ctx, channel, chatID, sessionKey, err) {
			return
		}
		response = ""
	}
	al.PublishResponseIfNeeded(ctx, channel, chatID, sessionKey, response)
}

// publishResponseOptions controls how a response is delivered.
type publishResponseOptions struct {
	// skipMessageToolSuppression delivers the message even when the message
	// tool already sent to this chat during the current round.
	skipMessageToolSuppression bool
	// surviveCanceledContext detaches the publish from the parent context so
	// the message is still handed to the bus when the parent is already done.
	surviveCanceledContext bool
}

func (al *AgentLoop) PublishResponseIfNeeded(ctx context.Context, channel, chatID, sessionKey, response string) {
	al.publishFinalResponse(ctx, channel, chatID, sessionKey, response)
}

func (al *AgentLoop) publishFinalResponse(
	ctx context.Context,
	channel, chatID, sessionKey, response string,
) bool {
	return al.publishResponseIfNeeded(
		ctx,
		channel,
		chatID,
		sessionKey,
		response,
		al.latestAssistantUsage(ctx, sessionKey, response),
		publishResponseOptions{},
	)
}

// DeliverResponse is PublishResponseIfNeeded for callers that must know the
// reply never reached the bus (a cron run must not report "ok" then).
func (al *AgentLoop) DeliverResponse(ctx context.Context, channel, chatID, sessionKey, response string) error {
	_, err := al.deliverResponse(
		ctx, channel, chatID, sessionKey, response,
		al.latestAssistantUsage(ctx, sessionKey, response), publishResponseOptions{},
	)
	return err
}

func (al *AgentLoop) publishResponseIfNeeded(
	ctx context.Context,
	channel, chatID, sessionKey, response string,
	usage *bus.TokenUsage,
	opts publishResponseOptions,
) bool {
	published, err := al.deliverResponse(ctx, channel, chatID, sessionKey, response, usage, opts)
	if err != nil {
		// A reply that never reached the bus used to vanish without a trace.
		// Log it so silent turns stay diagnosable.
		logger.WarnCF("agent", "Failed to publish outbound response", map[string]any{
			"channel":     channel,
			"chat_id":     chatID,
			"content_len": len(response),
			"error":       err.Error(),
		})
	}
	return published
}

// deliverResponse reports whether response was handed to the bus; a nil error
// with false means it was intentionally suppressed.
func (al *AgentLoop) deliverResponse(
	ctx context.Context,
	channel, chatID, sessionKey, response string,
	usage *bus.TokenUsage,
	opts publishResponseOptions,
) (bool, error) {
	if response == "" {
		return false, nil
	}

	alreadySentToSameChat := false
	if !opts.skipMessageToolSuppression {
		defaultAgent := al.GetRegistry().GetDefaultAgent()
		if defaultAgent != nil {
			if tool, ok := defaultAgent.Tools.Get("message"); ok {
				if mt, ok := tool.(*integrationtools.MessageTool); ok {
					alreadySentToSameChat = mt.HasSentTo(sessionKey, channel, chatID)
				}
			}
		}
	}

	if alreadySentToSameChat {
		if al.channelManager != nil && channel != "" && chatID != "" {
			dismissCtx, dismissCancel := context.WithTimeout(ctx, 5*time.Second)
			al.channelManager.DismissToolFeedback(
				dismissCtx,
				channel,
				chatID,
				nil,
			)
			dismissCancel()
		}
		logger.DebugCF(
			"agent",
			"Skipped outbound (message tool already sent to same chat)",
			map[string]any{"channel": channel, "chat_id": chatID},
		)
		return false, nil
	}

	msg := bus.OutboundMessage{
		Context:    bus.NewOutboundContext(channel, chatID, ""),
		SessionKey: sessionKey,
		Content:    response,
		Usage:      usage,
	}
	if sessionKey != "" {
		msg.ContextUsage, _ = al.currentContextUsage(al.agentForSession(sessionKey), sessionKey)
	}
	markFinalOutbound(&msg)

	// A failure notice is often produced while the parent context is already
	// being torn down (provider timeout, shutdown). Detach from cancellation so
	// the notice still reaches the bus, but keep a bounded deadline so a stuck
	// bus cannot leak this goroutine.
	pubCtx := ctx
	if opts.surviveCanceledContext {
		pubCtx = context.WithoutCancel(ctx)
	}
	pubCtx, pubCancel := context.WithTimeout(pubCtx, 10*time.Second)
	defer pubCancel()

	if err := al.bus.PublishOutbound(pubCtx, msg); err != nil {
		return false, err
	}
	logger.InfoCF("agent", "Published outbound response",
		map[string]any{
			"channel":     channel,
			"chat_id":     chatID,
			"content_len": len(response),
		})
	return true, nil
}

// latestAssistantUsage returns the usage attached to the persisted assistant
// reply that matches response. Matching on content prevents command replies,
// stop messages, and errors from picking up stale usage from an earlier turn.
func (al *AgentLoop) latestAssistantUsage(
	ctx context.Context,
	sessionKey, response string,
) *bus.TokenUsage {
	if sessionKey == "" || strings.TrimSpace(response) == "" {
		return nil
	}
	agent := al.agentForSession(sessionKey)
	if agent == nil || agent.Sessions == nil {
		return nil
	}
	history, err := agent.Sessions.GetHistory(ctx, sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "read assistant usage", err)
		return nil
	}
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(msg.Content) != strings.TrimSpace(response) {
			return nil
		}
		return tokenUsageFromProviders(msg.Usage)
	}
	return nil
}

func (al *AgentLoop) targetReasoningChannelID(channelName string) (chatID string) {
	if al.channelManager == nil {
		return ""
	}
	if ch, ok := al.channelManager.GetChannel(channelName); ok {
		return ch.ReasoningChannelID()
	}
	return ""
}

func (al *AgentLoop) publishPicoReasoning(
	ctx context.Context,
	reasoningContent, chatID, sessionKey, modelName string,
) {
	if reasoningContent == "" || chatID == "" {
		return
	}

	if ctx.Err() != nil {
		return
	}

	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	raw := map[string]string{metadataKeyMessageKind: messageKindThought}
	if trimmedModelName := strings.TrimSpace(modelName); trimmedModelName != "" {
		raw["model_name"] = trimmedModelName
	}

	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{
		Context: bus.InboundContext{
			Channel: "pico",
			ChatID:  chatID,
			Raw:     raw,
		},
		SessionKey: sessionKey,
		Content:    reasoningContent,
	}); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(err, bus.ErrBusClosed) {
			logger.DebugCF("agent", "Pico reasoning publish skipped (timeout/cancel)", map[string]any{
				"channel": "pico",
				"error":   err.Error(),
			})
		} else {
			logger.WarnCF("agent", "Failed to publish pico reasoning (best-effort)", map[string]any{
				"channel": "pico",
				"error":   err.Error(),
			})
		}
	}
}

func (al *AgentLoop) publishPicoToolCallInterim(
	ctx context.Context,
	ts *turnState,
	modelName string,
	reasoningContent string,
	content string,
	toolCalls []providers.ToolCall,
) {
	if ts == nil || ts.chatID == "" || al == nil || al.bus == nil {
		return
	}

	if strings.TrimSpace(reasoningContent) != "" {
		pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
		err := al.bus.PublishOutbound(
			pubCtx,
			outboundMessageForTurnWithOptions(
				ts,
				reasoningContent,
				outboundTurnMessageOptions{
					kind:      messageKindThought,
					modelName: modelName,
				},
			),
		)
		pubCancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, bus.ErrBusClosed) {
			logger.WarnCF("agent", "Failed to publish pico reasoning", map[string]any{
				"channel": ts.channel,
				"chat_id": ts.chatID,
				"error":   err.Error(),
			})
		}
	}

	if !ts.opts.AllowInterimPicoPublish {
		return
	}

	visibleToolCalls := utils.BuildVisibleToolCalls(
		toolCalls,
		al.cfg.Agents.Defaults.GetToolFeedbackMaxArgsLength(),
	)
	duplicateToolCallContent := len(visibleToolCalls) > 0 &&
		utils.ToolCallExplanationDuplicatesContent(content, toolCalls)

	if strings.TrimSpace(content) != "" && !duplicateToolCallContent {
		pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
		err := al.bus.PublishOutbound(
			pubCtx,
			outboundMessageForTurnWithOptions(ts, content, outboundTurnMessageOptions{
				modelName: modelName,
			}),
		)
		pubCancel()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, bus.ErrBusClosed) {
			logger.WarnCF("agent", "Failed to publish pico interim assistant content", map[string]any{
				"channel": ts.channel,
				"chat_id": ts.chatID,
				"error":   err.Error(),
			})
		}
	}

	if len(visibleToolCalls) == 0 {
		return
	}

	rawToolCalls, err := json.Marshal(visibleToolCalls)
	if err != nil {
		logger.WarnCF("agent", "Failed to serialize pico tool calls", map[string]any{
			"channel": ts.channel,
			"chat_id": ts.chatID,
			"error":   err.Error(),
		})
		return
	}

	msg := outboundMessageForTurnWithOptions(ts, "", outboundTurnMessageOptions{
		kind:      messageKindToolCalls,
		modelName: modelName,
		raw: map[string]string{
			metadataKeyToolCalls: string(rawToolCalls),
		},
	})

	pubCtx, pubCancel := context.WithTimeout(ctx, 3*time.Second)
	err = al.bus.PublishOutbound(pubCtx, msg)
	pubCancel()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
		!errors.Is(err, context.Canceled) &&
		!errors.Is(err, bus.ErrBusClosed) {
		logger.WarnCF("agent", "Failed to publish pico tool calls", map[string]any{
			"channel": ts.channel,
			"chat_id": ts.chatID,
			"error":   err.Error(),
		})
	}
}

func (al *AgentLoop) handleReasoning(
	ctx context.Context,
	reasoningContent, channelName, channelID string,
) {
	if reasoningContent == "" || channelName == "" || channelID == "" {
		return
	}

	// Check context cancellation before attempting to publish,
	// since PublishOutbound's select may race between send and ctx.Done().
	if ctx.Err() != nil {
		return
	}

	// Use a short timeout so the goroutine does not block indefinitely when
	// the outbound bus is full.  Reasoning output is best-effort; dropping it
	// is acceptable to avoid goroutine accumulation.
	pubCtx, pubCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pubCancel()

	if err := al.bus.PublishOutbound(pubCtx, bus.OutboundMessage{
		Context: bus.NewOutboundContext(channelName, channelID, ""),
		Content: reasoningContent,
	}); err != nil {
		// Treat context.DeadlineExceeded / context.Canceled as expected
		// (bus full under load, or parent canceled).  Check the error
		// itself rather than ctx.Err(), because pubCtx may time out
		// (5 s) while the parent ctx is still active.
		// Also treat ErrBusClosed as expected — it occurs during normal
		// shutdown when the bus is closed before all goroutines finish.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
			errors.Is(err, bus.ErrBusClosed) {
			logger.DebugCF("agent", "Reasoning publish skipped (timeout/cancel)", map[string]any{
				"channel": channelName,
				"error":   err.Error(),
			})
		} else {
			logger.WarnCF("agent", "Failed to publish reasoning (best-effort)", map[string]any{
				"channel": channelName,
				"error":   err.Error(),
			})
		}
	}
}
