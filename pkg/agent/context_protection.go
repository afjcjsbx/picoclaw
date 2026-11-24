// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import "github.com/sipeed/picoclaw/pkg/providers"

const (
	// defaultProtectFirstN is the number of leading conversation messages kept
	// verbatim by the legacy context manager. The history does not contain the
	// system prompt; 1 keeps the opening user instruction, and larger values
	// also pin the first assistant/tool exchange.
	defaultProtectFirstN = 1
	// defaultProtectLastN is the minimum number of trailing messages kept
	// verbatim. tailTokenBudget can protect more.
	defaultProtectLastN = 4
	// defaultMinTailUserMessages guarantees at least one real user message in
	// the unprotected tail.
	defaultMinTailUserMessages = 1

	autoTailBudgetPercent = 10
	minAutoTailBudget     = 2_000
	maxAutoTailBudget     = 32_000
)

// contextProtection is the resolved head/tail policy for one agent.
type contextProtection struct {
	protectFirstN       int
	protectLastN        int
	minTailUserMessages int
	tailTokenBudget     int
}

// historyProtection describes where the protected head and tail start in a
// history slice. ok is false when there is no compressible middle.
type historyProtection struct {
	headEnd         int
	tailStart       int
	tailStartFull   int
	limit           int
	tailTokenBudget int
	ok              bool
}

// autoTailTokenBudget derives the default tail budget from the usable input
// window: 10%, clamped to 2,000-32,000 tokens and never more than half of the
// window. A non-positive window disables the budget.
func autoTailTokenBudget(contextWindow, maxTokens int) int {
	usable := contextWindow - maxTokens
	if usable <= 0 {
		usable = contextWindow
	}
	if usable <= 0 {
		return 0
	}

	budget := usable * autoTailBudgetPercent / 100
	if budget < minAutoTailBudget {
		budget = minAutoTailBudget
	}
	if budget > maxAutoTailBudget {
		budget = maxAutoTailBudget
	}
	if half := usable / 2; budget > half {
		budget = half
	}
	return budget
}

// resolveContextProtection reads the policy fields resolved into the agent at
// construction time.
func resolveContextProtection(agent *AgentInstance) contextProtection {
	if agent == nil {
		return contextProtection{
			protectFirstN:       defaultProtectFirstN,
			protectLastN:        defaultProtectLastN,
			minTailUserMessages: defaultMinTailUserMessages,
		}
	}
	return contextProtection{
		protectFirstN:       max(0, agent.ProtectFirstN),
		protectLastN:        max(0, agent.ProtectLastN),
		minTailUserMessages: max(0, agent.MinTailUserMessages),
		tailTokenBudget:     max(0, agent.TailTokenBudget),
	}
}

// computeHistoryProtection resolves the protected head and tail for history
// [:limit]. limit is the end of the region eligible for compaction: it excludes
// the active turn so compaction never folds it into a summary.
//
// The head is a fixed message count, aligned forward so an assistant tool call
// is never split from its tool results. The tail is the union of the
// protect_last_n message floor, a backward token-budget walk, and the
// min_tail_user_messages guarantee; it starts at a turn boundary so tool-call
// sequences are not torn apart.
func computeHistoryProtection(
	history []providers.Message,
	limit int,
	agent *AgentInstance,
) historyProtection {
	if limit < 0 {
		limit = 0
	}
	if limit > len(history) {
		limit = len(history)
	}
	cfg := resolveContextProtection(agent)
	headEnd := alignHeadEnd(history, min(cfg.protectFirstN, limit), limit)

	// Resolve the tail against the full history. The active turn (history[limit:])
	// is protected by limit itself, so a tail that starts inside it does not
	// additionally protect older messages. Capping the cut at limit preserves
	// that distinction while the token budget can still reach into earlier
	// turns when the active turn is small.
	tailStartFull := computeTailStart(history, len(history), headEnd, cfg, agentProvider(agent))
	tailStart := min(tailStartFull, limit)
	if limit > 0 {
		tailStart = findSafeBoundary(history[:limit], tailStart)
	}

	return historyProtection{
		headEnd:         headEnd,
		tailStart:       tailStart,
		tailStartFull:   tailStartFull,
		limit:           limit,
		tailTokenBudget: cfg.tailTokenBudget,
		ok:              tailStart > headEnd && tailStart > 0,
	}
}

// pruneProtectedHistory evicts tool outputs in the unprotected middle and, if
// the protected tail is over budget, in the tail as well. The head is never
// touched.
func pruneProtectedHistory(
	history []providers.Message,
	protection historyProtection,
	limit int,
	agent *AgentInstance,
) bool {
	if agent == nil {
		return false
	}
	historicalTailStart := min(protection.tailStartFull, limit)
	if historicalTailStart < protection.headEnd {
		historicalTailStart = protection.headEnd
	}
	pruned := pruneToolResultsInRange(
		history,
		protection.headEnd,
		historicalTailStart,
		agent.Workspace,
	)
	if pruneToolResultsToBudget(
		history,
		historicalTailStart,
		limit,
		agent.Workspace,
		agent.Provider,
		protection.tailTokenBudget,
	) {
		pruned = true
	}
	return pruned
}

func agentProvider(agent *AgentInstance) providers.LLMProvider {
	if agent == nil {
		return nil
	}
	return agent.Provider
}

// alignHeadEnd extends the head forward while the next message is a tool
// result, keeping the protected prefix from splitting a tool-call group.
func alignHeadEnd(history []providers.Message, end, limit int) int {
	if end <= 0 {
		return 0
	}
	if end > limit {
		end = limit
	}
	for end < limit && history[end].Role == "tool" {
		end++
	}
	return end
}

// computeTailStart walks backwards from limit: first the protect_last_n message
// floor, then the token budget, then the minimum number of user messages. It
// never crosses headEnd.
func computeTailStart(
	history []providers.Message,
	limit, headEnd int,
	cfg contextProtection,
	provider providers.LLMProvider,
) int {
	if limit <= headEnd {
		return limit
	}

	start := limit
	if cfg.protectLastN > 0 {
		start = limit - cfg.protectLastN
	}
	if start < headEnd {
		start = headEnd
	}

	tokens := estimateRangeTokens(history, start, limit, provider)
	if cfg.tailTokenBudget > 0 {
		for start > headEnd && tokens < cfg.tailTokenBudget {
			start--
			tokens += estimateMessageTokensForProvider(provider, history[start])
		}
	}

	if cfg.minTailUserMessages > 0 {
		users := countUserMessages(history, start, limit)
		for users < cfg.minTailUserMessages && start > headEnd {
			start--
			if history[start].Role == "user" {
				users++
			}
		}
	}

	return start
}

// estimateRangeTokens sums the provider-aware token estimate for history[start:end].
func estimateRangeTokens(
	history []providers.Message,
	start, end int,
	provider providers.LLMProvider,
) int {
	if start < 0 {
		start = 0
	}
	if end > len(history) {
		end = len(history)
	}
	total := 0
	for i := start; i < end; i++ {
		total += estimateMessageTokensForProvider(provider, history[i])
	}
	return total
}

// countUserMessages counts user-role messages in history[start:end].
func countUserMessages(history []providers.Message, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(history) {
		end = len(history)
	}
	count := 0
	for i := start; i < end; i++ {
		if history[i].Role == "user" {
			count++
		}
	}
	return count
}
