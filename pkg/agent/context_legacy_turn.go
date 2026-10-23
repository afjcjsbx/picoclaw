// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/providers"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	// keepActiveRounds is how many of the newest tool rounds of the active turn
	// stay verbatim when its middle is summarized. The newest round holds results
	// the model has not seen yet, so it can never be summarized away.
	keepActiveRounds = 2
	// maxTranscriptRunes bounds what the summarizer sees of one message of the
	// turn's middle (a tool output, an assistant text, a call's arguments).
	maxTranscriptRunes = 1500
	// activeTurnFocus tells the summarizer the segment is part of a task in
	// progress, not a finished conversation.
	activeTurnFocus = "the work done so far on the current task: tools called and their key results, " +
		"files, identifiers and decisions, and what remains to be done. Long outputs are shortened " +
		"here with […]; never report that shortening or treat it as work left to do"
)

// summarizeActiveTurn summarizes the middle of the turn in progress, for when
// the turn alone fills the window and no earlier Turn is left to summarize. The
// turn's opening user message and its newest keepActiveRounds tool rounds stay
// verbatim; everything between becomes part of the session summary, so the
// turn carries on from the compacted context. Rounds (an assistant message and
// its tool results) are never split. It reports whether a summary was applied.
func (m *legacyContextManager) summarizeActiveTurn(
	parent context.Context,
	agent *AgentInstance,
	sessionKey string,
	ts *turnState,
) bool {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()

	history, err := agent.Sessions.GetHistory(ctx, sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get history", err)
		return false
	}
	activeLen := matchingTurnMessageTail(history, ts.persistedMessagesSnapshot())
	if activeLen == 0 {
		return false
	}
	start := len(history) - activeLen
	active := history[start:]

	head := 0
	if active[0].Role == "user" {
		head = 1
	}
	rounds := splitToolRounds(active[head:])
	if len(rounds) <= keepActiveRounds {
		return false
	}
	cut := len(rounds) - keepActiveRounds
	middle := flattenRounds(rounds[:cut])

	// An LLM call is only worth paying for when it reclaims a real share of the threshold.
	middleTokens := 0
	for _, msg := range middle {
		middleTokens += EstimateMessageTokens(msg)
	}
	if middleTokens < summarizeThresholdTokens(agent)/10 {
		return false
	}

	summary, err := agent.Sessions.GetSummary(ctx, sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get summary", err)
		summary = ""
	}
	finalSummary := m.summarizeTranscript(ctx, agent, turnTranscript(middle), summary)

	// A canceled caller (hard abort) must not turn a half-finished summarization
	// into a degraded summary that replaces the real messages.
	if parent.Err() != nil || finalSummary == "" {
		return false
	}

	// The new summary holds part of this turn: a rollback must restore the old one.
	ts.pinRestoreSummary(summary)
	kept := append(append([]providers.Message(nil), active[:head]...), flattenRounds(rounds[cut:])...)
	newHistory := append(append([]providers.Message(nil), history[:start]...), kept...)
	warnSessionStoreError(sessionKey, "set summary", agent.Sessions.SetSummary(ctx, sessionKey, finalSummary))
	warnSessionStoreError(sessionKey, "set history", agent.Sessions.SetHistory(ctx, sessionKey, newHistory))
	warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(ctx, sessionKey))
	ts.replacePersistedMessages(kept)

	m.al.emitEvent(
		runtimeevents.KindAgentSessionSummarize,
		m.al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "summarizeActiveTurn", "turn.session.summarize"),
		SessionSummarizePayload{
			SummarizedMessages: len(middle),
			KeptMessages:       len(newHistory),
			SummaryLen:         len(finalSummary),
		},
	)
	m.al.notifyContextFeedback(ts, formatContextSummarizeFeedback(len(middle), len(newHistory)))
	return true
}

// summarizeTranscript folds the transcript into the existing summary in rolling
// batches, each sized to a quarter of the context window, so the summarizer
// prompt never outgrows the window it is meant to relieve.
func (m *legacyContextManager) summarizeTranscript(
	ctx context.Context,
	agent *AgentInstance,
	transcript []providers.Message,
	summary string,
) string {
	maxBatchChars := agent.ContextWindow * 5 / 8 // a quarter of the window at 2.5 chars per token
	batchStart, batchChars := 0, 0
	for i, msg := range transcript {
		if batchChars > 0 && batchChars+len(msg.Content) > maxBatchChars {
			summary, _ = m.summarizeBatch(ctx, agent, transcript[batchStart:i], summary, activeTurnFocus)
			batchStart, batchChars = i, 0
		}
		batchChars += len(msg.Content)
	}
	summary, _ = m.summarizeBatch(ctx, agent, transcript[batchStart:], summary, activeTurnFocus)
	return summary
}

// splitToolRounds groups messages into rounds: every non-tool message opens a
// round that owns the tool results following it.
func splitToolRounds(msgs []providers.Message) [][]providers.Message {
	var rounds [][]providers.Message
	for i, msg := range msgs {
		if msg.Role != "tool" || i == 0 {
			rounds = append(rounds, nil)
		}
		rounds[len(rounds)-1] = append(rounds[len(rounds)-1], msg)
	}
	return rounds
}

// activeTurnPruneEnd is the index in history where the protected part of the
// turn in progress starts: its newest keepActiveRounds rounds, or the whole turn
// when it has no more rounds than that. It is len(history) when the turn is not
// found in history.
func activeTurnPruneEnd(history, persisted []providers.Message) int {
	activeLen := matchingTurnMessageTail(history, persisted)
	if activeLen == 0 {
		return len(history)
	}
	start := len(history) - activeLen
	rounds := splitToolRounds(history[start:])
	if len(rounds) <= keepActiveRounds {
		return start
	}
	return len(history) - len(flattenRounds(rounds[len(rounds)-keepActiveRounds:]))
}

func flattenRounds(rounds [][]providers.Message) []providers.Message {
	var msgs []providers.Message
	for _, round := range rounds {
		msgs = append(msgs, round...)
	}
	return msgs
}

// turnTranscript renders messages as plain text for the summarizer: assistant
// tool calls with their arguments and tool results labeled by tool, each bound
// to maxTranscriptRunes.
func turnTranscript(msgs []providers.Message) []providers.Message {
	toolNames := make(map[string]string)
	out := make([]providers.Message, 0, len(msgs))
	for _, msg := range msgs {
		var sb strings.Builder
		switch msg.Role {
		case "tool":
			name := toolNames[msg.ToolCallID]
			if name == "" {
				name = "tool"
			}
			fmt.Fprintf(&sb, "[%s result] %s", name, clipTranscript(msg.Content))
		default:
			sb.WriteString(clipTranscript(msg.Content))
			for _, tc := range msg.ToolCalls {
				name, args := tc.Name, ""
				if tc.Function != nil {
					name, args = tc.Function.Name, tc.Function.Arguments
				}
				toolNames[tc.ID] = name
				fmt.Fprintf(&sb, "\n[called %s(%s)]", name, clipTranscript(args))
			}
		}
		out = append(out, providers.Message{Role: msg.Role, Content: sb.String()})
	}
	return out
}

// clipTranscript keeps the head and the tail of an over-long text around a short
// marker. The marker stays terse on purpose: a verbose "TRUNCATED" notice ends up
// quoted in the summary as if the source itself had been cut.
func clipTranscript(s string) string {
	if utf8.RuneCountInString(s) <= maxTranscriptRunes {
		return s
	}
	head := maxTranscriptRunes * 2 / 5
	return toolshared.RunePrefix(s, head) + " […] " + toolshared.RuneSuffix(s, maxTranscriptRunes-head)
}
