package session_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/routing"
	"github.com/sipeed/picoclaw/pkg/session"
)

// Compile-time interface satisfaction checks.
var (
	_ session.SessionStore = (*session.SessionManager)(nil)
	_ session.SessionStore = (*session.JSONLBackend)(nil)
)

func newBackend(t *testing.T) *session.JSONLBackend {
	t.Helper()
	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return session.NewJSONLBackend(store)
}

func mustAddMessage(t *testing.T, b *session.JSONLBackend, key, role, content string) {
	t.Helper()
	if err := b.AddMessage(context.Background(), key, role, content); err != nil {
		t.Fatalf("AddMessage(%q) failed: %v", key, err)
	}
}

func mustGetHistory(t *testing.T, b *session.JSONLBackend, key string) []providers.Message {
	t.Helper()
	history, err := b.GetHistory(context.Background(), key)
	if err != nil {
		t.Fatalf("GetHistory(%q) failed: %v", key, err)
	}
	return history
}

func TestJSONLBackend_AddAndGetHistory(t *testing.T) {
	b := newBackend(t)

	mustAddMessage(t, b, "s1", "user", "hello")
	mustAddMessage(t, b, "s1", "assistant", "hi")

	history := mustGetHistory(t, b, "s1")
	if len(history) != 2 {
		t.Fatalf("got %d messages, want 2", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hello" {
		t.Errorf("msg[0] = %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "hi" {
		t.Errorf("msg[1] = %+v", history[1])
	}
}

func TestJSONLBackend_AddFullMessage(t *testing.T) {
	b := newBackend(t)

	msg := providers.Message{
		Role:    "assistant",
		Content: "done",
		ToolCalls: []providers.ToolCall{
			{ID: "tc1", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"x"}`}},
		},
	}
	if err := b.AddFullMessage(context.Background(), "s1", msg); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	history := mustGetHistory(t, b, "s1")
	if len(history) != 1 {
		t.Fatalf("got %d, want 1", len(history))
	}
	if len(history[0].ToolCalls) != 1 || history[0].ToolCalls[0].ID != "tc1" {
		t.Errorf("tool calls = %+v", history[0].ToolCalls)
	}
}

func TestJSONLBackend_AddFullMessage_PreservesModelName(t *testing.T) {
	b := newBackend(t)

	msg := providers.Message{
		Role:      "assistant",
		Content:   "done",
		ModelName: "gpt-5.4-mini",
	}
	if err := b.AddFullMessage(context.Background(), "s1", msg); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	history := mustGetHistory(t, b, "s1")
	if len(history) != 1 {
		t.Fatalf("got %d, want 1", len(history))
	}
	if history[0].ModelName != "gpt-5.4-mini" {
		t.Fatalf("ModelName = %q, want %q", history[0].ModelName, "gpt-5.4-mini")
	}
}

func TestJSONLBackend_Summary(t *testing.T) {
	b := newBackend(t)

	if got, err := b.GetSummary(context.Background(), "s1"); err != nil {
		t.Fatalf("GetSummary: %v", err)
	} else if got != "" {
		t.Errorf("got %q, want empty", got)
	}

	if err := b.SetSummary(context.Background(), "s1", "test summary"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	if got, err := b.GetSummary(context.Background(), "s1"); err != nil {
		t.Fatalf("GetSummary: %v", err)
	} else if got != "test summary" {
		t.Errorf("got %q, want %q", got, "test summary")
	}
}

func TestJSONLBackend_TruncateAndSave(t *testing.T) {
	b := newBackend(t)

	for i := 0; i < 10; i++ {
		mustAddMessage(t, b, "s1", "user", fmt.Sprintf("msg %d", i))
	}
	if err := b.TruncateHistory(context.Background(), "s1", 3); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}

	history := mustGetHistory(t, b, "s1")
	if len(history) != 3 {
		t.Fatalf("got %d, want 3", len(history))
	}
	if history[0].Content != "msg 7" {
		t.Errorf("got %q, want %q", history[0].Content, "msg 7")
	}

	// Save triggers compaction.
	if err := b.Save(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}

	// Messages still accessible after compaction.
	history = mustGetHistory(t, b, "s1")
	if len(history) != 3 {
		t.Fatalf("after save: got %d, want 3", len(history))
	}
}

func TestJSONLBackend_SetHistory(t *testing.T) {
	b := newBackend(t)
	mustAddMessage(t, b, "s1", "user", "old")

	if err := b.SetHistory(context.Background(), "s1", []providers.Message{
		{Role: "user", Content: "new1"},
		{Role: "assistant", Content: "new2"},
	}); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	history := mustGetHistory(t, b, "s1")
	if len(history) != 2 {
		t.Fatalf("got %d, want 2", len(history))
	}
	if history[0].Content != "new1" {
		t.Errorf("got %q, want %q", history[0].Content, "new1")
	}
}

func TestJSONLBackend_EmptySession(t *testing.T) {
	b := newBackend(t)

	history := mustGetHistory(t, b, "nonexistent")
	if history == nil {
		t.Fatal("got nil, want empty slice")
	}
	if len(history) != 0 {
		t.Errorf("got %d, want 0", len(history))
	}
}

func TestJSONLBackend_SessionIsolation(t *testing.T) {
	b := newBackend(t)
	mustAddMessage(t, b, "s1", "user", "session1")
	mustAddMessage(t, b, "s2", "user", "session2")

	h1 := mustGetHistory(t, b, "s1")
	h2 := mustGetHistory(t, b, "s2")

	if len(h1) != 1 || h1[0].Content != "session1" {
		t.Errorf("s1: %+v", h1)
	}
	if len(h2) != 1 || h2[0].Content != "session2" {
		t.Errorf("s2: %+v", h2)
	}
}

func TestJSONLBackend_SummarizeFlow(t *testing.T) {
	// Simulates the real summarization flow in the agent loop:
	// SetSummary → TruncateHistory → Save
	b := newBackend(t)

	for i := 0; i < 20; i++ {
		mustAddMessage(t, b, "s1", "user", fmt.Sprintf("msg %d", i))
	}

	if err := b.SetSummary(context.Background(), "s1", "conversation about testing"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	if err := b.TruncateHistory(context.Background(), "s1", 4); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	if err := b.Save(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}

	if got, err := b.GetSummary(context.Background(), "s1"); err != nil {
		t.Fatalf("GetSummary: %v", err)
	} else if got != "conversation about testing" {
		t.Errorf("summary = %q", got)
	}
	history := mustGetHistory(t, b, "s1")
	if len(history) != 4 {
		t.Fatalf("got %d messages, want 4", len(history))
	}
	if history[0].Content != "msg 16" {
		t.Errorf("first message = %q, want %q", history[0].Content, "msg 16")
	}
}

func TestJSONLBackend_ResolveAliasAndPersistMetadata(t *testing.T) {
	b := newBackend(t)

	scope := &session.SessionScope{
		Version:    session.ScopeVersionV1,
		AgentID:    "main",
		Channel:    "telegram",
		Account:    "default",
		Dimensions: []string{"chat"},
		Values: map[string]string{
			"chat": "group:c1",
		},
	}
	b.EnsureSessionMetadata("canonical", scope, []string{"legacy"})

	if got := b.ResolveSessionKey("legacy"); got != "canonical" {
		t.Fatalf("ResolveSessionKey() = %q, want %q", got, "canonical")
	}

	mustAddMessage(t, b, "legacy", "user", "hello through alias")
	history := mustGetHistory(t, b, "canonical")
	if len(history) != 1 {
		t.Fatalf("len(history) = %d, want 1", len(history))
	}
	if history[0].Content != "hello through alias" {
		t.Fatalf("history[0].Content = %q, want %q", history[0].Content, "hello through alias")
	}

	resolvedScope := b.GetSessionScope("legacy")
	if resolvedScope == nil {
		t.Fatal("GetSessionScope() returned nil")
	}
	if resolvedScope.AgentID != scope.AgentID || resolvedScope.Values["chat"] != scope.Values["chat"] {
		t.Fatalf("GetSessionScope() = %+v, want %+v", resolvedScope, scope)
	}
}

func TestJSONLBackend_EnsureSessionMetadata_PromotesLegacyAliasHistory(t *testing.T) {
	b := newBackend(t)

	legacyKey := "agent:main:direct:legacy-user"
	mustAddMessage(t, b, legacyKey, "user", "legacy history")
	if err := b.SetSummary(context.Background(), legacyKey, "legacy summary"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}

	canonicalKey := session.BuildOpaqueSessionKey(legacyKey)
	b.EnsureSessionMetadata(canonicalKey, &session.SessionScope{
		Version: session.ScopeVersionV1,
		AgentID: "main",
	}, []string{legacyKey})

	if got := b.ResolveSessionKey(legacyKey); got != canonicalKey {
		t.Fatalf("ResolveSessionKey() = %q, want %q", got, canonicalKey)
	}
	history := mustGetHistory(t, b, canonicalKey)
	if len(history) != 1 || history[0].Content != "legacy history" {
		t.Fatalf("promoted history = %+v", history)
	}
	if summary, err := b.GetSummary(context.Background(), canonicalKey); err != nil {
		t.Fatalf("GetSummary: %v", err)
	} else if summary != "legacy summary" {
		t.Fatalf("promoted summary = %q, want %q", summary, "legacy summary")
	}
}

func TestJSONLBackend_EnsureSessionMetadata_PromotesLegacyPicoDirectAliasHistory(t *testing.T) {
	b := newBackend(t)

	legacyKey := "agent:main:pico:direct:pico:session-123"
	mustAddMessage(t, b, legacyKey, "user", "legacy pico history")

	scope := &session.SessionScope{
		Version:    session.ScopeVersionV1,
		AgentID:    "main",
		Channel:    "pico",
		Account:    "default",
		Dimensions: []string{"sender"},
		Values: map[string]string{
			"sender": "pico-user",
		},
	}
	allocation := session.AllocateRouteSession(session.AllocationInput{
		AgentID: "main",
		Context: bus.InboundContext{
			Channel:  "pico",
			Account:  "default",
			ChatID:   "pico:session-123",
			ChatType: "direct",
			SenderID: "pico-user",
		},
		SessionPolicy: routing.SessionPolicy{
			Dimensions: []string{"sender"},
		},
	})

	b.EnsureSessionMetadata(allocation.SessionKey, scope, allocation.SessionAliases)

	if got := b.ResolveSessionKey(legacyKey); got != allocation.SessionKey {
		t.Fatalf("ResolveSessionKey() = %q, want %q", got, allocation.SessionKey)
	}
	history := mustGetHistory(t, b, allocation.SessionKey)
	if len(history) != 1 || history[0].Content != "legacy pico history" {
		t.Fatalf("promoted history = %+v", history)
	}
}

func TestJSONLBackend_EnsureSessionMetadata_DoesNotOverwriteNonEmptyCanonicalHistory(t *testing.T) {
	b := newBackend(t)

	canonicalKey := session.BuildOpaqueSessionKey("agent:main:direct:current-user")
	legacyKey := "agent:main:direct:legacy-user"

	mustAddMessage(t, b, canonicalKey, "user", "current canonical history")
	mustAddMessage(t, b, legacyKey, "user", "legacy history")

	b.EnsureSessionMetadata(canonicalKey, &session.SessionScope{
		Version: session.ScopeVersionV1,
		AgentID: "main",
	}, []string{legacyKey})

	history := mustGetHistory(t, b, canonicalKey)
	if len(history) != 1 || history[0].Content != "current canonical history" {
		t.Fatalf("canonical history overwritten: %+v", history)
	}
}

func TestJSONLBackend_LastActivity(t *testing.T) {
	b := newBackend(t)

	if got := b.LastActivity("missing"); !got.IsZero() {
		t.Fatalf("LastActivity(missing) = %v, want zero", got)
	}

	before := time.Now().Add(-time.Second)
	mustAddMessage(t, b, "s1", "user", "hello")
	if got := b.LastActivity("s1"); got.Before(before) {
		t.Fatalf("LastActivity = %v, want >= %v", got, before)
	}
}

func TestJSONLBackend_DeleteSession(t *testing.T) {
	b := newBackend(t)

	mustAddMessage(t, b, "agent:main:s1", "user", "one")
	mustAddMessage(t, b, "agent:main:s2", "user", "two")
	if err := b.SetSummary(context.Background(), "agent:main:s1", "a summary"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}

	if err := b.DeleteSession("agent:main:s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := mustGetHistory(t, b, "agent:main:s1"); len(got) != 0 {
		t.Fatalf("history after delete = %v, want empty", got)
	}
	if got, err := b.GetSummary(context.Background(), "agent:main:s1"); err != nil {
		t.Fatalf("GetSummary: %v", err)
	} else if got != "" {
		t.Fatalf("summary after delete = %q, want empty", got)
	}
	sessions := b.ListSessions()
	if len(sessions) != 1 || sessions[0] != "agent:main:s2" {
		t.Fatalf("ListSessions = %v, want [agent:main:s2]", sessions)
	}
	if got := mustGetHistory(t, b, "agent:main:s2"); len(got) != 1 {
		t.Fatalf("other session history = %v, want untouched", got)
	}

	// Deleting an unknown or already-deleted session is a no-op.
	if err := b.DeleteSession("agent:main:s1"); err != nil {
		t.Fatalf("DeleteSession(again): %v", err)
	}
	if err := b.DeleteSession("agent:main:never-existed"); err != nil {
		t.Fatalf("DeleteSession(missing): %v", err)
	}
}

func TestJSONLBackend_DeleteSessionDoesNotFollowAliases(t *testing.T) {
	b := newBackend(t)

	mustAddMessage(t, b, "sk_canonical", "user", "keep me")
	b.EnsureSessionMetadata("sk_canonical", nil, []string{"agent:main:legacy"})
	if got := b.ResolveSessionKey("agent:main:legacy"); got != "sk_canonical" {
		t.Fatalf("alias resolves to %q, want sk_canonical (test setup)", got)
	}

	if err := b.DeleteSession("agent:main:legacy"); err != nil {
		t.Fatalf("DeleteSession(alias): %v", err)
	}
	if got := mustGetHistory(t, b, "sk_canonical"); len(got) != 1 {
		t.Fatalf("canonical session lost its history after deleting an alias: %v", got)
	}
}
