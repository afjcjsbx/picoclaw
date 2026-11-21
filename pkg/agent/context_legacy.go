package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// legacyContextManager wraps the existing summarization/compression logic
// as a ContextManager implementation. It is the default when no other
// ContextManager is configured.
type legacyContextManager struct {
	al          *AgentLoop
	summarizing sync.Map // dedup so /compact and threshold compaction never truncate one session concurrently
}

func (m *legacyContextManager) Assemble(ctx context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	// Legacy: read history from session, return as-is.
	// Budget enforcement happens in BuildMessages caller via
	// isOverContextBudget + forceCompression.
	// Assemble the history of the agent that owns the session, like Compact and
	// Clear do, so routed agents do not fall back to the default agent's store.
	agent := m.al.agentForSession(req.SessionKey)
	if agent == nil {
		return &AssembleResponse{}, nil
	}
	history, err := agent.Sessions.GetHistory(ctx, req.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("assemble history: %w", err)
	}
	summary, err := agent.Sessions.GetSummary(ctx, req.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("assemble summary: %w", err)
	}
	return &AssembleResponse{
		History: history,
		Summary: summary,
	}, nil
}

func (m *legacyContextManager) Compact(ctx context.Context, req *CompactRequest) error {
	switch req.Reason {
	case ContextCompressReasonProactive, ContextCompressReasonRetry:
		// Sync emergency compression — budget exceeded.
		if result, ok := m.forceCompression(req.SessionKey); ok {
			m.al.emitEvent(
				runtimeevents.KindAgentContextCompress,
				m.al.newTurnEventScope(
					m.al.eventAgentIDForSession(ctx, req.SessionKey),
					req.SessionKey,
					nil,
				).meta(0, "forceCompression", "turn.context.compress"),
				ContextCompressPayload{
					Reason:            req.Reason,
					DroppedMessages:   result.DroppedMessages,
					RemainingMessages: result.RemainingMessages,
				},
			)
			m.al.notifyContextFeedbackFromCtx(
				ctx,
				req.SessionKey,
				formatContextCompactFeedback(result.DroppedMessages, result.RemainingMessages),
			)
		}
	case ContextCompressReasonSummarize:
		// Legacy compacts synchronously right before an LLM call
		// (ContextCompressReasonThreshold), never after the turn.
	case ContextCompressReasonThreshold:
		// Same agent as Assemble, so the rebuilt prompt reads what was compacted.
		return m.compactSession(ctx, m.al.agentForSession(req.SessionKey), req)
	case ContextCompressReasonManual:
		err := m.compactSession(ctx, m.al.agentForSession(req.SessionKey), req)
		if req.Outcome != nil {
			switch {
			case err == nil:
				req.Outcome.Compacted = true
				req.Outcome.Reason = CompactReasonCompacted
			case errors.Is(err, ErrNothingToCompact):
				req.Outcome.Reason = CompactReasonNoCandidates
			}
		}
		return err
	}
	return nil
}

// compactSession summarizes the session synchronously, for /compact and for the
// summarize threshold reached before an LLM call. When the threshold is what
// triggered it, it summarizes earlier Turns first, then the middle of the turn
// in progress, and as a last resort clears old tool outputs. ErrNothingToCompact
// means the session did not change.
func (m *legacyContextManager) compactSession(ctx context.Context, agent *AgentInstance, req *CompactRequest) error {
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("sessions not initialized")
	}
	summarizeKey := agent.ID + ":" + req.SessionKey
	if _, loading := m.summarizing.LoadOrStore(summarizeKey, true); loading {
		return fmt.Errorf("a summarization is already in progress")
	}
	defer m.summarizing.Delete(summarizeKey)

	// /compact reports its own outcome; the threshold path notifies the turn's chat.
	var ts *turnState
	if req.Reason == ContextCompressReasonThreshold {
		if ts = turnStateFromContext(ctx); ts == nil {
			ts = m.al.getActiveTurnState(req.SessionKey)
		}
	}
	if m.summarizeSession(ctx, agent, req.SessionKey, ts, req.Instructions) {
		return nil
	}
	if req.Reason == ContextCompressReasonThreshold {
		// Nothing older than the turn left: summarize the turn's own middle.
		if ts != nil && m.summarizeActiveTurn(ctx, agent, req.SessionKey, ts) {
			return nil
		}
		if remaining, ok := m.pruneSessionToolResults(ctx, agent, req.SessionKey, ts); ok {
			m.al.emitEvent(
				runtimeevents.KindAgentContextCompress,
				m.al.newTurnEventScope(agent.ID, req.SessionKey, nil).
					meta(0, "compactSession", "turn.context.compress"),
				ContextCompressPayload{Reason: req.Reason, RemainingMessages: remaining},
			)
			return nil
		}
	}
	return ErrNothingToCompact
}

