package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type execConfirmationHook struct{ agent *AgentLoop }

type pendingExecApproval struct {
	origin   bus.InboundContext
	decision chan bool
	expires  time.Time
}

func (h *execConfirmationHook) ApproveTool(ctx context.Context, req *ToolApprovalRequest) (ApprovalDecision, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultHookApprovalTimeout)
		defer cancel()
	}
	if req.Tool != "exec" || req.Arguments["action"] != "run" {
		return ApprovalDecision{Approved: true}, nil
	}
	command, _ := req.Arguments["command"].(string)
	if tools.IsCatastrophicExecCommand(command) {
		return ApprovalDecision{Reason: "catastrophic command is always blocked"}, nil
	}
	for _, pattern := range h.agent.cfg.Tools.Exec.CustomDenyPatterns {
		matched, err := regexp.MatchString(pattern, strings.ToLower(strings.TrimSpace(command)))
		if err != nil || matched {
			return ApprovalDecision{Reason: "command matches a custom deny pattern"}, nil
		}
	}
	if !h.agent.cfg.Tools.Exec.EnableDenyPatterns || !tools.DefaultExecCommandNeedsApproval(command) {
		return ApprovalDecision{Approved: true}, nil
	}
	if !h.agent.running.Load() || h.agent.channelManager == nil || req.Context == nil || req.Context.Inbound == nil {
		return ApprovalDecision{Reason: "no interactive user available to approve this command"}, nil
	}
	origin := *req.Context.Inbound
	if !h.agent.cfg.Tools.Exec.AllowRemote && !constants.IsInternalChannel(origin.Channel) {
		return ApprovalDecision{Reason: "exec is restricted to internal channels"}, nil
	}
	if origin.Channel == "" || origin.ChatID == "" || origin.SenderID == "" ||
		origin.SenderID == "cron" || origin.SenderID == "heartbeat" || origin.Channel == "system" {
		return ApprovalDecision{Reason: "no interactive user available to approve this command"}, nil
	}
	channel, ok := h.agent.channelManager.GetChannel(origin.Channel)
	if !ok || !channel.IsRunning() {
		return ApprovalDecision{Reason: "no interactive user available to approve this command"}, nil
	}

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ApprovalDecision{Reason: "could not create an approval token"}, err
	}
	token := hex.EncodeToString(nonce[:])
	deadline, _ := ctx.Deadline()
	pending := &pendingExecApproval{origin: origin, decision: make(chan bool, 1), expires: deadline}
	h.agent.pendingExecApprovals.Store(token, pending)
	defer h.agent.pendingExecApprovals.Delete(token)

	remaining := time.Until(deadline)
	if remaining < 0 {
		remaining = 0
	}
	message := fmt.Sprintf("Approve this shell command?\n%q\nReply approve %s or deny %s within %s.",
		command, token, token, remaining.Round(time.Second))
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := h.agent.channelManager.SendMessage(sendCtx, bus.OutboundMessage{
		Context: origin, Content: message, SessionKey: req.Meta.SessionKey,
	})
	cancel()
	if err != nil {
		return ApprovalDecision{Reason: "could not deliver the approval request"}, nil
	}

	select {
	case approved := <-pending.decision:
		if approved {
			return ApprovalDecision{Approved: true, approvedCommand: command}, nil
		}
		return ApprovalDecision{Reason: "user denied the command"}, nil
	case <-ctx.Done():
		if h.agent.bus != nil {
			notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = h.agent.bus.PublishOutbound(notifyCtx, bus.OutboundMessage{
				Context: origin, Content: "Approval closed; command denied.", SessionKey: req.Meta.SessionKey,
			})
			notifyCancel()
		}
		return ApprovalDecision{Reason: "approval expired or was canceled"}, nil
	}
}

// tryHandleExecApproval consumes approval replies before they enter steering or
// the LLM, including while the original turn is waiting in ApproveTool.
func (al *AgentLoop) tryHandleExecApproval(ctx context.Context, msg bus.InboundMessage) bool {
	msg = bus.NormalizeInboundMessage(msg)
	parts := strings.Fields(strings.TrimSpace(msg.Content))
	if len(parts) != 2 || len(parts[1]) != 32 ||
		(parts[0] != "approve" && parts[0] != "deny" && parts[0] != "/approve" && parts[0] != "/deny") {
		return false
	}
	if decoded, err := hex.DecodeString(parts[1]); err != nil || len(decoded) != 16 {
		return false
	}
	response := "No pending approval for that token."
	if value, ok := al.pendingExecApprovals.Load(parts[1]); ok {
		pending := value.(*pendingExecApproval)
		origin := pending.origin
		incoming := msg.Context
		unexpired := time.Now().Before(pending.expires)
		sameRequester := origin.Channel == incoming.Channel && origin.Account == incoming.Account &&
			origin.ChatID == incoming.ChatID && origin.TopicID == incoming.TopicID &&
			origin.SpaceID == incoming.SpaceID && origin.SenderID == incoming.SenderID
		if unexpired && sameRequester {
			if _, loaded := al.pendingExecApprovals.LoadAndDelete(parts[1]); loaded {
				approved := parts[0] == "approve" || parts[0] == "/approve"
				pending.decision <- approved
				response = "Approval response received."
			}
		} else if !unexpired {
			response = "That approval request has expired."
		} else {
			response = "Only the requesting user can answer this approval."
		}
	}
	if al.bus != nil {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = al.bus.PublishOutbound(sendCtx, bus.OutboundMessage{Context: msg.Context, Content: response})
		cancel()
	}
	return true
}
