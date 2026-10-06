package subagent

import (
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/messageutil"
)

// MaxEphemeralHistorySize limits the number of messages stored in ephemeral sessions.
// This prevents memory accumulation in long-running sub-turns.
const MaxEphemeralHistorySize = 50

// EphemeralSessionStore is an in-memory session.SessionStore used by SubTurns.
// It does not persist to disk and auto-truncates history to MaxEphemeralHistorySize.
type EphemeralSessionStore struct {
	mu      sync.Mutex
	history []providers.Message
	summary string
}

// NewEphemeralSession returns a store preloaded with initial.
func NewEphemeralSession(initial []providers.Message) *EphemeralSessionStore {
	s := &EphemeralSessionStore{}
	if len(initial) > 0 {
		s.history = append(s.history, initial...)
	}
	return s
}

func (e *EphemeralSessionStore) AddMessage(_, role, content string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, providers.Message{Role: role, Content: content})
	e.truncateLocked()
}

func (e *EphemeralSessionStore) AddFullMessage(_ string, msg providers.Message) {
	if messageutil.IsTransientAssistantThoughtMessage(msg) {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, msg)
	e.truncateLocked()
}

func (e *EphemeralSessionStore) GetHistory(_ string) []providers.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]providers.Message, len(e.history))
	copy(out, e.history)
	return out
}

func (e *EphemeralSessionStore) GetSummary(_ string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.summary
}

func (e *EphemeralSessionStore) SetSummary(_, summary string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.summary = summary
}

func (e *EphemeralSessionStore) SetHistory(_ string, history []providers.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	history = messageutil.FilterInvalidHistoryMessages(history)
	e.history = make([]providers.Message, len(history))
	copy(e.history, history)
	e.truncateLocked()
}

func (e *EphemeralSessionStore) TruncateHistory(_ string, keepLast int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if keepLast <= 0 {
		e.history = nil
		return
	}

	if keepLast >= len(e.history) {
		return
	}
	e.history = e.history[len(e.history)-keepLast:]
}

func (e *EphemeralSessionStore) Save(_ string) error    { return nil }
func (e *EphemeralSessionStore) Close() error           { return nil }
func (e *EphemeralSessionStore) ListSessions() []string { return nil }

func (e *EphemeralSessionStore) truncateLocked() {
	if len(e.history) > MaxEphemeralHistorySize {
		e.history = e.history[len(e.history)-MaxEphemeralHistorySize:]
	}
}
