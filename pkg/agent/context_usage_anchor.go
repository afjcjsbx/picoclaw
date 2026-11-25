// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	// maxUsageAnchors bounds how many sessions keep a usage anchor. The anchor
	// is best-effort, memory-only state; evicting the least-recently-used entry
	// costs at most a heuristic re-estimate of that session's next prompt.
	maxUsageAnchors = 64
	// min/maxUsageAnchorRatio keep a broken provider report (for example only
	// the uncached part of the prompt) from distorting every budget decision.
	minUsageAnchorRatio = 0.25
	maxUsageAnchorRatio = 8
)

// usageAnchorKey scopes an anchor to one agent conversation, like exec state:
// session keys alone can be reused across agents.
type usageAnchorKey struct {
	agentID    string
	sessionKey string
}

// usageAnchor is the provider-reported prompt size of the session's most recent
// LLM call. With it, the next prompt is measured as this anchor plus only the
// messages appended since, instead of re-estimating the whole history with the
// chars*2/5 heuristic. That heuristic is reasonable for prose but undershoots
// code, JSON and tool output, which is exactly what compaction has to catch.
//
// The anchor is memory-only on purpose: after a restart the first call per
// session falls back to the heuristic, then the anchor is rebuilt from real
// usage. It stores no message content, only lengths and a fingerprint, so a
// long session costs the same few dozen bytes as a short one.
type usageAnchor struct {
	// promptTokens is providers.UsageInfo.PromptTokens of the reporting call.
	// It already includes system prompt, tool definitions, and cached tokens.
	promptTokens int
	// promptMessages is len(messages) of the prompt that usage describes.
	promptMessages int
	// prefixHash fingerprints messages[promptPrefixStart:promptMessages]. The
	// system message is excluded because dynamic context (time, summary) changes
	// between turns; its token delta is accounted separately.
	prefixHash uint64
	// overheadTokens is the heuristic size of the system message and tool
	// definitions included in promptTokens. It lets the estimate follow a
	// changed system prompt or tool set without recomputing history.
	overheadTokens int
	// historyTokens is the heuristic size of the non-system prompt messages.
	// promptTokens-overheadTokens over historyTokens is the session's observed
	// real/heuristic ratio, used to calibrate stored-context estimates.
	historyTokens int
	// ratio is the observed real/heuristic ratio of the reporting prompt. It
	// survives baseline invalidation (compaction) so calibration keeps working
	// on the next assembly; full /clear drops the whole entry.
	ratio float64
	// model is the model that reported the usage. A different model has a
	// different tokenizer, so the anchor must not be reused.
	model    string
	lastUsed time.Time
}

// calibrationRatio reports how many real tokens one heuristic token represents
// for this session. 1 means "no reliable observation": the caller keeps the
// plain heuristic.
func (a usageAnchor) calibrationRatio() float64 {
	if a.ratio > 0 {
		return a.ratio
	}
	if a.historyTokens <= 0 || a.promptTokens <= a.overheadTokens {
		return 1
	}
	ratio := float64(a.promptTokens-a.overheadTokens) / float64(a.historyTokens)
	if ratio < minUsageAnchorRatio || ratio > maxUsageAnchorRatio {
		return 1
	}
	return ratio
}

// usageAnchorStore keeps one anchor per session with LRU eviction.
type usageAnchorStore struct {
	mu      sync.Mutex
	entries map[usageAnchorKey]usageAnchor
}

func newUsageAnchorStore() *usageAnchorStore {
	return &usageAnchorStore{entries: make(map[usageAnchorKey]usageAnchor, 4)}
}

// record stores the anchor of a completed call. A new session evicts the least
// recently used anchor when the store is full.
func (s *usageAnchorStore) record(key usageAnchorKey, anchor usageAnchor) {
	if s == nil || key.sessionKey == "" {
		return
	}
	anchor.lastUsed = time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[usageAnchorKey]usageAnchor, 4)
	}
	if _, exists := s.entries[key]; !exists {
		s.pruneLocked()
	}
	s.entries[key] = anchor
}

