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
		// Sync emergency compression — budget exceeded. The active turn is
		// protected during pruning and middle-dropping, so its record stays
		// aligned with the session.
		ts := turnStateFromContext(ctx)
		if ts == nil {
			ts = m.al.getActiveTurnState(req.SessionKey)
		}
		if result, ok := m.forceCompression(req.SessionKey, ts); ok {
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
// sit outside the protected head and tail, in place: each is archived under
// workspace and replaced by a stub pointing at the file. Media-carrying results
// are kept. When the protected tail itself exceeds its token budget, its old
// tool outputs are evicted too, so one huge result cannot force the whole
// history to be dropped. It reports whether anything changed.
func pruneOldToolResults(history []providers.Message, agent *AgentInstance, limit int) bool {
	if agent == nil {
		return false
	}
	protection := computeHistoryProtection(history, limit, agent)
	return pruneProtectedHistory(history, protection, limit, agent)
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
	pruned := pruneToolResultsInRange(history, protection.headEnd, historicalTailStart, agent.Workspace)
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

// pruneToolResultsBefore is pruneToolResultsInRange for history[:end].
func pruneToolResultsBefore(history []providers.Message, end int, workspace string) bool {
	return pruneToolResultsInRange(history, 0, end, workspace)
}

// canEvictToolResult reports whether a tool message is a bulky, non-media
// output that has not already been evicted.
func canEvictToolResult(msg providers.Message) bool {
	return msg.Role == "tool" && resultLen(msg.Content) > minToolResultChars &&
		len(msg.Media) == 0 && len(msg.Attachments) == 0 &&
		!isEvictedToolResult(msg.Content)
}

// pruneToolResultsInRange evicts bulky tool outputs in history[start:end].
func pruneToolResultsInRange(history []providers.Message, start, end int, workspace string) bool {
	if start < 0 {
		start = 0
	}
	if end > len(history) {
		end = len(history)
	}
	if start >= end {
		return false
	}

	pruned := false
	for i := start; i < end; i++ {
		if canEvictToolResult(history[i]) {
			history[i].Content = archiveToolResult(workspace, history[i].Content)
			pruned = true
		}
	}
	return pruned
}

// pruneToolResultsToBudget evicts the oldest bulky tool outputs in
// history[start:end] until the range fits budget. It stops as soon as the
// range fits, so newer tail results survive when an older one is enough.
func pruneToolResultsToBudget(
	history []providers.Message,
	start, end int,
	workspace string,
	provider providers.LLMProvider,
	budget int,
) bool {
	if budget <= 0 {
		return false
	}
	if start < 0 {
		start = 0
	}
	if end > len(history) {
		end = len(history)
	}
	if start >= end {
		return false
	}

	tokens := estimateRangeTokens(history, start, end, provider)
	if tokens <= budget {
		return false
	}

	pruned := false
	for i := start; i < end && tokens > budget; i++ {
		if !canEvictToolResult(history[i]) {
			continue
		}
		before := estimateMessageTokensForProvider(provider, history[i])
		history[i].Content = archiveToolResult(workspace, history[i].Content)
		after := estimateMessageTokensForProvider(provider, history[i])
		tokens -= before - after
		pruned = true
	}
	return pruned
}

// pruneSessionToolResults applies the head/tail protection to the stored
// history and reports the resulting message count. When ts is the turn in
// progress, its newest keepActiveRounds rounds are kept whole as well: the
// newest round holds results the model has not seen yet.
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

	limit := len(history)
	activeLen := 0
	activeEnd := limit
	if ts != nil {
		persisted := ts.persistedMessagesSnapshot()
		activeLen = matchingTurnMessageTail(history, persisted)
		if activeLen > 0 {
			limit -= activeLen
		}
		// Older rounds of the active turn are prunable; its newest
		// keepActiveRounds rounds and the turn's opening message are not.
		activeEnd = activeTurnPruneEnd(history, persisted)
	}

	protection := computeHistoryProtection(history, limit, agent)
	pruned := pruneProtectedHistory(history, protection, limit, agent)
	if activeEnd > limit {
		if pruneToolResultsInRange(history, limit, activeEnd, agent.Workspace) {
			pruned = true
		}
	}
	if !pruned {
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

// forceCompression aggressively reduces context when the limit is hit. It
// keeps the protected head and the newest ~50% of Turns (a Turn is a complete
// user→LLM→response cycle, as defined in #1316) where possible, dropping the
// middle so tool-call sequences are never split. It falls back to dropping the
// head only when there is no room for a middle.
func (m *legacyContextManager) forceCompression(sessionKey string, ts *turnState) (compressionResult, bool) {
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

	// The active turn is not pruned: its newest round holds results the model
	// has not seen yet, and keeping the suffix stable preserves the turn's
	// restore point.
	limit := len(history)
	if ts != nil {
		if activeLen := matchingTurnMessageTail(history, ts.persistedMessagesSnapshot()); activeLen > 0 {
			limit -= activeLen
		}
	}
	if pruneOldToolResults(history, agent, limit) {
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

	protection := computeHistoryProtection(history, limit, agent)
	turns := parseTurnBoundaries(history)
	var mid int
	if len(turns) >= 2 {
		mid = turns[len(turns)/2]
	} else {
		mid = findSafeBoundary(history, len(history)/2)
	}
	if limit > 0 && mid > limit {
		// Never cut into the active turn when older history can be dropped
		// instead; a steering message inside the turn is not a safe cut.
		mid = limit
	}

	var keptHistory []providers.Message
	keptHead := false
	if mid <= 0 {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" {
				keptHistory = []providers.Message{history[i]}
				break
			}
		}
	} else if mid > protection.headEnd {
		// Keep the protected opening and the newest half: drop the middle,
		// not the first instruction. The fallback below still drops the head
		// when there is no room for a middle.
		keptHistory = make([]providers.Message, 0, protection.headEnd+len(history)-mid)
		keptHistory = append(keptHistory, history[:protection.headEnd]...)
		keptHistory = append(keptHistory, history[mid:]...)
		keptHead = protection.headEnd > 0
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
	if keptHead {
		compressionNote = fmt.Sprintf(
			"[Emergency compression dropped %d messages from the middle due to context limit]",
			droppedCount,
		)
	}
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

// summarizeSession summarizes the middle between the protected head and tail,
// synchronously. focus, when non-empty, tells the summarizer which details to
// preserve. It reports whether a summary was applied.
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
	protection := computeHistoryProtection(history, limit, agent)
	if !protection.ok {
		return false
	}
	safeCut := protection.tailStart
	if safeCut <= protection.headEnd || safeCut > limit {
		return false
	}
	toSummarize := history[protection.headEnd:safeCut]
	keptHistory := make([]providers.Message, 0, protection.headEnd+len(history)-safeCut)
	keptHistory = append(keptHistory, history[:protection.headEnd]...)
	keptHistory = append(keptHistory, history[safeCut:]...)
	keepCount := len(keptHistory)

	maxMessageTokens := agent.ContextWindow / 2
	validMessages := make([]providers.Message, 0)
	omitted := false

	for _, msg := range toSummarize {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		msgTokens := len(msg.Content) / 2
		if msgTokens > maxMessageTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, msg)
	}

	if len(validMessages) == 0 {
		return false
	}

	const (
		maxSummarizationMessages = 10
		llmMaxRetries            = 3
	)

	var finalSummary string
	if len(validMessages) > maxSummarizationMessages {
		mid := len(validMessages) / 2
		mid = m.findNearestUserMessage(validMessages, mid)

		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := m.summarizeBatch(ctx, agent, part1, "", focus)
		s2, _ := m.summarizeBatch(ctx, agent, part2, "", focus)

		mergePrompt := fmt.Sprintf(
			"Merge these two conversation summaries into one cohesive summary:%s\n\n1: %s\n\n2: %s",
			focusInstruction(focus), s1, s2,
		)

		resp, err := m.retryLLMCall(ctx, agent, mergePrompt, llmMaxRetries)
		if err == nil && resp.Content != "" {
			finalSummary = resp.Content
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = m.summarizeBatch(ctx, agent, validMessages, summary, focus)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	// A canceled caller (hard abort) must not turn a half-finished summarization
	// into a degraded summary that replaces the real history.
	if parent.Err() != nil {
		return false
	}

	if finalSummary != "" {
		warnSessionStoreError(sessionKey, "set summary", agent.Sessions.SetSummary(ctx, sessionKey, finalSummary))
		warnSessionStoreError(
			sessionKey,
			"set history",
			agent.Sessions.SetHistory(ctx, sessionKey, keptHistory),
		)
		warnSessionStoreError(sessionKey, "save", agent.Sessions.Save(ctx, sessionKey))
		m.al.emitEvent(
			runtimeevents.KindAgentSessionSummarize,
			m.al.newTurnEventScope(agent.ID, sessionKey, nil).meta(0, "summarizeSession", "turn.session.summarize"),
			SessionSummarizePayload{
				SummarizedMessages: len(validMessages),
				KeptMessages:       keepCount,
				SummaryLen:         len(finalSummary),
				OmittedOversized:   omitted,
			},
		)
		m.al.notifyContextFeedback(
			ts,
			formatContextSummarizeFeedback(len(validMessages), keepCount),
		)
		return true
	}
	return false
}

func (m *legacyContextManager) findNearestUserMessage(messages []providers.Message, mid int) int {
	originalMid := mid

	for mid > 0 && messages[mid].Role != "user" {
		mid--
	}

	if messages[mid].Role == "user" {
		return mid
	}

	mid = originalMid
	for mid < len(messages) && messages[mid].Role != "user" {
		mid++
	}

	if mid < len(messages) {
		return mid
	}

	return originalMid
}

func (m *legacyContextManager) retryLLMCall(
	ctx context.Context,
	agent *AgentInstance,
	prompt string,
	maxRetries int,
) (*providers.LLMResponse, error) {
	const llmTemperature = 0.3

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
					"max_tokens":       agent.MaxTokens,
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
	const (
		llmMaxRetries             = 3
		fallbackMinContentLength  = 200
		fallbackMaxContentPercent = 10
	)

	var sb strings.Builder
	sb.WriteString("Provide a concise summary of this conversation segment, preserving core context and key points.")
	sb.WriteString(focusInstruction(focus))
	sb.WriteString("\n")
	if existingSummary != "" {
		sb.WriteString("Existing context: ")
		sb.WriteString(existingSummary)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCONVERSATION:\n")
	for _, msg := range batch {
		fmt.Fprintf(&sb, "%s: %s\n", msg.Role, msg.Content)
	}
	prompt := sb.String()

	response, err := m.retryLLMCall(ctx, agent, prompt, llmMaxRetries)
	if err == nil && response.Content != "" {
		return strings.TrimSpace(response.Content), nil
	}

	var fallback strings.Builder
	if existingSummary != "" {
		fallback.WriteString(existingSummary)
		fallback.WriteString("\n\n")
	}
	fallback.WriteString("Conversation summary: ")
	for i, msg := range batch {
		if i > 0 {
			fallback.WriteString(" | ")
		}
		content := strings.TrimSpace(msg.Content)
		runes := []rune(content)
		if len(runes) == 0 {
			fallback.WriteString(fmt.Sprintf("%s: ", msg.Role))
			continue
		}

		keepLength := len(runes) * fallbackMaxContentPercent / 100
		if keepLength < fallbackMinContentLength {
			keepLength = fallbackMinContentLength
		}
		if keepLength > len(runes) {
			keepLength = len(runes)
		}

		content = string(runes[:keepLength])
		if keepLength < len(runes) {
			content += "..."
		}
		fallback.WriteString(fmt.Sprintf("%s: %s", msg.Role, content))
	}
	return fallback.String(), nil
}

// focusInstruction renders the user's /compact focus as a prompt sentence
// (empty when no focus was given).
func focusInstruction(focus string) string {
	if focus == "" {
		return ""
	}
	return " Pay particular attention to preserving details about: " + focus + "."
}
