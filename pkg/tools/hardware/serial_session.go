package hardwaretools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func serialSessionPrefixFor(agentID, sessionKey string) string {
	if agentID == "" {
		agentID = "default"
	}
	if sessionKey == "" {
		sessionKey = "default"
	}
	return agentID + "\x00" + sessionKey + "\x00"
}

func serialSessionKey(ctx context.Context, port string) string {
	return serialSessionPrefixFor(toolshared.ToolAgentID(ctx), toolshared.ToolSessionKey(ctx)) + port
}

func serialSessionPrefix(ctx context.Context) string {
	return serialSessionPrefixFor(toolshared.ToolAgentID(ctx), toolshared.ToolSessionKey(ctx))
}

func (t *SerialTool) effectiveSessionTTL() time.Duration {
	if t.sessionTTL <= 0 {
		return defaultSerialSessionTTL
	}
	return t.sessionTTL
}

func (t *SerialTool) effectiveMaxSessions() int {
	if t.maxSessions <= 0 {
		return defaultSerialMaxSessions
	}
	return t.maxSessions
}

func (t *SerialTool) ensureSessionsLocked() {
	if t.sessions == nil {
		t.sessions = make(map[string]*serialSession)
	}
}

func (t *SerialTool) openSession(ctx context.Context, cfg serialConfig) *ToolResult {
	if result := t.writePolicy().checkTarget(cfg.Port); result != nil {
		return result
	}
	key := serialSessionKey(ctx, cfg.Port)

	t.mu.Lock()
	t.ensureSessionsLocked()
	previous := t.sessions[key]
	if previous != nil {
		delete(t.sessions, key)
	}
	t.mu.Unlock()
	if previous != nil {
		t.closeSessionObject(previous)
	}

	handle, err := serialOpenHandle(cfg, time.Second)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to open serial session on %s: %v", cfg.Port, err))
	}

	now := time.Now()
	session := &serialSession{
		key:      key,
		cfg:      cfg,
		handle:   handle,
		lastUsed: now,
	}

	t.mu.Lock()
	t.ensureSessionsLocked()
	evicted := t.evictOldestSessionLocked()
	t.sessions[key] = session
	session.timer = time.AfterFunc(t.effectiveSessionTTL(), func() {
		t.expireSession(key, session)
	})
	t.mu.Unlock()

	if evicted != nil {
		t.closeSessionObject(evicted)
	}

	result, _ := json.MarshalIndent(map[string]any{
		"action":       "open",
		"session":      "open",
		"port":         cfg.Port,
		"baud":         cfg.Baud,
		"data_bits":    cfg.DataBits,
		"parity":       cfg.Parity,
		"stop_bits":    cfg.StopBits,
		"flow_control": cfg.FlowControl,
		"rs485":        cfg.RS485,
		"ttl_seconds":  t.effectiveSessionTTL().Seconds(),
		"max_sessions": t.effectiveMaxSessions(),
		"replaced":     previous != nil,
	}, "", "  ")
	return SilentResult(string(result))
}

func (t *SerialTool) closeSession(ctx context.Context, port string) *ToolResult {
	key := serialSessionKey(ctx, port)
	t.mu.Lock()
	session := t.sessions[key]
	if session != nil {
		delete(t.sessions, key)
	}
	t.mu.Unlock()

	closed := session != nil
	if session != nil {
		t.closeSessionObject(session)
	}

	result, _ := json.MarshalIndent(map[string]any{
		"action": "close",
		"port":   port,
		"closed": closed,
	}, "", "  ")
	return SilentResult(string(result))
}