func (m *legacyContextManager) Ingest(_ context.Context, _ *IngestRequest) error {
	// Legacy: no-op. Messages are persisted by Sessions JSONL.
	return nil
}

func (m *legacyContextManager) Clear(ctx context.Context, sessionKey string) error {
	// Routed (non-default) agents keep history in their own session store,
	// so resolve the owning agent instead of assuming the default one.
	agent := m.al.agentForSession(sessionKey)
	if agent == nil || agent.Sessions == nil {
		return fmt.Errorf("sessions not initialized")
	}
	warnSessionStoreError(sessionKey, "set history", agent.Sessions.SetHistory(ctx, sessionKey, []providers.Message{}))
	warnSessionStoreError(sessionKey, "set summary", agent.Sessions.SetSummary(ctx, sessionKey, ""))
	return agent.Sessions.Save(ctx, sessionKey)
}

const (
	// prunedToolResult replaces bulky tool outputs outside the protected tail
	// when they could not be archived.
	prunedToolResult = "[Old tool output cleared to save context space]"
	// archivedToolResultPrefix starts the stub that replaces an archived tool
	// output; it also marks a result as already evicted.
	archivedToolResultPrefix = "[Old tool output archived at "
	protectedTail            = 4
	minToolResultChars       = 200
)

// isEvictedToolResult reports whether content is a stub left by pruning.
func isEvictedToolResult(content string) bool {
	return content == prunedToolResult || strings.HasPrefix(content, archivedToolResultPrefix)
}

// archiveToolResult saves a tool output before it is evicted and returns the
// stub that replaces it in the history; the model recovers the output with
// read_file. An empty workspace, a write failure, or a stub no shorter than the
// output (a deep workspace path) falls back to prunedToolResult. Files go in
// their own artifact dir so they and live tool spills do not push each other
// out of the retention bound.
func archiveToolResult(workspace, content string) string {
	if workspace == "" {
		return prunedToolResult
	}
	path, err := toolshared.PersistTextArtifact(
		filepath.Join(workspace, ".artifacts", "evicted"), "tool_result_*.txt", content)
	if err != nil {
		return prunedToolResult
	}
	stub := archivedToolResultPrefix + path + "; read_file it, or re-run the tool]"
	if len(stub) >= len(content) {
		_ = os.Remove(path)
		return prunedToolResult
	}
	return stub
}

// resultLen is len(content) without trailing budget notices, so a notice alone
// never makes a short result bulky enough to be pruned.
func resultLen(content string) int {
	n := len(content)
	for _, notice := range [...]string{budgetNoticePrefix, runBudgetNotice} {
		if i := strings.LastIndex(content, "\n\n"+notice); i >= 0 {
			n = min(n, i)
		}
	}
	return n
}

// pruneOldToolResults evicts tool outputs longer than minToolResultChars that
// sit before the last protectedTail messages, in place: each is archived under
// workspace and replaced by a stub pointing at the file. Media-carrying results
// are kept. It reports whether anything changed.
func pruneOldToolResults(history []providers.Message, workspace string) bool {
	return pruneToolResultsBefore(history, len(history)-protectedTail, workspace)
}

// pruneToolResultsBefore is pruneOldToolResults for history[:end].
func pruneToolResultsBefore(history []providers.Message, end int, workspace string) bool {
	pruned := false
	for i := 0; i < end; i++ {
		if history[i].Role == "tool" && resultLen(history[i].Content) > minToolResultChars &&
			len(history[i].Media) == 0 && len(history[i].Attachments) == 0 &&
			!isEvictedToolResult(history[i].Content) {
			history[i].Content = archiveToolResult(workspace, history[i].Content)
			pruned = true
		}
	}
	return pruned
}

