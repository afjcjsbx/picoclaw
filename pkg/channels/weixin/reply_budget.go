package weixin

import (
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/bus"
)

const (
	// replyLimitPerInbound is how many messages iLink delivers after one inbound
	// user message. Later ones fail with ret=-2 "prepare failed" and are not
	// delivered (measured 2026-10-11, see issue #135).
	replyLimitPerInbound = 10
	// finalReplyReserve keeps slots for the final reply and other non-progress
	// messages, so tool feedback never uses the whole allowance.
	finalReplyReserve = 3
	// toolFeedbackSlots is how many slots tool feedback may use. The last one
	// carries toolFeedbackPausedNotice instead of the feedback itself.
	toolFeedbackSlots = replyLimitPerInbound - finalReplyReserve

	toolFeedbackPausedNotice = "⏳ More steps are running; further progress updates are paused until the reply."
)

// toolFeedbackAction says what to do with the next tool feedback message.
type toolFeedbackAction int

const (
	toolFeedbackSend toolFeedbackAction = iota
	toolFeedbackSendNotice
	toolFeedbackSkip
)

// replyBudget counts the messages delivered to each user since their last
// inbound message. The zero value is ready to use.
type replyBudget struct {
	mu   sync.Mutex
	sent map[string]int
}

// reset starts a new count for userID. iLink grants a fresh allowance for
// every inbound message.
func (b *replyBudget) reset(userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sent, userID)
}

// recordSent counts one message delivered to userID.
func (b *replyBudget) recordSent(userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sent == nil {
		b.sent = make(map[string]int)
	}
	b.sent[userID]++
}

// sentTo returns how many messages userID received since their last inbound message.
func (b *replyBudget) sentTo(userID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sent[userID]
}

// nextToolFeedback decides what to do with the next tool feedback for userID:
// send it while tool feedback slots remain, replace it with a notice in the
// last slot, and skip it after that.
func (b *replyBudget) nextToolFeedback(userID string) toolFeedbackAction {
	switch sent := b.sentTo(userID); {
	case sent < toolFeedbackSlots-1:
		return toolFeedbackSend
	case sent == toolFeedbackSlots-1:
		return toolFeedbackSendNotice
	default:
		return toolFeedbackSkip
	}
}

// isToolFeedback reports whether msg is a tool progress message from the agent.
func isToolFeedback(msg bus.OutboundMessage) bool {
	return strings.EqualFold(strings.TrimSpace(msg.Context.Raw["message_kind"]), "tool_feedback")
}