func (t *SerialTool) listSessions(ctx context.Context) *ToolResult {
	prefix := serialSessionPrefix(ctx)
	now := time.Now()

	t.mu.Lock()
	sessions := make([]map[string]any, 0, len(t.sessions))
	for key, session := range t.sessions {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		sessions = append(sessions, map[string]any{
			"port":         session.cfg.Port,
			"baud":         session.cfg.Baud,
			"data_bits":    session.cfg.DataBits,
			"parity":       session.cfg.Parity,
			"stop_bits":    session.cfg.StopBits,
			"flow_control": session.cfg.FlowControl,
			"rs485":        session.cfg.RS485,
			"idle_ms":      now.Sub(session.lastUsed).Milliseconds(),
		})
	}
	t.mu.Unlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i]["port"].(string) < sessions[j]["port"].(string)
	})
	result, _ := json.MarshalIndent(map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
	}, "", "  ")
	return SilentResult(string(result))
}

// lockSession returns the open session for cfg, holding its I/O lock. The
// caller must call unlock when non-nil. A nil session means no explicit
// session was opened and the caller should use a transient handle.
func (t *SerialTool) lockSession(
	ctx context.Context,
	cfg serialConfig,
) (*serialSession, func(), *ToolResult) {
	key := serialSessionKey(ctx, cfg.Port)

	t.mu.Lock()
	session := t.sessions[key]
	if session == nil {
		t.mu.Unlock()
		return nil, nil, nil
	}
	if session.cfg != cfg {
		t.mu.Unlock()
		return nil, nil, ErrorResult(fmt.Sprintf(
			"serial session on %s is open with different settings; close it first or reuse the same settings",
			cfg.Port,
		))
	}
	t.mu.Unlock()

	// Do not hold t.mu while waiting for a slow read/write: close, /clear, and
	// session listing must still make progress. Re-check the map after taking
	// the session lock so a concurrent close cannot leave us using a dead fd.
	session.mu.Lock()
	t.mu.Lock()
	if t.sessions[key] != session {
		t.mu.Unlock()
		session.mu.Unlock()
		return nil, nil, nil
	}
	session.lastUsed = time.Now()
	if session.timer != nil {
		session.timer.Reset(t.effectiveSessionTTL())
	}
	t.mu.Unlock()

	return session, session.mu.Unlock, nil
}

func (t *SerialTool) expireSession(key string, session *serialSession) {
	t.mu.Lock()
	if t.sessions[key] != session {
		t.mu.Unlock()
		return
	}
	ttl := t.effectiveSessionTTL()
	idle := time.Since(session.lastUsed)
	if idle < ttl {
		// The timer fired just as the session was used; reschedule for the
		// remaining idle budget instead of closing an active port.
		if session.timer != nil {
			session.timer.Reset(ttl - idle)
		}
		t.mu.Unlock()
		return
	}
	delete(t.sessions, key)
	t.mu.Unlock()

	t.closeSessionObject(session)
}

func (t *SerialTool) evictOldestSessionLocked() *serialSession {
	limit := t.effectiveMaxSessions()
	if len(t.sessions) < limit {
		return nil
	}
	var oldest *serialSession
	for _, session := range t.sessions {
		if oldest == nil || session.lastUsed.Before(oldest.lastUsed) {
			oldest = session
		}
	}
	if oldest != nil {
		delete(t.sessions, oldest.key)
	}
	return oldest
}

func (t *SerialTool) closeSessionObject(session *serialSession) {
	if session == nil {
		return
	}
	if session.timer != nil {
		session.timer.Stop()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	_ = serialCloseHandle(session.handle)
}

// ClearSession closes every persistent serial session owned by one agent
// conversation. The agent calls it on /clear.
func (t *SerialTool) ClearSession(agentID, sessionKey string) {
	prefix := serialSessionPrefixFor(agentID, sessionKey)
	t.closeSessionsWithPrefix(prefix)
}

// CloseAll closes every persistent serial session held by this tool.
func (t *SerialTool) CloseAll() {
	t.closeSessionsWithPrefix("")
}

func (t *SerialTool) closeSessionsWithPrefix(prefix string) {
	t.mu.Lock()
	var closing []*serialSession
	for key, session := range t.sessions {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}
		delete(t.sessions, key)
		closing = append(closing, session)
	}
	t.mu.Unlock()

	for _, session := range closing {
		t.closeSessionObject(session)
	}
}
