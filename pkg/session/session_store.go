package session

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// SessionReader reads conversation state for a session.
type SessionReader interface {
	// GetHistory returns the message history for the session.
	GetHistory(ctx context.Context, key string) ([]providers.Message, error)
	// GetSummary returns the conversation summary, or "" when none is stored.
	GetSummary(ctx context.Context, key string) (string, error)
}

// SessionWriter mutates conversation state. Implementations must be safe for
// concurrent use. A returned error means the change was not persisted and the
// caller decides whether to surface, retry or drop it.
type SessionWriter interface {
	// AddMessage appends a simple role/content message to the session.
	AddMessage(ctx context.Context, sessionKey, role, content string) error
	// AddFullMessage appends a complete message including tool calls.
	AddFullMessage(ctx context.Context, sessionKey string, msg providers.Message) error
	// SetSummary replaces the conversation summary.
	SetSummary(ctx context.Context, key, summary string) error
	// SetHistory replaces the full message history.
	SetHistory(ctx context.Context, key string, history []providers.Message) error
	// TruncateHistory keeps only the last keepLast messages.
	TruncateHistory(ctx context.Context, key string, keepLast int) error
}

// SessionStore is the persistence surface used by the agent loop: reads,
// writes and lifecycle operations.
//
// DeleteSession and LastActivity are intentionally not part of this
// interface: only the session pruner needs them, and it declares its own
// minimal interfaces (pkg/session/pruner). Both SessionManager and
// JSONLBackend still expose them as concrete methods.
type SessionStore interface {
	SessionReader
	SessionWriter
	// Save flushes pending state and compacts storage for the session.
	Save(ctx context.Context, key string) error
	// ListSessions returns all known session keys.
	ListSessions() []string
	// Close releases resources held by the store.
	Close() error
}