// load returns the session's anchor and marks it as recently used.
func (s *usageAnchorStore) load(key usageAnchorKey) (usageAnchor, bool) {
	if s == nil {
		return usageAnchor{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	anchor, ok := s.entries[key]
	if !ok {
		return usageAnchor{}, false
	}
	anchor.lastUsed = time.Now()
	s.entries[key] = anchor
	return anchor, true
}

// loadBySession returns the most recently used anchor for a session key
// regardless of the owning agent. It is used for calibration, which only needs
// the observed ratio and has no agent ID at hand; the exact-key lookup used by
// compaction still includes the agent.
func (s *usageAnchorStore) loadBySession(sessionKey string) (usageAnchor, bool) {
	if s == nil || sessionKey == "" {
		return usageAnchor{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var (
		best   usageAnchor
		newest time.Time
	)
	found := false
	for key, anchor := range s.entries {
		if key.sessionKey != sessionKey {
			continue
		}
		if !found || anchor.lastUsed.After(newest) {
			best, newest, found = anchor, anchor.lastUsed, true
		}
	}
	return best, found
}

// invalidateBaseline marks the anchor's prompt description as stale while
// keeping the observed calibration ratio. Compaction changes the stored
// context, so the reported prompt no longer describes the next prompt, but the
// content type (and therefore its real/heuristic ratio) is still informative.
func (s *usageAnchorStore) invalidateBaseline(key usageAnchorKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	anchor, ok := s.entries[key]
	if !ok {
		return
	}
	anchor.promptTokens = 0
	anchor.promptMessages = 0
	anchor.prefixHash = 0
	anchor.overheadTokens = 0
	anchor.historyTokens = 0
	anchor.lastUsed = time.Now()
	s.entries[key] = anchor
}

func (s *usageAnchorStore) clear(key usageAnchorKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// pruneLocked removes the least-recently-used anchor until there is room for a
// new one. It is only called while holding s.mu and only when a new key is
// inserted.
func (s *usageAnchorStore) pruneLocked() {
	for len(s.entries) >= maxUsageAnchors {
		var (
			oldestKey  usageAnchorKey
			oldestTime time.Time
			found      bool
		)
		for key, anchor := range s.entries {
			if !found || anchor.lastUsed.Before(oldestTime) {
				oldestKey, oldestTime, found = key, anchor.lastUsed, true
			}
		}
		if !found {
			return
		}
		delete(s.entries, oldestKey)
	}
}

// usageAnchorRatio returns the real/heuristic token ratio observed for a
// session, or 1 when no usable anchor exists.
func (al *AgentLoop) usageAnchorRatio(agentID, sessionKey string) float64 {
	if al == nil {
		return 1
	}
	anchor, ok := al.usageAnchors.load(usageAnchorKey{agentID: agentID, sessionKey: sessionKey})
	if !ok {
		return 1
	}
	return anchor.calibrationRatio()
}

// usageAnchorRatioForSession is usageAnchorRatio without an agent ID. It is
// used by callees that only know the session key and must not pay for an agent
// lookup (which can read session metadata) on every LLM call.
func (al *AgentLoop) usageAnchorRatioForSession(sessionKey string) float64 {
	if al == nil {
		return 1
	}
	anchor, ok := al.usageAnchors.loadBySession(sessionKey)
	if !ok {
		return 1
	}
	return anchor.calibrationRatio()
}

// clearUsageAnchor forgets the anchor of one conversation. It is called when
// the stored context is discarded (/clear), because even the observed
// calibration no longer describes the session.
func (al *AgentLoop) clearUsageAnchor(agentID, sessionKey string) {
	if al == nil {
		return
	}
	al.usageAnchors.clear(usageAnchorKey{agentID: agentID, sessionKey: sessionKey})
}

// invalidateUsageAnchor keeps the session's calibration ratio but drops the
// reported prompt baseline. Compaction creates this state: the old prompt size
// is stale, while the content type's real/heuristic ratio is still useful.
func (al *AgentLoop) invalidateUsageAnchor(agentID, sessionKey string) {
	if al == nil {
		return
	}
	al.usageAnchors.invalidateBaseline(
		usageAnchorKey{agentID: agentID, sessionKey: sessionKey},
	)
}

// recordUsageAnchor stores the prompt size the provider just reported for the
// call in exec. It is the write half of the usage anchor; CompactBeforeLLM is
// the read half.
func (p *Pipeline) recordUsageAnchor(ts *turnState, exec *turnExecution) {
	if p == nil || p.al == nil || ts == nil || ts.agent == nil || exec == nil || exec.response == nil {
		return
	}
	if ts.opts.NoHistory {
		return
	}
	usage := exec.response.Usage
	if usage == nil || usage.PromptTokens <= 0 {
		return
	}
	messages := exec.callMessages
	if len(messages) == 0 {
		return
	}
	provider := exec.activeProvider
	if provider == nil {
		provider = ts.agent.Provider
	}
	key := usageAnchorKey{agentID: ts.agent.ID, sessionKey: ts.sessionKey}
	anchor := usageAnchor{
		promptTokens:   usage.PromptTokens,
		promptMessages: len(messages),
		prefixHash:     promptPrefixHash(messages, len(messages)),
		overheadTokens: promptOverheadTokens(provider, messages, exec.providerToolDefs),
		model:          exec.llmModelName,
	}
	// Reuse the previous anchor's heuristic history size when this prompt still
	// starts with it. The common case (another call in the same turn, or the
	// next turn without compaction) then scans only the appended messages
	// instead of the whole history.
	anchor.historyTokens = p.anchoredHistoryTokens(key, provider, messages, exec.llmModelName)
	anchor.ratio = anchor.calibrationRatio()
	p.al.usageAnchors.record(key, anchor)
}

// anchoredHistoryTokens returns the heuristic size of the non-system messages
// covered by the previous anchor plus the messages appended since. It falls
// back to a full scan when the previous anchor is missing or the prompt is no
// longer an extension of it.
func (p *Pipeline) anchoredHistoryTokens(
	key usageAnchorKey,
	provider providers.LLMProvider,
	messages []providers.Message,
	model string,
) int {
	previous, ok := p.al.usageAnchors.load(key)
	if !ok || previous.model != model || previous.promptMessages <= 0 ||
		len(messages) < previous.promptMessages {
		return promptHistoryTokens(provider, messages)
	}
	if promptPrefixHash(messages, previous.promptMessages) != previous.prefixHash {
		return promptHistoryTokens(provider, messages)
	}
	total := previous.historyTokens
	for _, msg := range messages[previous.promptMessages:] {
		total += estimateMessageTokensForProvider(provider, msg)
	}
	return total
}

// anchoredPromptTokens estimates the prompt about to be sent from the
// session's last provider-reported usage plus the messages appended since. It
// reports ok=false whenever the anchor cannot be trusted (no provider usage
// yet, different model, or the prompt no longer starts with the anchored
// messages), so the caller falls back to the full heuristic.
func (p *Pipeline) anchoredPromptTokens(
	ts *turnState,
	exec *turnExecution,
	toolTokens int,
) (int, bool) {
	if p == nil || p.al == nil || ts == nil || ts.agent == nil || exec == nil {
		return 0, false
	}
	anchor, ok := p.al.usageAnchors.load(
		usageAnchorKey{agentID: ts.agent.ID, sessionKey: ts.sessionKey},
	)
	if !ok || anchor.promptTokens <= 0 || anchor.promptMessages <= 0 {
		return 0, false
	}
	// A report at or below the estimated non-history overhead cannot describe
	// the whole prompt (some providers report only the uncached part). Trusting
	// it would delay compaction, so fall back to the heuristic instead.
	if anchor.promptTokens <= anchor.overheadTokens {
		return 0, false
	}
	if anchor.model != exec.llmModelName {
		return 0, false
	}
	messages := exec.messages
	if len(messages) < anchor.promptMessages {
		return 0, false
	}
	// Compaction, eviction, pruning or a rebuilt prompt all change the prefix.
	// The hash only reads lengths, so this stays cheap even for large outputs.
	if promptPrefixHash(messages, anchor.promptMessages) != anchor.prefixHash {
		return 0, false
	}

	provider := exec.activeProvider
	if provider == nil {
		provider = ts.agent.Provider
	}
	deltaTokens := 0
	for _, msg := range messages[anchor.promptMessages:] {
		deltaTokens += estimateMessageTokensForProvider(provider, msg)
	}
	// New tool output and assistant text usually resemble the anchored
	// history. Carrying the session's observed real/heuristic ratio over the
	// delta keeps a large JSON or code result from being counted at prose
	// density, which is the undercount this anchor exists to correct.
	deltaTokens = calibrateHeuristicTokens(deltaTokens, anchor.calibrationRatio())
	systemTokens := 0
	if promptPrefixStart(messages) == 1 {
		systemTokens = estimateMessageTokensForProvider(provider, messages[0])
	}

	// Replace the anchored system/tool envelope with the current one: only the
	// history part can be extended by a message delta.
	tokens := anchor.promptTokens - anchor.overheadTokens + toolTokens + systemTokens + deltaTokens
	if tokens < 1 {
		return 0, false
	}
	return tokens, true
}

// invalidateUsageAnchor drops the turn session's prompt baseline after a
// context rebuild changed the stored history. The calibration ratio is kept
// (see invalidateBaseline); only the prompt description becomes stale.
func (p *Pipeline) invalidateUsageAnchor(ts *turnState) {
	if p == nil || p.al == nil || ts == nil || ts.agent == nil {
		return
	}
	p.al.invalidateUsageAnchor(ts.agent.ID, ts.sessionKey)
}

// promptPrefixStart is the first message the prefix fingerprint covers. The
// leading system message is excluded: its dynamic sections change every turn,
// and promptOverheadTokens accounts for its size separately.
func promptPrefixStart(messages []providers.Message) int {
	if len(messages) > 0 && messages[0].Role == "system" {
		return 1
	}
	return 0
}

// promptOverheadTokens estimates the non-history part of a prompt: tool
// definitions plus the leading system message.
func promptOverheadTokens(
	provider providers.LLMProvider,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
) int {
	tokens := EstimateToolDefsTokens(toolDefs)
	if promptPrefixStart(messages) == 1 {
		tokens += estimateMessageTokensForProvider(provider, messages[0])
	}
	return tokens
}

// promptHistoryTokens estimates the non-system messages of a prompt.
func promptHistoryTokens(provider providers.LLMProvider, messages []providers.Message) int {
	total := 0
	for _, msg := range messages[promptPrefixStart(messages):] {
		total += estimateMessageTokensForProvider(provider, msg)
	}
	return total
}

// promptPrefixHash fingerprints the shape of messages[:end] from
// promptPrefixStart on. It never scans message content, only roles, sizes, and
// tool-call structure, so validating a large anchored prefix costs a few reads
// per message. The system message is skipped by promptPrefixStart. A mismatch
// only disables the anchor for one call; it never changes context.
func promptPrefixHash(messages []providers.Message, end int) uint64 {
	if end > len(messages) {
		end = len(messages)
	}
	h := uint64(14695981039346656037) // FNV-1a offset basis
	for i := promptPrefixStart(messages); i < end; i++ {
		msg := &messages[i]
		h = fnvMix(h, promptRoleCode(msg.Role))
		h = fnvMix(h, uint64(len(msg.Content)))
		h = fnvMix(h, uint64(len(msg.ReasoningContent)))
		h = fnvMix(h, uint64(len(msg.ToolCallID)))
		h = fnvMix(h, uint64(len(msg.Media)))
		h = fnvMix(h, uint64(len(msg.ToolCalls)))
		for _, call := range msg.ToolCalls {
			h = fnvMix(h, uint64(len(call.ID)))
			h = fnvMix(h, uint64(len(call.Name)))
			if call.Function != nil {
				h = fnvMix(h, uint64(len(call.Function.Name)))
				h = fnvMix(h, uint64(len(call.Function.Arguments)))
			}
		}
		h = fnvMix(h, uint64(len(msg.SystemParts)))
		for _, part := range msg.SystemParts {
			h = fnvMix(h, uint64(len(part.Text)))
		}
	}
	return h
}

func fnvMix(h, value uint64) uint64 {
	const fnvPrime = 1099511628211
	h ^= value
	h *= fnvPrime
	return h
}

func promptRoleCode(role string) uint64 {
	switch role {
	case "system":
		return 1
	case "user":
		return 2
	case "assistant":
		return 3
	case "tool":
		return 4
	default:
		return 5
	}
}

// scaleHeuristicBudget converts a real-token budget into the heuristic token
// units seahorse stores: real = heuristic * ratio, so heuristic = real / ratio.
func scaleHeuristicBudget(budget int, ratio float64) int {
	if budget <= 0 || ratio <= 0 || ratio == 1 {
		return budget
	}
	scaled := int(float64(budget) / ratio)
	if scaled < 1 {
		return 1
	}
	return scaled
}

// calibrateHeuristicTokens converts a heuristic token count into real tokens.
func calibrateHeuristicTokens(tokens int, ratio float64) int {
	if tokens <= 0 || ratio <= 0 || ratio == 1 {
		return tokens
	}
	calibrated := int(float64(tokens) * ratio)
	if calibrated < 1 {
		return 1
	}
	return calibrated
}
