package tools

import (
	"context"
	"sync"
	"time"
)

// maxExecStateEntries bounds how many conversations keep explicit exec
// cwd/env state. The state is best-effort, memory-only, and evicted
// least-recently-used so a long-running gateway cannot grow without bound.
const maxExecStateEntries = 64

// execStateKey scopes state to one agent conversation. Requiring both values
// prevents state from leaking between conversations, agents, or direct tool
// calls that have no session context.
type execStateKey struct {
	agentID    string
	sessionKey string
}

type execStateEntry struct {
	cwd      string
	env      map[string]string
	lastUsed time.Time
}

// execSessionState is a copy of the persisted state for one conversation.
type execSessionState struct {
	cwd string
	env map[string]string
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

func cloneExecStateEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(env))
	for key, value := range env {
		cloned[key] = value
	}
	return cloned
}

// load returns a snapshot of the conversation state and marks it as recently
// used. A missing session context yields zero state, keeping exec stateless
// instead of sharing a global entry.
func (s *execStateStore) load(ctx context.Context) execSessionState {
	if s == nil {
		return execSessionState{}
	}
	key, ok := execStateKeyFromContext(ctx)
	if !ok {
		return execSessionState{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return execSessionState{}
	}
	entry.lastUsed = time.Now()
	return execSessionState{
		cwd: entry.cwd,
		env: cloneExecStateEnv(entry.env),
	}
}

// rememberCwd stores the resolved cwd for subsequent calls in the conversation.
func (s *execStateStore) rememberCwd(ctx context.Context, cwd string) {
	s.mutate(ctx, func(entry *execStateEntry) {
		entry.cwd = cwd
	})
}

// clearCwd forgets a stored cwd that is no longer usable. It does not create
// an entry when the conversation has none.
func (s *execStateStore) clearCwd(ctx context.Context) {
	s.mutateExisting(ctx, func(entry *execStateEntry) {
		entry.cwd = ""
	})
}

// storeEnv replaces the conversation's remembered env overlay. A nil map
// clears it.
func (s *execStateStore) storeEnv(ctx context.Context, env map[string]string) {
	s.mutate(ctx, func(entry *execStateEntry) {
		entry.env = cloneExecStateEnv(env)
	})
}

func (s *execStateStore) mutate(ctx context.Context, update func(*execStateEntry)) {
	if s == nil {
		return
	}
	key, ok := execStateKeyFromContext(ctx)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		s.pruneLocked()
		entry = &execStateEntry{}
		s.entries[key] = entry
	}
	update(entry)
	entry.lastUsed = time.Now()
}

func (s *execStateStore) mutateExisting(ctx context.Context, update func(*execStateEntry)) {
	if s == nil {
		return
	}
	key, ok := execStateKeyFromContext(ctx)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok {
		return
	}
	update(entry)
	entry.lastUsed = time.Now()
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

// ClearSession drops remembered cwd/env state for one agent conversation. It
// is called when the user clears or resets the conversation.
func (t *ExecTool) ClearSession(agentID, sessionKey string) {
	if t == nil || t.state == nil {
		return
	}
	t.state.clear(execStateKey{agentID: agentID, sessionKey: sessionKey})
}

// mergeExecStateEnv applies a per-call env map on top of the remembered
// overlay. A missing env argument (nil) reuses the overlay; an explicit empty
// map clears it; any other map is merged so repeated exports accumulate.
// Blocked LLM-provided keys are dropped before they can be stored.
func mergeExecStateEnv(base, update map[string]string) (effective, next map[string]string, provided bool) {
	if update == nil {
		return cloneExecStateEnv(base), nil, false
	}
	if len(update) == 0 {
		return nil, nil, true
	}

	merged := cloneExecStateEnv(base)
	if merged == nil {
		merged = make(map[string]string, len(update))
	}
	for key, value := range update {
		key = execEnvKey(key)
		if isBlockedExecEnv(key) {
			continue
		}
		merged[key] = value
	}
	if len(merged) == 0 {
		merged = nil
	}
	return cloneExecStateEnv(merged), merged, true
}
