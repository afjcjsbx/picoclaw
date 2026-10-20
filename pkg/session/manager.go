package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/messageutil"
	"github.com/sipeed/picoclaw/pkg/session/schema"
)

// sessionRecord is the persisted session snapshot; the layout lives in the
// shared schema package so the migration path reads the same struct.
type sessionRecord = schema.Record

type SessionManager struct {
	sessions map[string]*sessionRecord
	mu       sync.RWMutex
	storage  string
}

func NewSessionManager(storage string) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*sessionRecord),
		storage:  storage,
	}

	if storage != "" {
		os.MkdirAll(storage, 0o700)
		sm.loadSessions()
	}

	return sm
}

func newSession(key string) *sessionRecord {
	now := time.Now()
	return &sessionRecord{
		Key:      key,
		Messages: []providers.Message{},
		Created:  now,
		Updated:  now,
	}
}

func ensureMessageCreatedAt(msg *providers.Message, fallback time.Time) {
	if msg.CreatedAt != nil && !msg.CreatedAt.IsZero() {
		return
	}
	ts := fallback
	msg.CreatedAt = &ts
}

func normalizeHistoryCreatedAt(history []providers.Message) {
	now := time.Now()
	for i := range history {
		ensureMessageCreatedAt(&history[i], now)
	}
}

func (sm *SessionManager) AddMessage(ctx context.Context, sessionKey, role, content string) error {
	return sm.AddFullMessage(ctx, sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
func (sm *SessionManager) AddFullMessage(_ context.Context, sessionKey string, msg providers.Message) error {
	if messageutil.IsTransientAssistantThoughtMessage(msg) {
		return nil
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionKey]
	if !ok {
		session = newSession(sessionKey)
		sm.sessions[sessionKey] = session
	}

	now := time.Now()
	ensureMessageCreatedAt(&msg, now)

	session.Messages = append(session.Messages, msg)
	session.Updated = now
	return nil
}

func (sm *SessionManager) GetHistory(_ context.Context, key string) ([]providers.Message, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return []providers.Message{}, nil
	}

	return messageutil.CloneHistory(session.Messages), nil
}

func (sm *SessionManager) GetSummary(_ context.Context, key string) (string, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return "", nil
	}
	return session.Summary, nil
}

func (sm *SessionManager) SetSummary(_ context.Context, key string, summary string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		session.Summary = summary
		session.Updated = time.Now()
	}
	return nil
}

func (sm *SessionManager) TruncateHistory(_ context.Context, key string, keepLast int) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	if keepLast <= 0 {
		session.Messages = []providers.Message{}
		session.Updated = time.Now()
		return nil
	}

	if len(session.Messages) <= keepLast {
		return nil
	}

	// Copy the retained tail into a fresh slice so the old backing array
	// (and the messages dropped by the truncation) can be garbage collected.
	session.Messages = slices.Clone(session.Messages[len(session.Messages)-keepLast:])
	session.Updated = time.Now()
	return nil
}

func (sm *SessionManager) ListSessions() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	keys := make([]string, 0, len(sm.sessions))
	for k := range sm.sessions {
		keys = append(keys, k)
	}
	return keys
}

// LastActivity returns when the session was last updated, or the zero time for
// an unknown session. Used by automatic session pruning.
func (sm *SessionManager) LastActivity(key string) time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if stored, ok := sm.sessions[key]; ok {
		return stored.Updated
	}
	return time.Time{}
}

// sessionPath returns the on-disk path of a session file. It validates the
// sanitized filename so callers cannot escape the storage directory.
func (sm *SessionManager) sessionPath(key string) (string, error) {
	filename := schema.SanitizeKey(key)
	// filepath.IsLocal rejects empty names, "..", absolute paths and
	// OS-reserved device names (NUL, COM1 … on Windows). SanitizeKey has
	// already replaced '/' and '\' with '_', so no subdirectories are
	// created.
	if filename == "." || !filepath.IsLocal(filename) {
		return "", os.ErrInvalid
	}
	return filepath.Join(sm.storage, filename+".json"), nil
}

// DeleteSession removes a session from memory and deletes its backing file.
// Used by automatic session pruning. Missing files are ignored.
func (sm *SessionManager) DeleteSession(key string) error {
	sm.mu.Lock()
	delete(sm.sessions, key)
	sm.mu.Unlock()

	if sm.storage == "" {
		return nil
	}

	path, err := sm.sessionPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (sm *SessionManager) Save(_ context.Context, key string) error {
	if sm.storage == "" {
		return nil
	}

	// Validate the key before looking the session up so an invalid key fails
	// even when the session does not exist.
	sessionPath, err := sm.sessionPath(key)
	if err != nil {
		return err
	}

	// Snapshot under read lock, then perform slow file I/O after unlock.
	sm.mu.RLock()
	stored, ok := sm.sessions[key]
	if !ok {
		sm.mu.RUnlock()
		return nil
	}

	snapshot := sessionRecord{
		Key:     stored.Key,
		Summary: stored.Summary,
		Created: stored.Created,
		Updated: stored.Updated,
	}
	if len(stored.Messages) > 0 {
		snapshot.Messages = messageutil.FilterInvalidHistoryMessages(stored.Messages)
	} else {
		snapshot.Messages = []providers.Message{}
	}
	sm.mu.RUnlock()

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(sm.storage, "session-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		sessionPath := filepath.Join(sm.storage, file.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		var session sessionRecord
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}
		session.Messages = messageutil.FilterInvalidHistoryMessages(session.Messages)
		normalizeHistoryCreatedAt(session.Messages)

		sm.sessions[session.Key] = &session
	}

	return nil
}

// Close is a no-op for the in-memory SessionManager; it satisfies the
// SessionStore interface so callers can release resources uniformly.
func (sm *SessionManager) Close() error {
	return nil
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(_ context.Context, key string, history []providers.Message) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		history = messageutil.FilterInvalidHistoryMessages(history)
		// Copy the caller's slice and every nested field before storing so
		// later mutations on either side stay isolated. It also keeps the
		// CreatedAt normalization below from writing into the caller's
		// messages.
		msgs := messageutil.CloneHistory(history)
		normalizeHistoryCreatedAt(msgs)
		session.Messages = msgs
		session.Updated = time.Now()
	}
	return nil
}
