package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// JSONLBackend adapts a memory.Store into the SessionStore interface.
// Read and write methods take a context and return errors: the caller decides
// whether to surface, retry or drop a failed operation.
type JSONLBackend struct {
	store memory.Store

	// aliasCache memoizes alias -> canonical session key resolutions.
	// Metadata for a session is written once, when the session is
	// created, so a positive resolution stays valid for the process
	// lifetime; entries are dropped when the target is deleted.
	aliasCache sync.Map // alias string -> canonical string
}

type metaAwareStore interface {
	GetSessionMeta(ctx context.Context, sessionKey string) (memory.SessionMeta, error)
	UpsertSessionMeta(ctx context.Context, sessionKey string, scope json.RawMessage, aliases []string) error
	ResolveSessionKey(ctx context.Context, sessionKey string) (string, bool, error)
}

type aliasPromotingStore interface {
	PromoteAliasHistory(ctx context.Context, sessionKey string, scope json.RawMessage, aliases []string) (bool, error)
}

// NewJSONLBackend wraps a memory.Store for use as a SessionStore.
func NewJSONLBackend(store memory.Store) *JSONLBackend {
	return &JSONLBackend{store: store}
}

// resolveSessionKey maps an alias onto its canonical session key. Positive
// resolutions are memoized in aliasCache: without it every read and write
// would call ResolveSessionKey on the metadata store, which scans every
// *.meta.json file in the sessions directory unless the key is a
// short-circuiting canonical key. Misses are deliberately not cached, so a
// session created after a failed lookup still resolves on the next call.
func (b *JSONLBackend) resolveSessionKey(ctx context.Context, sessionKey string) (string, error) {
	metaStore, ok := b.store.(metaAwareStore)
	if !ok {
		return sessionKey, nil
	}
	if cached, ok := b.aliasCache.Load(sessionKey); ok {
		if canonical, ok := cached.(string); ok && canonical != "" {
			return canonical, nil
		}
	}
	resolved, found, err := metaStore.ResolveSessionKey(ctx, sessionKey)
	if err != nil {
		return "", fmt.Errorf("session: resolve session key: %w", err)
	}
	if found && resolved != "" {
		b.aliasCache.Store(sessionKey, resolved)
		return resolved, nil
	}
	return sessionKey, nil
}

// ResolveSessionKey maps aliases onto their canonical session key when the
// underlying store supports structured metadata. Unknown aliases fall back to
// the original input so existing callers remain compatible. Resolution failures
// are logged and fall back to the input key.
func (b *JSONLBackend) ResolveSessionKey(sessionKey string) string {
	resolved, err := b.resolveSessionKey(context.Background(), sessionKey)
	if err != nil {
		log.Printf("session: resolve session key: %v", err)
		return sessionKey
	}
	return resolved
}

// EnsureSessionMetadata persists scope and alias metadata for a session and
// seeds aliasCache with the aliases it just wrote, so later resolutions of
// those aliases do not have to scan the metadata files.
func (b *JSONLBackend) EnsureSessionMetadata(sessionKey string, scope *SessionScope, aliases []string) {
	metaStore, ok := b.store.(metaAwareStore)
	if !ok {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	var rawScope json.RawMessage
	if scope != nil {
		data, err := json.Marshal(scope)
		if err != nil {
			log.Printf("session: encode session scope: %v", err)
			return
		}
		rawScope = data
	}
	ctx := context.Background()
	if err := metaStore.UpsertSessionMeta(ctx, sessionKey, rawScope, aliases); err != nil {
		log.Printf("session: upsert session metadata: %v", err)
		return
	}

	// The metadata written above is the ground truth for these aliases:
	// seed the cache so subsequent resolutions skip the metadata scan.
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == sessionKey {
			continue
		}
		b.aliasCache.Store(alias, sessionKey)
	}

	if promotingStore, ok := b.store.(aliasPromotingStore); ok {
		// The main session is a shared fallback, not this session's own
		// history: exclude its aliases from promotion so stale messages
		// from the fallback are not attached to new per-channel sessions.
		agentID := ""
		if scope != nil {
			agentID = scope.AgentID
		}
		promotableAliases := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			if isMainSessionAlias(agentID, strings.TrimSpace(alias)) {
				continue
			}
			promotableAliases = append(promotableAliases, alias)
		}
		if _, err := promotingStore.PromoteAliasHistory(ctx, sessionKey, rawScope, promotableAliases); err != nil {
			log.Printf("session: promote alias history: %v", err)
		}
	}
}

// GetSessionScope reads structured scope metadata for a session key or alias.
func (b *JSONLBackend) GetSessionScope(sessionKey string) *SessionScope {
	metaStore, ok := b.store.(metaAwareStore)
	if !ok {
		return nil
	}
	sessionKey = b.ResolveSessionKey(sessionKey)
	meta, err := metaStore.GetSessionMeta(context.Background(), sessionKey)
	if err != nil {
		log.Printf("session: get session metadata: %v", err)
		return nil
	}
	if len(meta.Scope) == 0 {
		return nil
	}
	var scope SessionScope
	if err := json.Unmarshal(meta.Scope, &scope); err != nil {
		log.Printf("session: decode session scope: %v", err)
		return nil
	}
	return CloneScope(&scope)
}