// pruneSessionToolResults applies pruneOldToolResults to the stored history and
// reports the resulting message count. When ts is the turn in progress, its
// newest keepActiveRounds rounds are kept whole as well: the newest round holds
// results the model has not seen yet.
func (m *legacyContextManager) pruneSessionToolResults(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
	ts *turnState,
) (remaining int, ok bool) {
	history, err := agent.Sessions.GetHistory(ctx, sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get history", err)
		return 0, false
	}
	end, activeLen := len(history)-protectedTail, 0
	if ts != nil {
		persisted := ts.persistedMessagesSnapshot()
		activeLen = matchingTurnMessageTail(history, persisted)
		end = min(end, activeTurnPruneEnd(history, persisted))
	}
	if !pruneToolResultsBefore(history, end, agent.Workspace) {
		return 0, false
	}
	if activeLen > 0 {
		// The turn's record must keep matching the session, or the active turn
		// (and the hard-abort restore point) would shrink to the unpruned tail.
		ts.replacePersistedMessages(history[len(history)-activeLen:])
	}
	warnSessionStoreError(sessionKey, "set history", agent.Sessions.SetHistory(ctx, sessionKey, history))
	warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(ctx, sessionKey))
	return len(history), true
}

type compressionResult struct {
	DroppedMessages   int
	RemainingMessages int
}

// forceCompression aggressively reduces context when the limit is hit.
// It drops the oldest ~50% of Turns (a Turn is a complete user→LLM→response
// cycle, as defined in #1316), so tool-call sequences are never split.
func (m *legacyContextManager) forceCompression(sessionKey string) (compressionResult, bool) {
	agent := m.al.agentForSession(sessionKey)
	if agent == nil {
		return compressionResult{}, false
	}

	// forceCompression has no context of its own: it only reads and writes the
	// local session store, so a background context is sufficient.
	history, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get history", err)
		history = []providers.Message{}
	}
	if pruneOldToolResults(history, agent.Workspace) {
		warnSessionStoreError(
			sessionKey,
			"set history",
			agent.Sessions.SetHistory(context.Background(), sessionKey, history),
		)
		warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(context.Background(), sessionKey))
		return compressionResult{RemainingMessages: len(history)}, true
	}
	if len(history) <= 2 {
		return compressionResult{}, false
	}

	turns := parseTurnBoundaries(history)
	var mid int
	if len(turns) >= 2 {
		mid = turns[len(turns)/2]
	} else {
		mid = findSafeBoundary(history, len(history)/2)
	}
	var keptHistory []providers.Message
	if mid <= 0 {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				keptHistory = []providers.Message{history[i]}
				break
			}
		}
	} else {
		keptHistory = history[mid:]
	}

	droppedCount := len(history) - len(keptHistory)

	existingSummary, err := agent.Sessions.GetSummary(context.Background(), sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get summary", err)
		existingSummary = ""
	}
	compressionNote := fmt.Sprintf(
		"[Emergency compression dropped %d oldest messages due to context limit]",
		droppedCount,
	)
	if existingSummary != "" {
		compressionNote = existingSummary + "\n\n" + compressionNote
	}
	warnSessionStoreError(
		sessionKey,
		"set summary",
		agent.Sessions.SetSummary(context.Background(), sessionKey, compressionNote),
	)

	warnSessionStoreError(
		sessionKey,
		"set history",
		agent.Sessions.SetHistory(context.Background(), sessionKey, keptHistory),
	)
	warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(context.Background(), sessionKey))

	logger.WarnCF("agent", "Forced compression executed", map[string]any{
		"session_key":  sessionKey,
		"dropped_msgs": droppedCount,
		"new_count":    len(keptHistory),
	})

	return compressionResult{
		DroppedMessages:   droppedCount,
		RemainingMessages: len(keptHistory),
	}, true
}

