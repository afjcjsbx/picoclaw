// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

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

// ContextProtection is the resolved legacy context head/tail policy for one
// agent.
type ContextProtection struct {
	ProtectFirstN       int
	ProtectLastN        int
	MinTailUserMessages int
	TailTokenBudget     int
}

// historyProtection describes where the protected head and tail start in a
// history slice. ok is false when there is no compressible middle.
type historyProtection struct {
	headEnd             int
	snappedTailStart    int
	tailStartBeforeSnap int
	compactableEnd      int
	tailTokenBudget     int
	ok                  bool
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

// resolveContextProtection merges agents.defaults with the agent's resolved
// context window. Nil fields use the documented defaults; explicit zeros are
// preserved and disable the corresponding protection.
func resolveContextProtection(
	defaults *config.AgentDefaults,
	contextWindow, maxTokens int,
) ContextProtection {
	protection := ContextProtection{
		ProtectFirstN:       defaultProtectFirstN,
		ProtectLastN:        defaultProtectLastN,
		MinTailUserMessages: defaultMinTailUserMessages,
		TailTokenBudget:     autoTailTokenBudget(contextWindow, maxTokens),
	}
	if defaults.ProtectFirstN != nil {
		protection.ProtectFirstN = max(0, *defaults.ProtectFirstN)
	}
	if defaults.ProtectLastN != nil {
		protection.ProtectLastN = max(0, *defaults.ProtectLastN)
	}
	if defaults.MinTailUserMessages != nil {
		protection.MinTailUserMessages = max(0, *defaults.MinTailUserMessages)
	}
	if defaults.TailTokenBudget != nil {
		protection.TailTokenBudget = max(0, *defaults.TailTokenBudget)
	}
	return protection
}

// computeHistoryProtection resolves the protected head and tail for
// history[:compactableEnd]. compactableEnd is the end of the region eligible
// for compaction: it excludes the active turn so compaction never folds it
// into a summary.
//
// The head is a fixed message count, aligned forward so an assistant tool call
// is never split from its tool results. The tail is the union of the
// protect_last_n message floor, a backward token-budget walk, and the
// min_tail_user_messages guarantee; it starts at a turn boundary so tool-call
// sequences are not torn apart. agent must be non-nil.
func computeHistoryProtection(
	history []providers.Message,
	compactableEnd int,
	agent *AgentInstance,
) historyProtection {
	if compactableEnd < 0 {
		compactableEnd = 0
	}
	if compactableEnd > len(history) {
		compactableEnd = len(history)
	}
	cfg := agent.Protection
	headEnd := alignHeadEnd(history, min(cfg.ProtectFirstN, compactableEnd), compactableEnd)

	// Resolve the tail against the full history. The active turn
	// (history[compactableEnd:]) is protected by compactableEnd itself, so a
	// tail that starts inside it does not additionally protect older messages.
	// Capping the cut at compactableEnd preserves that distinction while the
	// token budget can still reach into earlier turns when the active turn is
	// small.
	tailStartBeforeSnap := computeTailStart(history, len(history), headEnd, cfg, agent.Provider)
	snappedTailStart := min(tailStartBeforeSnap, compactableEnd)
	if compactableEnd > 0 {
		snappedTailStart = snapTailStart(history, snappedTailStart, compactableEnd)
	}

	return historyProtection{
		headEnd:             headEnd,
		snappedTailStart:    snappedTailStart,
		tailStartBeforeSnap: tailStartBeforeSnap,
		compactableEnd:      compactableEnd,
		tailTokenBudget:     cfg.TailTokenBudget,
		ok:                  snappedTailStart > headEnd && snappedTailStart > 0,
	}
}

// snapTailStart finds the Turn boundary for the tail. The active-turn boundary
// at compactableEnd is a valid cut when it starts a Turn, even though it sits
// after the tail target; that lets an older Turn be summarized before a short
// active turn. Otherwise the tail is snapped within the compactable history.
func snapTailStart(history []providers.Message, target, compactableEnd int) int {
	if compactableEnd > 0 && isSafeBoundary(history, compactableEnd) {
		snapped := findSafeBoundary(history, target)
		if snapped > compactableEnd {
			snapped = compactableEnd
		}
		return snapped
	}
	return findSafeBoundary(history[:compactableEnd], target)
}

// pruneProtectedHistory evicts tool outputs in the unprotected middle and, if
// the protected tail is over budget, in the tail as well. The head is never
// touched. agent must be non-nil.
func pruneProtectedHistory(
	history []providers.Message,
	protection historyProtection,
	agent *AgentInstance,
) bool {
	// Prefer the snapped tail when it is a real protected boundary (a backward
	// snap). When there is no safe Turn boundary, the unsnapped target still
	// separates old outputs from the floors; pruning does not need a Turn cut
	// because it keeps every message.
	tailStart := protection.tailStartBeforeSnap
	if protection.ok && protection.snappedTailStart < tailStart {
		tailStart = protection.snappedTailStart
	}
	tailStart = min(tailStart, protection.compactableEnd)
	tailStart = max(tailStart, protection.headEnd)
	pruned := pruneToolResultsInRange(
		history,
		protection.headEnd,
		tailStart,
		agent.Workspace,
	)
	if pruneToolResultsToBudget(
		history,
		tailStart,
		protection.compactableEnd,
		len(history),
		agent.Workspace,
		agent.Provider,
		protection.tailTokenBudget,
	) {
		pruned = true
	}
	return pruned
}

// alignHeadEnd extends the head forward while the next message is a tool
// result, keeping the protected prefix from splitting a tool-call group.
func alignHeadEnd(history []providers.Message, end, compactableEnd int) int {
	if end <= 0 {
		return 0
	}
	if end > compactableEnd {
		end = compactableEnd
	}
	for end < compactableEnd && history[end].Role == "tool" {
		end++
	}
	return end
}

// computeTailStart walks backwards from compactableEnd: first the protect_last_n
// message floor, then the token budget, then the minimum number of user messages.
// It never crosses headEnd.
func computeTailStart(
	history []providers.Message,
	compactableEnd, headEnd int,
	cfg ContextProtection,
	provider providers.LLMProvider,
) int {
	if compactableEnd <= headEnd {
		return compactableEnd
	}

	start := compactableEnd
	if cfg.ProtectLastN > 0 {
		start = compactableEnd - cfg.ProtectLastN
	}
	if start < headEnd {
		start = headEnd
	}

	tokens := estimateRangeTokens(history, start, compactableEnd, provider)
	if cfg.TailTokenBudget > 0 {
		for start > headEnd && tokens < cfg.TailTokenBudget {
			start--
			tokens += estimateMessageTokensForProvider(provider, history[start])
		}
	}

	if cfg.MinTailUserMessages > 0 {
		users := countUserMessages(history, start, compactableEnd)
		for users < cfg.MinTailUserMessages && start > headEnd {
			start--
			if history[start].Role == "user" {
				users++
			}
		}
	}

	return start
}

// clampHistoryRange bounds [start, end) to history. Callers must check for an
// empty range because start may be greater than or equal to end after clamping.
func clampHistoryRange(history []providers.Message, start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end > len(history) {
		end = len(history)
	}
	return start, end
}

// estimateRangeTokens sums the provider-aware token estimate for history[start:end].
func estimateRangeTokens(
	history []providers.Message,
	start, end int,
	provider providers.LLMProvider,
) int {
	start, end = clampHistoryRange(history, start, end)
	total := 0
	for i := start; i < end; i++ {
		total += estimateMessageTokensForProvider(provider, history[i])
	}
	return total
}

// countUserMessages counts user-role messages in history[start:end].
func countUserMessages(history []providers.Message, start, end int) int {
	start, end = clampHistoryRange(history, start, end)
	count := 0
	for i := start; i < end; i++ {
		if history[i].Role == "user" {
			count++
		}
	}
	return count
}