// AddMessage appends a simple role/content message to the session.
func (b *JSONLBackend) AddMessage(ctx context.Context, sessionKey, role, content string) error {
	sessionKey, err := b.resolveSessionKey(ctx, sessionKey)
	if err != nil {
		return err
	}
	if err := b.store.AddMessage(ctx, sessionKey, role, content); err != nil {
		return fmt.Errorf("session: add message: %w", err)
	}
	return nil
}

// AddFullMessage appends a complete message including tool calls.
func (b *JSONLBackend) AddFullMessage(ctx context.Context, sessionKey string, msg providers.Message) error {
	sessionKey, err := b.resolveSessionKey(ctx, sessionKey)
	if err != nil {
		return err
	}
	if err := b.store.AddFullMessage(ctx, sessionKey, msg); err != nil {
		return fmt.Errorf("session: add full message: %w", err)
	}
	return nil
}

// GetHistory returns the message history for the session.
func (b *JSONLBackend) GetHistory(ctx context.Context, key string) ([]providers.Message, error) {
	key, err := b.resolveSessionKey(ctx, key)
	if err != nil {
		return nil, err
	}
	msgs, err := b.store.GetHistory(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("session: get history: %w", err)
	}
	return msgs, nil
}

// GetSummary returns the conversation summary, or "" when none is stored.
func (b *JSONLBackend) GetSummary(ctx context.Context, key string) (string, error) {
	key, err := b.resolveSessionKey(ctx, key)
	if err != nil {
		return "", err
	}
	summary, err := b.store.GetSummary(ctx, key)
	if err != nil {
		return "", fmt.Errorf("session: get summary: %w", err)
	}
	return summary, nil
}

// SetSummary replaces the conversation summary.
func (b *JSONLBackend) SetSummary(ctx context.Context, key, summary string) error {
	key, err := b.resolveSessionKey(ctx, key)
	if err != nil {
		return err
	}
	if err := b.store.SetSummary(ctx, key, summary); err != nil {
		return fmt.Errorf("session: set summary: %w", err)
	}
	return nil
}

// SetHistory replaces the full message history.
func (b *JSONLBackend) SetHistory(ctx context.Context, key string, history []providers.Message) error {
	key, err := b.resolveSessionKey(ctx, key)
	if err != nil {
		return err
	}
	if err := b.store.SetHistory(ctx, key, history); err != nil {
		return fmt.Errorf("session: set history: %w", err)
	}
	return nil
}

// TruncateHistory keeps only the last keepLast messages.
func (b *JSONLBackend) TruncateHistory(ctx context.Context, key string, keepLast int) error {
	key, err := b.resolveSessionKey(ctx, key)
	if err != nil {
		return err
	}
	if err := b.store.TruncateHistory(ctx, key, keepLast); err != nil {
		return fmt.Errorf("session: truncate history: %w", err)
	}
	return nil
}

// Save persists session state. Since the JSONL store fsyncs every write
// immediately, the data is already durable. Save runs compaction to reclaim
// space from logically truncated messages (no-op when there are none).
func (b *JSONLBackend) Save(ctx context.Context, key string) error {
	key, err := b.resolveSessionKey(ctx, key)
	if err != nil {
		return err
	}
	if err := b.store.Compact(ctx, key); err != nil {
		return fmt.Errorf("session: compact: %w", err)
	}
	return nil
}

// Close releases resources held by the underlying store.
func (b *JSONLBackend) Close() error {
	return b.store.Close()
}

// ListSessions returns all known session keys.
func (b *JSONLBackend) ListSessions() []string {
	return b.store.ListSessions()
}

// sessionDeletingStore is implemented by memory stores that can physically
// remove a session. It is optional so alternative backends remain compatible.
type sessionDeletingStore interface {
	DeleteSession(ctx context.Context, sessionKey string) error
}

// sessionActivityStore is implemented by memory stores that can report when a
// session was last written.
type sessionActivityStore interface {
	LastActivity(sessionKey string) time.Time
}

// LastActivity returns when the session was last written, or the zero time when
// the backing store cannot tell. Used by automatic session pruning, which only
// passes canonical keys, so no alias resolution is done here.
func (b *JSONLBackend) LastActivity(key string) time.Time {
	if store, ok := b.store.(sessionActivityStore); ok {
		return store.LastActivity(key)
	}
	return time.Time{}
}

// DeleteSession removes a session and its metadata from the backing store.
// Used by automatic session pruning, which only passes canonical keys (from
// ListSessions or the seahorse index). The key is deliberately not resolved
// through aliases: resolving costs a scan of every metadata file per call,
// making a prune pass quadratic in the number of sessions, and an alias must
// never cause the session it points to to be deleted. After a successful
// delete the alias cache is swept for entries pointing at the removed
// session, so a stale alias cannot resurrect it.
func (b *JSONLBackend) DeleteSession(key string) error {
	deleter, ok := b.store.(sessionDeletingStore)
	if !ok {
		return fmt.Errorf("session: backing store does not support DeleteSession")
	}
	if err := deleter.DeleteSession(context.Background(), key); err != nil {
		return err
	}
	b.aliasCache.Range(func(alias, canonical any) bool {
		if resolved, ok := canonical.(string); ok && resolved == key {
			b.aliasCache.Delete(alias)
		}
		return true
	})
	return nil
}
