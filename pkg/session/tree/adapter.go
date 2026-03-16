package tree

import (
	"path/filepath"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// Adapter wraps a collection of tree SessionManagers to implement the
// session.SessionStore interface. This enables drop-in replacement of
// the legacy flat SessionManager/JSONLBackend without changing the agent loop.
//
// Each session key maps to its own tree SessionManager (one JSONL file per key).
type Adapter struct {
	mu         sync.RWMutex
	managers   map[string]*SessionManager
	sessionDir string
}

// NewAdapter creates a new tree-based SessionStore adapter.
func NewAdapter(sessionDir string) *Adapter {
	return &Adapter{
		managers:   make(map[string]*SessionManager),
		sessionDir: sessionDir,
	}
}

// keyDir returns a per-session-key subdirectory inside the session dir.
func (a *Adapter) keyDir(key string) string {
	safe := sanitizeKey(key)
	return filepath.Join(a.sessionDir, safe)
}

// getOrCreate returns the SessionManager for a key, creating one if needed.
func (a *Adapter) getOrCreate(key string) *SessionManager {
	a.mu.RLock()
	sm, ok := a.managers[key]
	a.mu.RUnlock()
	if ok {
		return sm
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check
	if sm, ok = a.managers[key]; ok {
		return sm
	}

	// Each key gets its own subdirectory for session file isolation
	dir := a.keyDir(key)
	sm = ContinueRecent(key, dir)
	a.managers[key] = sm

	logger.DebugCF("session", "Tree session initialized", map[string]any{
		"key":        key,
		"session_id": sm.GetSessionID()[:8],
		"entries":    len(sm.GetEntries()),
		"persisted":  sm.IsPersisted(),
	})

	return sm
}

// Manager returns the underlying tree SessionManager for a session key.
// This exposes the full tree API for callers that need branching, tree traversal, etc.
func (a *Adapter) Manager(key string) *SessionManager {
	return a.getOrCreate(key)
}

// --- SessionStore implementation ---

func (a *Adapter) AddMessage(sessionKey, role, content string) {
	sm := a.getOrCreate(sessionKey)
	sm.AppendMessage(AgentMessage{
		Role:    role,
		Content: content,
	})
}

func (a *Adapter) AddFullMessage(sessionKey string, msg providers.Message) {
	sm := a.getOrCreate(sessionKey)
	sm.AppendProviderMessage(msg)
}

func (a *Adapter) GetHistory(key string) []providers.Message {
	sm := a.getOrCreate(key)
	ctx := sm.BuildSessionContext()

	msgs := make([]providers.Message, 0, len(ctx.Messages))
	dropped := 0
	for _, am := range ctx.Messages {
		// Only include messages that the LLM understands
		switch am.Role {
		case "user", "assistant", "tool", "system":
			msgs = append(msgs, agentMsgToProviderMsg(am))
		case "compactionSummary":
			msgs = append(msgs, providers.Message{
				Role:    "user",
				Content: "CONTEXT_SUMMARY: " + am.Summary,
			})
		case "branchSummary":
			msgs = append(msgs, providers.Message{
				Role:    "user",
				Content: "BRANCH_CONTEXT: " + am.Summary,
			})
		case "custom":
			msgs = append(msgs, providers.Message{
				Role:    "user",
				Content: am.Content,
			})
		default:
			dropped++
		}
	}

	logger.DebugCF("session", "GetHistory from tree", map[string]any{
		"key":             key,
		"tree_messages":   len(ctx.Messages),
		"output_messages": len(msgs),
		"dropped":         dropped,
		"has_compaction":  ctx.ThinkingLevel != "",
	})

	return msgs
}

func (a *Adapter) GetSummary(key string) string {
	sm := a.getOrCreate(key)
	// In tree mode, summaries are stored as compaction entries.
	// Walk from leaf to find the latest compaction.
	entries := sm.GetEntries()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "compaction" {
			return entries[i].Summary
		}
	}
	return ""
}

func (a *Adapter) SetSummary(key, summary string) {
	sm := a.getOrCreate(key)
	// Store as a compaction entry with the current leaf as firstKeptEntryId
	leafID := sm.GetLeafID()
	firstKept := ""
	if leafID != nil {
		firstKept = *leafID
	}
	sm.AppendCompaction(summary, firstKept, 0, nil, false)
}

func (a *Adapter) SetHistory(key string, history []providers.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Replace the entire session with new history
	sm := Create(key, a.keyDir(key))
	for _, msg := range history {
		sm.AppendProviderMessage(msg)
	}
	a.managers[key] = sm
}

func (a *Adapter) TruncateHistory(key string, keepLast int) {
	sm := a.getOrCreate(key)
	entries := sm.GetEntries()

	// Count message entries
	var msgEntries []*SessionEntry
	for _, e := range entries {
		if e.Type == "message" {
			msgEntries = append(msgEntries, e)
		}
	}

	if keepLast <= 0 || len(msgEntries) <= keepLast {
		return
	}

	// The first entry to keep
	firstKeptIdx := len(msgEntries) - keepLast
	firstKeptEntry := msgEntries[firstKeptIdx]

	// Build a summary of truncated messages
	var summaryParts []string
	for i := 0; i < firstKeptIdx; i++ {
		e := msgEntries[i]
		if e.Message != nil {
			summaryParts = append(summaryParts, e.Message.Role+": "+truncateString(e.Message.Content, 200))
		}
	}

	summary := ""
	if len(summaryParts) > 0 {
		summary = "Truncated conversation context (oldest messages removed)"
	}

	sm.AppendCompaction(summary, firstKeptEntry.ID, 0, nil, false)
}

func (a *Adapter) Save(key string) error {
	// Tree sessions auto-persist on each append. No-op.
	return nil
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.managers = make(map[string]*SessionManager)
	return nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
