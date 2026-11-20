package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

const (
	// execApprovalTokenBytes is the entropy of an approval token. The hex form
	// is twice as long and is what the user replies with.
	execApprovalTokenBytes = 16
	// maxExecApprovalCommandRunes bounds the command shown in a prompt. A
	// longer unknown command cannot be reviewed safely in chat, so it is
	// denied instead of showing a truncated view the user might approve.
	maxExecApprovalCommandRunes = 12000
)

// execUnknownCommandApprovalHook asks the originating user to approve a
// command the AST risk classifier cannot resolve. It is mounted by every
// AgentLoop and reads the current config on each call, so a hot reload applies
// without remounting the hook.
type execUnknownCommandApprovalHook struct {
	agent *AgentLoop
}

type pendingExecApproval struct {
	origin   bus.InboundContext
	decision chan bool
	expires  time.Time
}

// ApprovalTimeout implements approvalTimeoutOverrider: a person needs longer
// than the generic hook deadline to read a prompt and reply. It follows the
// current config, so a hot reload applies to the next request.
func (h *execUnknownCommandApprovalHook) ApprovalTimeout() time.Duration {
	if h == nil || h.agent == nil {
		return 0
	}
	cfg := h.agent.GetConfig()
	if cfg == nil {
		return 0
	}
	return time.Duration(cfg.Tools.Exec.GetApprovalTimeoutSeconds()) * time.Second
}

// ApproveTool implements ToolApprover. Known commands and non-run actions pass
// through untouched; only an unknown exec run reaches the interactive gate.
func (h *execUnknownCommandApprovalHook) ApproveTool(
	ctx context.Context,
	req *ToolApprovalRequest,
) (ApprovalDecision, error) {
	if h == nil || h.agent == nil || req == nil {
		return ApprovalDecision{Approved: true}, nil
	}
	if req.Tool != "exec" {
		return ApprovalDecision{Approved: true}, nil
	}
	action, _ := req.Arguments["action"].(string)
	if action != "" && action != "run" {
		return ApprovalDecision{Approved: true}, nil
	}
	command, _ := req.Arguments["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return ApprovalDecision{Approved: true}, nil
	}

	cfg := h.agent.GetConfig()
	if cfg == nil {
		return ApprovalDecision{Reason: "configuration is unavailable"}, nil
	}
	policy := cfg.Tools.Exec.GetUnknownCommandPolicy()
	if policy == config.ExecUnknownCommandAllow {
		return ApprovalDecision{Approved: true}, nil
	}

	assessment, active := tools.AssessExecCommand(command, cfg)
	if !active || !assessment.Unknown {
		return ApprovalDecision{Approved: true}, nil
	}
	// A rejected or pattern-denied command is blocked by the exec guard itself,
	// which returns the precise error. Asking here would prompt for a command
	// that cannot run.
	if assessment.Reject || assessment.Denied {
		return ApprovalDecision{Approved: true}, nil
	}
	if policy == config.ExecUnknownCommandBlock {
		return ApprovalDecision{
			Reason: "unknown commands are blocked by tools.exec.unknown_command_policy",
		}, nil
	}

	return h.ask(ctx, req, command, assessment.Reason)
}

