// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"strconv"
	"time"

	"github.com/afjcjsbx/picoclaw/pkg/utils"
)

const (
	contextFeedbackToolSummarize = "summarize"
	contextFeedbackToolCompact   = "compact"
)

// notifyContextFeedback publishes a tool-feedback-style notice to the
// originating session's chat when context summarization or compaction occurs.
// It is a no-op unless tool feedback is enabled for the agent defaults, so the
// notice reuses the same channel flow as regular tool feedback.
func (al *AgentLoop) notifyContextFeedback(ts *turnState, content string) {
	if al == nil || al.bus == nil || content == "" {
		return
	}
	if al.cfg == nil || !al.cfg.Agents.Defaults.IsToolFeedbackEnabled() {
		return
	}
	if ts == nil || ts.channel == "" || ts.opts.SuppressToolFeedback {
		return
	}

	pubCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = al.bus.PublishOutbound(pubCtx, outboundMessageForTurnWithOptions(
		ts,
		content,
		outboundTurnMessageOptions{kind: messageKindToolFeedback},
	))
}

// notifyContextFeedbackFromCtx resolves the active turn for the session (from
// ctx when available, otherwise from the active turn registry) and forwards the
// notice. Async summarization runs without the turn in ctx, so the active-turn
// fallback keeps the notice deliverable while the turn is still registered.
func (al *AgentLoop) notifyContextFeedbackFromCtx(ctx context.Context, sessionKey, content string) {
	if al == nil {
		return
	}
	ts := turnStateFromContext(ctx)
	if ts == nil {
		ts = al.getActiveTurnState(sessionKey)
	}
	al.notifyContextFeedback(ts, content)
}

func formatContextSummarizeFeedback(summarized, kept int) string {
	return utils.FormatToolFeedbackMessage(
		contextFeedbackToolSummarize,
		"Summarized conversation history: "+
			strconv.Itoa(summarized)+" messages summarized, "+strconv.Itoa(kept)+" kept.",
		"",
	)
}

func formatContextCompactFeedback(dropped, remaining int) string {
	return utils.FormatToolFeedbackMessage(
		contextFeedbackToolCompact,
		"Compressed conversation context: dropped "+strconv.Itoa(dropped)+
			" oldest messages, "+strconv.Itoa(remaining)+" kept.",
		"",
	)
}

// formatContextSummaryCounts reports seahorse summary creation counts in plain
// language, for example:
//
//	"Compacted context: summarized 2 blocks of older messages and merged 3
//	summaries into a higher-level summary to free up space."
//
// A "block" is a chunk of older messages collapsed into one compact summary; a
// merge folds several summaries into a higher-level one. The notice only shows
// the steps that actually ran, so leaf-only compactions omit the merge clause.
func formatContextSummaryCounts(leaf, condensed int) string {
	detail := "Compacted context: "
	switch {
	case leaf > 0 && condensed > 0:
		detail += "summarized " + countLabel(leaf, "block", "blocks") +
			" of older messages and merged " + countLabel(condensed, "summary", "summaries") +
			" into a higher-level summary"
	case leaf > 0:
		detail += "summarized " + countLabel(leaf, "block", "blocks") + " of older messages"
	default:
		detail += "merged " + countLabel(condensed, "summary", "summaries") + " into a higher-level summary"
	}
	return utils.FormatToolFeedbackMessage(contextFeedbackToolCompact, detail+" to free up space.", "")
}

// countLabel renders a count with its correctly pluralized noun, e.g.
// "1 block" or "3 blocks".
func countLabel(n int, singular, plural string) string {
	word := plural
	if n == 1 {
		word = singular
	}
	return strconv.Itoa(n) + " " + word
}