// summarizeSession summarizes all but the most recent messages, synchronously. focus, when
// non-empty, tells the summarizer which details to preserve. It reports whether
// a summary was applied.
func (m *legacyContextManager) summarizeSession(
	parent context.Context,
	agent *AgentInstance,
	sessionKey string,
	ts *turnState,
	focus string,
) bool {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()

	history, err := agent.Sessions.GetHistory(ctx, sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get history", err)
		history = []providers.Message{}
	}
	summary, err := agent.Sessions.GetSummary(ctx, sessionKey)
	if err != nil {
		warnSessionStoreError(sessionKey, "get summary", err)
		summary = ""
	}

	if len(history) <= 4 {
		return false
	}

	// The turn in progress is never cut: a steering message inside it is a user
	// message too, and would otherwise look like a Turn boundary.
	limit := len(history)
	if ts != nil {
		limit -= matchingTurnMessageTail(history, ts.persistedMessagesSnapshot())
	}
	safeCut := findSafeBoundary(history, min(len(history)-4, limit))
	if safeCut <= 0 || safeCut > limit {
		return false
	}
	keepCount := len(history) - safeCut
	toSummarize := history[:safeCut]

	// Completed turns include tool calls and tool results: files read or
	// modified, commands run, and errors encountered are often the only record
	// of what happened. turnTranscript renders them, labels tool results by
	// name, and clips one oversized payload so the summarizer prompt stays
	// bounded.
	transcript := m.turnTranscript(toSummarize)
	if len(transcript) == 0 {
		return false
	}

	finalSummary := m.summarizeTranscript(ctx, agent, transcript, summary, focus)

	// A canceled caller (hard abort) must not turn a half-finished summarization
	// into a degraded summary that replaces the real history.
	if parent.Err() != nil {
		return false
	}

	if finalSummary != "" {
		warnSessionStoreError(sessionKey, "set summary", agent.Sessions.SetSummary(ctx, sessionKey, finalSummary))
		warnSessionStoreError(
			sessionKey,
			"truncate history",
			agent.Sessions.TruncateHistory(ctx, sessionKey, keepCount),
		)
		warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(ctx, sessionKey))
		m.al.emitEvent(
			runtimeevents.KindAgentSessionSummarize,
			m.al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "summarizeSession", "turn.session.summarize"),
			SessionSummarizePayload{
				SummarizedMessages: len(toSummarize),
				KeptMessages:       keepCount,
				SummaryLen:         len(finalSummary),
			},
		)
		m.al.notifyContextFeedback(
			ts,
			formatContextSummarizeFeedback(len(toSummarize), keepCount),
		)
		return true
	}
	return false
}

func (m *legacyContextManager) retryLLMCall(
	ctx context.Context,
	agent *AgentInstance,
	prompt string,
	maxRetries int,
	maxTokens int,
) (*providers.LLMResponse, error) {
	const llmTemperature = 0.3

	if maxTokens <= 0 {
		maxTokens = agent.MaxTokens
	}

	var resp *providers.LLMResponse
	var err error
	// The provider wants the model ID, not the model_list alias agent.Model holds.
	model := resolvedCandidateModel(agent.Candidates, agent.Model)

	for attempt := 0; attempt < maxRetries; attempt++ {
		m.al.activeRequestsInc()
		resp, err = func() (*providers.LLMResponse, error) {
			defer m.al.activeRequestsDec()
			return chatRetryingTransient(
				ctx,
				agent.Provider,
				[]providers.Message{{Role: "user", Content: prompt}},
				nil,
				model,
				map[string]any{
					"max_tokens":       maxTokens,
					"temperature":      llmTemperature,
					"prompt_cache_key": agent.ID,
				},
				llmRetryFromConfig(m.al.cfg),
			)
		}()

		if err == nil && resp != nil && resp.Content != "" {
			return resp, nil
		}
		// A transient failure was already retried with backoff.
		if _, transient := transientLLMRetryReason(err); transient {
			break
		}
		if attempt < maxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}

	return resp, err
}

func (m *legacyContextManager) summarizeBatch(
	ctx context.Context,
	agent *AgentInstance,
	batch []providers.Message,
	existingSummary string,
	focus string,
) (string, error) {
	const llmMaxRetries = 3

	existingSummary = strings.TrimSpace(m.redactSummaryText(existingSummary))
	existingSummary = clipSummaryText(existingSummary, summaryMaxRunes(agent))
	focus = strings.TrimSpace(m.redactSummaryText(focus))

	var conversation strings.Builder
	for _, msg := range batch {
		content := strings.TrimSpace(m.redactSummaryText(msg.Content))
		if content == "" {
			continue
		}
		fmt.Fprintf(&conversation, "%s: %s\n", msg.Role, content)
	}

	prompt := buildSummaryPrompt(existingSummary, conversation.String(), focus)

	response, err := m.retryLLMCall(ctx, agent, prompt, llmMaxRetries, summaryMaxTokens(agent))
	if err == nil && response != nil && strings.TrimSpace(response.Content) != "" {
		summary := strings.TrimSpace(m.redactSummaryText(response.Content))
		summary = clipSummaryText(summary, summaryMaxRunes(agent))
		if summary != "" {
			return summary, nil
		}
	}

	fallback := m.fallbackSummary(batch, existingSummary)
	fallback = clipSummaryText(fallback, summaryMaxRunes(agent))
	return fallback, nil
}