// ask delivers an approval prompt to the originating user and waits for the
// matching reply. Every failure path denies the command: an unattended or
// unreachable user must not silently approve an unclassifiable command.
func (h *execUnknownCommandApprovalHook) ask(
	ctx context.Context,
	req *ToolApprovalRequest,
	command, reason string,
) (ApprovalDecision, error) {
	al := h.agent
	if !al.running.Load() {
		return ApprovalDecision{
			Reason: "unknown command requires approval, but no interactive user is available",
		}, nil
	}
	if al.channelManager == nil {
		return ApprovalDecision{
			Reason: "unknown command requires approval, but no channel is available",
		}, nil
	}
	if req.Context == nil || req.Context.Inbound == nil {
		return ApprovalDecision{
			Reason: "unknown command requires approval, but the request has no origin",
		}, nil
	}

	origin := *req.Context.Inbound
	if origin.Channel == "" || origin.ChatID == "" || origin.SenderID == "" ||
		origin.Channel == "system" || isAutomatedExecSender(origin.SenderID) {
		return ApprovalDecision{
			Reason: "unknown command requires approval, but no interactive user is available",
		}, nil
	}

	cfg := al.GetConfig()
	if cfg != nil && !cfg.Tools.Exec.AllowRemote && !constants.IsInternalChannel(origin.Channel) {
		// The exec tool rejects remote runs before executing them, so there is
		// nothing for the user to approve.
		return ApprovalDecision{Approved: true}, nil
	}

	channel, ok := al.channelManager.GetChannel(origin.Channel)
	if !ok || !channel.IsRunning() {
		return ApprovalDecision{
			Reason: "unknown command requires approval, but its channel is not running",
		}, nil
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(defaultHookApprovalTimeout)
	}

	display, ok := formatExecApprovalCommand(command)
	if !ok {
		return ApprovalDecision{
			Reason: "unknown command is too long to display for approval; shorten it or set unknown_command_policy to allow",
		}, nil
	}

	var nonce [execApprovalTokenBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ApprovalDecision{Reason: "could not create an approval token"}, err
	}
	token := hex.EncodeToString(nonce[:])

	pending := &pendingExecApproval{
		origin:   origin,
		decision: make(chan bool, 1),
		expires:  deadline,
	}
	al.pendingExecApprovals.Store(token, pending)
	defer al.pendingExecApprovals.Delete(token)

	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	reasonLine := ""
	if reason != "" {
		reasonLine = fmt.Sprintf("Reason: %s\n", reason)
	}
	message := fmt.Sprintf(
		"Approval required for an unknown exec command.\n"+
			"Reply \"approve\" to run it or \"deny\" to cancel. "+
			"If more than one request is pending, reply \"approve %s\" or \"deny %s\" instead. "+
			"This request expires in %s.\n\n%sCommand: %s",
		token, token, remaining.Round(time.Second), reasonLine, display,
	)
	outboundCtx := outboundContextFromInbound(
		req.Context.Inbound,
		origin.Channel,
		origin.ChatID,
		origin.MessageID,
	)
	if outboundCtx.Raw == nil {
		outboundCtx.Raw = make(map[string]string, 5)
	}
	outboundCtx.Raw[metadataKeyMessageKind] = messageKindApproval
	outboundCtx.Raw[metadataKeyApprovalToken] = token
	outboundCtx.Raw[metadataKeyApprovalCommand] = display
	outboundCtx.Raw[metadataKeyApprovalExpires] = strconv.FormatInt(deadline.UnixMilli(), 10)
	if reason != "" {
		outboundCtx.Raw[metadataKeyApprovalReason] = reason
	}

	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := al.channelManager.SendMessage(sendCtx, bus.OutboundMessage{
		Context:    outboundCtx,
		AgentID:    req.Meta.AgentID,
		SessionKey: req.Meta.SessionKey,
		Content:    message,
	})
	cancel()
	if err != nil {
		logger.WarnCF("agent", "Failed to deliver unknown-command approval prompt", map[string]any{
			"agent_id": req.Meta.AgentID,
			"channel":  origin.Channel,
			"error":    err.Error(),
		})
		return ApprovalDecision{Reason: "could not deliver the approval request"}, nil
	}

	logger.InfoCF("agent", "Unknown-command approval requested", map[string]any{
		"agent_id": req.Meta.AgentID,
		"channel":  origin.Channel,
		"chat_id":  origin.ChatID,
	})

	select {
	case approved := <-pending.decision:
		return execApprovalDecision(approved), nil
	case <-ctx.Done():
		// Prefer a decision that arrived at the deadline over the timeout.
		select {
		case approved := <-pending.decision:
			return execApprovalDecision(approved), nil
		default:
		}
		al.sendExecApprovalReply(
			ctx,
			origin,
			fmt.Sprintf("Approval request %s expired; the command was denied.", token),
		)
		return ApprovalDecision{Reason: "approval expired or was canceled"}, nil
	}
}

func execApprovalDecision(approved bool) ApprovalDecision {
	if approved {
		return ApprovalDecision{Approved: true}
	}
	return ApprovalDecision{Reason: "user denied the unknown command"}
}

func isAutomatedExecSender(senderID string) bool {
	switch strings.ToLower(strings.TrimSpace(senderID)) {
	case "cron", "heartbeat", "system":
		return true
	default:
		return false
	}
}

