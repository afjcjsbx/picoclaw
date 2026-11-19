package tools

import (
	"context"
	"sync"
	"time"
)

// maxExecStateEntries bounds how many conversations keep exec state. The state
// is best-effort, memory-only, and evicted least-recently-used so a long-running
// gateway cannot grow without bound.
const maxExecStateEntries = 64

// execStateKey scopes state to one agent conversation. Requiring both values
// prevents state from leaking between conversations, agents, or direct tool
// calls that have no session context.
type execStateKey struct {
	agentID    string
	sessionKey string
}

// execStateEntry is everything exec remembers about one conversation.
type execStateEntry struct {
	// cwd is the last working directory that passed validation.
	cwd string
	// block tracks the model retrying a rejected command.
	block    blockStreak
	lastUsed time.Time
}

type execStateStore struct {
	mu      sync.Mutex
	entries map[execStateKey]*execStateEntry
}

func newExecStateStore() *execStateStore {
	return &execStateStore{entries: make(map[execStateKey]*execStateEntry)}
}

func execStateKeyFromContext(ctx context.Context) (execStateKey, bool) {
	key := execStateKey{
		agentID:    ToolAgentID(ctx),
		sessionKey: ToolSessionKey(ctx),
	}
	if key.agentID == "" || key.sessionKey == "" {
		return execStateKey{}, false
	}
	return key, true
}

// update runs fn on the conversation's entry and marks it as recently used.
// create makes a missing entry; without it a missing entry is left alone. A
// missing session context yields no state at all, keeping exec stateless
// instead of sharing a global entry. It reports whether fn ran.
func (s *execStateStore) update(ctx context.Context, create bool, fn func(*execStateEntry)) bool {
	if s == nil {
		return false
	}
	key, ok := execStateKeyFromContext(ctx)
	if !ok {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		if !create {
			return false
		}
		s.pruneLocked()
		entry = &execStateEntry{}
		s.entries[key] = entry
	}
	fn(entry)
	entry.lastUsed = time.Now()
	return true
}

// load returns the conversation's remembered cwd.
func (s *execStateStore) load(ctx context.Context) string {
	var cwd string
	s.update(ctx, false, func(entry *execStateEntry) { cwd = entry.cwd })
	return cwd
}

// rememberCwd stores the resolved cwd for subsequent calls in the conversation.
func (s *execStateStore) rememberCwd(ctx context.Context, cwd string) {
	s.update(ctx, true, func(entry *execStateEntry) { entry.cwd = cwd })
}

// clearCwd forgets a stored cwd that is no longer usable. It does not create
// an entry when the conversation has none.
func (s *execStateStore) clearCwd(ctx context.Context) {
	s.update(ctx, false, func(entry *execStateEntry) { entry.cwd = "" })
}

func (s *execStateStore) clear(key execStateKey) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// pruneLocked removes the least-recently-used entry until there is room for a
// new one. It is only called while holding s.mu and only when a new key is
// inserted.
func (s *execStateStore) pruneLocked() {
	for len(s.entries) >= maxExecStateEntries {
		var (
			oldestKey  execStateKey
			oldestTime time.Time
			found      bool
		)
		for key, entry := range s.entries {
			if !found || entry.lastUsed.Before(oldestTime) {
				oldestKey = key
				oldestTime = entry.lastUsed
				found = true
			}
		}
		if !found {
			return
		}
		delete(s.entries, oldestKey)
	}
}

// ClearSession drops remembered cwd and guard-block state for one agent
// conversation. It is called when the user clears or resets the conversation.
func (t *ExecTool) ClearSession(agentID, sessionKey string) {
	if t == nil {
		return
	}
	t.state.clear(execStateKey{agentID: agentID, sessionKey: sessionKey})
}
