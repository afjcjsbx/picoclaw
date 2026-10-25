package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// computeContextUsage estimates current context window consumption for the
// given agent and session from the session store. Includes history, system
// prompt (with dynamic context, summary, and skills — mirroring BuildMessages
// composition), and tool definitions.
// The output reserve (MaxTokens) is not counted as "used" but reduces the
// effective budget, matching isOverContextBudget's compression trigger:
//
//	compress when: history + system + tools + maxTokens > contextWindow
//	equivalent to: history + system + tools > contextWindow - maxTokens
//
// Callers that need the context actually sent to the model should use
// AgentLoop.currentContextUsage, which reads through the active ContextManager.
// Returns nil when the agent or session is unavailable.
func computeContextUsage(agent *AgentInstance, sessionKey string) *bus.ContextUsage {
	if agent == nil || agent.Sessions == nil {
		return nil
	}
	history, summary := readSessionContext(agent, sessionKey)
	return contextUsageFromMessages(agent, history, summary)
}

// currentContextUsage returns the usage of the prompt the active ContextManager
// assembles for the session, plus the number of messages in that prompt. The
// manager is the source of truth: seahorse compaction folds old messages into
// summaries in its own store without rewriting the JSONL session history, so
// reading Sessions directly would keep reporting the pre-compaction size.
// Falls back to the session store when assembly fails.
func (al *AgentLoop) currentContextUsage(
	agent *AgentInstance,
	sessionKey string,
) (*bus.ContextUsage, int) {
	if agent == nil || agent.Sessions == nil {
		return nil, 0
	}
	if al != nil && al.contextManager != nil {
		assembled, err := al.contextManager.Assemble(context.Background(), &AssembleRequest{
			SessionKey: sessionKey,
			Budget:     agent.ContextWindow,
			MaxTokens:  agent.MaxTokens,
		})
		switch {
		case err != nil:
			warnSessionStoreError(sessionKey, "assemble context usage", err)
		case assembled != nil:
			return contextUsageFromMessages(agent, assembled.History, assembled.Summary), len(assembled.History)
		}
	}

	history, summary := readSessionContext(agent, sessionKey)
	return contextUsageFromMessages(agent, history, summary), len(history)
}

// readSessionContext reads the raw history and summary. This is a best-effort
// estimator used by the UI and command stats, so read failures are logged and
// the zero value is used instead of aborting the estimate.
func readSessionContext(agent *AgentInstance, sessionKey string) ([]providers.Message, string) {
	history, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get history", err)
		history = []providers.Message{}
	}
	summary, err := agent.Sessions.GetSummary(context.Background(), sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get summary", err)
		summary = ""
	}
	return history, summary
}

// contextUsageFromMessages estimates usage for a concrete prompt context.
// summary must be the summary BuildMessages will inject, which is why assembled
// contexts pass the ContextManager's summary instead of the session store's.
func contextUsageFromMessages(
	agent *AgentInstance,
	history []providers.Message,
	summary string,
) *bus.ContextUsage {
	if agent == nil {
		return nil
	}
	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		return nil
	}

	historyTokens := 0
	for _, m := range history {
		historyTokens += EstimateMessageTokens(m)
	}

	// System message tokens: uses EstimateSystemTokens which mirrors
	// the full system message composition in BuildMessages (static prompt,
	// dynamic context, active skills, summary with wrapping prefix).
	systemTokens := 0
	if agent.ContextBuilder != nil {
		// Pass nil for active skills: skills are only injected when the user
		// explicitly activates them via /use, which is rare. Using nil matches
		// the common case and avoids over-counting all installed skills.
		systemTokens = agent.ContextBuilder.EstimateSystemTokens(summary, nil)
	}

	// Tool definition tokens
	toolTokens := 0
	if agent.Tools != nil {
		toolTokens = EstimateToolDefsTokens(agent.Tools.ToProviderDefs())
	}

	// Used = history + system (includes summary) + tools
	usedTokens := historyTokens + systemTokens + toolTokens

	// Effective budget = contextWindow minus output reserve (maxTokens)
	effectiveWindow := usableContextTokens(agent)

	// compressAt = effectiveWindow: aligns with isOverContextBudget's
	// proactive trigger (msgTokens + toolTokens + maxTokens > contextWindow).
	compressAt := effectiveWindow

	// summarizeAt = soft summarization trigger, compared against UsedTokens
	// before every LLM call (see Pipeline.CompactBeforeLLM).
	summarizeAt := summarizeThresholdTokens(agent)

	usedPercent := 0
	if compressAt > 0 {
		usedPercent = usedTokens * 100 / compressAt
	}
	if usedPercent > 100 {
		usedPercent = 100
	}

	return &bus.ContextUsage{
		UsedTokens:        usedTokens,
		TotalTokens:       contextWindow,
		HistoryTokens:     historyTokens,
		CompressAtTokens:  compressAt,
		SummarizeAtTokens: summarizeAt,
		UsedPercent:       usedPercent,
	}
}

// usableContextTokens is the prompt budget left once the output reserve
// (MaxTokens) is taken out of the context window.
func usableContextTokens(agent *AgentInstance) int {
	if usable := agent.ContextWindow - agent.MaxTokens; usable >= 0 {
		return usable
	}
	return agent.ContextWindow
}

// summarizeThresholdTokens is the prompt size at which summarization starts:
// SummarizeTokenPercent of the usable window, capped at the window itself so
// summarization never fires later than emergency compression.
func summarizeThresholdTokens(agent *AgentInstance) int {
	usable := usableContextTokens(agent)
	threshold := usable * agent.SummarizeTokenPercent / 100
	if threshold <= 0 || threshold > usable {
		return usable
	}
	return threshold
}