func formatExecApprovalCommand(command string) (string, bool) {
	if len([]rune(command)) > maxExecApprovalCommandRunes {
		return "", false
	}
	return fmt.Sprintf("%q", command), true
}

// tryHandleExecApproval consumes approval replies before they can enter
// steering or the LLM, including while the original turn is blocked in the
// approval hook. It reports whether the message was an approval reply.
func (al *AgentLoop) tryHandleExecApproval(ctx context.Context, msg bus.InboundMessage) bool {
	msg = bus.NormalizeInboundMessage(msg)
	parts := strings.Fields(strings.TrimSpace(msg.Content))
	if len(parts) == 0 {
		return false
	}
	// Channel group triggers may leave a leading @mention in the text, so match
	// the trailing pair instead of requiring exactly two words. A lone word is
	// the short form, which avoids retyping the token on a phone.
	action, token := strings.ToLower(parts[0]), ""
	if len(parts) > 1 {
		action, token = strings.ToLower(parts[len(parts)-2]), strings.ToLower(parts[len(parts)-1])
	}
	if action != "approve" && action != "deny" && action != "/approve" && action != "/deny" {
		return false
	}
	if token == "" {
		// The short form answers the only live request addressed to this user.
		// With none it is ordinary chat; with several the user must pick one.
		var pending int
		token, pending = al.soleExecApprovalToken(msg.Context)
		if pending == 0 {
			return false
		}
		if pending > 1 {
			al.sendExecApprovalReply(
				ctx,
				msg.Context,
				fmt.Sprintf(
					"%d approvals are pending; reply \"%s <token>\" with the token from the request.",
					pending, action,
				),
			)
			return true
		}
	}
	if len(token) != execApprovalTokenBytes*2 {
		return false
	}
	if decoded, err := hex.DecodeString(token); err != nil || len(decoded) != execApprovalTokenBytes {
		return false
	}

	response := "No pending approval for that token."
	if value, ok := al.pendingExecApprovals.Load(token); ok {
		pending, _ := value.(*pendingExecApproval)
		if pending == nil {
			al.pendingExecApprovals.Delete(token)
			return true
		}
		incoming := msg.Context
		origin := pending.origin
		unexpired := time.Now().Before(pending.expires)
		switch {
		case !unexpired:
			response = "That approval request has expired."
		case !sameExecRequester(origin, incoming):
			response = "Only the requesting user can answer this approval."
		default:
			if _, loaded := al.pendingExecApprovals.LoadAndDelete(token); loaded {
				approved := action == "approve" || action == "/approve"
				pending.decision <- approved
				response = "Approval response received."
				logger.InfoCF("agent", "Unknown-command approval answered", map[string]any{
					"channel":  origin.Channel,
					"chat_id":  origin.ChatID,
					"approved": approved,
				})
			}
		}
	}

	al.sendExecApprovalReply(ctx, msg.Context, response)
	return true
}

// sameExecRequester reports whether both contexts identify the same user in the
// same conversation.
func sameExecRequester(a, b bus.InboundContext) bool {
	return a.Channel == b.Channel &&
		a.Account == b.Account &&
		a.ChatID == b.ChatID &&
		a.TopicID == b.TopicID &&
		a.SpaceID == b.SpaceID &&
		a.SenderID == b.SenderID
}

// soleExecApprovalToken returns the token of a live request addressed to
// requester and how many such requests exist. The token is only meaningful when
// the count is exactly one.
func (al *AgentLoop) soleExecApprovalToken(requester bus.InboundContext) (string, int) {
	var token string
	var count int
	now := time.Now()
	al.pendingExecApprovals.Range(func(key, value any) bool {
		pending, _ := value.(*pendingExecApproval)
		if pending != nil && now.Before(pending.expires) && sameExecRequester(pending.origin, requester) {
			token, _ = key.(string)
			count++
		}
		return true
	})
	return token, count
}

// sendExecApprovalReply delivers a short operational reply to the requester
// without letting it enter the LLM conversation.
func (al *AgentLoop) sendExecApprovalReply(ctx context.Context, origin bus.InboundContext, content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	msg := bus.OutboundMessage{Context: origin, Content: content}
	if al.channelManager != nil {
		if err := al.channelManager.SendMessage(sendCtx, msg); err == nil {
			return
		}
	}
	if al.bus != nil {
		_ = al.bus.PublishOutbound(sendCtx, msg)
	}
}
