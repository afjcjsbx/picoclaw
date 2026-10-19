package session

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"telegram:123456", "telegram_123456"},
		{"discord:987654321", "discord_987654321"},
		{"slack:C01234", "slack_C01234"},
		{"no-colons-here", "no-colons-here"},
		{"multiple:colons:here", "multiple_colons_here"},
		{"agent:main:telegram:group:-1003822706455/12", "agent_main_telegram_group_-1003822706455_12"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSave_WithColonInKey(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Create a session with a key containing colon (typical channel session key).
	key := "telegram:123456"
	if err := sm.AddMessage(context.Background(), key, "user", "hello"); err != nil {
		t.Fatalf("AddMessage(%q) failed: %v", key, err)
	}

	// Save should succeed even though the key contains ':'
	if err := sm.Save(context.Background(), key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// The file on disk should use sanitized name.
	expectedFile := filepath.Join(tmpDir, "telegram_123456.json")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Fatalf("expected session file %s to exist", expectedFile)
	}

	// Load into a fresh manager and verify the session round-trips.
	sm2 := NewSessionManager(tmpDir)
	history, err := sm2.GetHistory(context.Background(), key)
	if err != nil {
		t.Fatalf("GetHistory(%q) failed: %v", key, err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected message content %q, got %q", "hello", history[0].Content)
	}
}

func TestSave_RejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Invalid names that must still be rejected.
	badKeys := []string{"", ".", ".."}
	for _, key := range badKeys {
		if err := sm.Save(context.Background(), key); err == nil {
			t.Errorf("Save(%q) should have failed but didn't", key)
		}
	}

	// Keys containing path separators are sanitized (no subdirs created).
	if err := sm.AddMessage(context.Background(), "foo/bar", "user", "hello"); err != nil {
		t.Fatalf("AddMessage(\"foo/bar\") failed: %v", err)
	}
	if err := sm.Save(context.Background(), "foo/bar"); err != nil {
		t.Fatalf("Save(\"foo/bar\") after sanitize should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "foo_bar.json")); os.IsNotExist(err) {
		t.Errorf("expected foo_bar.json in storage (sanitized from foo/bar)")
	}
}

func TestLoadSessions_NormalizesMissingCreatedAt(t *testing.T) {
	tmpDir := t.TempDir()
	sessionPath := filepath.Join(tmpDir, "telegram_legacy.json")
	legacy := `{
  "key": "telegram:legacy",
  "messages": [
    {
      "role": "user",
      "content": "hello"
    }
  ],
  "created": "2026-01-01T00:00:00Z",
  "updated": "2026-01-01T00:00:00Z"
}`

	if err := os.WriteFile(sessionPath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sm := NewSessionManager(tmpDir)
	history, err := sm.GetHistory(context.Background(), "telegram:legacy")
	if err != nil {
		t.Fatalf("GetHistory(telegram:legacy) failed: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d, want 1", len(history))
	}
	if history[0].CreatedAt == nil || history[0].CreatedAt.IsZero() {
		t.Fatalf("history[0].CreatedAt = %v, want non-zero timestamp", history[0].CreatedAt)
	}
}

func TestLastActivity(t *testing.T) {
	sm := NewSessionManager(t.TempDir())

	if got := sm.LastActivity("missing"); !got.IsZero() {
		t.Fatalf("LastActivity(missing) = %v, want zero", got)
	}

	before := time.Now().Add(-time.Second)
	if err := sm.AddMessage(context.Background(), "s1", "user", "hello"); err != nil {
		t.Fatalf("AddMessage(s1) failed: %v", err)
	}
	if got := sm.LastActivity("s1"); got.Before(before) {
		t.Fatalf("LastActivity = %v, want >= %v", got, before)
	}
}

func TestDeleteSession(t *testing.T) {
	dir := t.TempDir()
	sm := NewSessionManager(dir)

	for _, key := range []string{"telegram:1", "telegram:2"} {
		if err := sm.AddMessage(context.Background(), key, "user", "hello"); err != nil {
			t.Fatalf("AddMessage(%q): %v", key, err)
		}
		if err := sm.Save(context.Background(), key); err != nil {
			t.Fatalf("Save(%q): %v", key, err)
		}
	}

	if err := sm.DeleteSession("telegram:1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "telegram_1.json")); !os.IsNotExist(err) {
		t.Fatalf("session file still exists after delete (err=%v)", err)
	}
	if got := sm.ListSessions(); len(got) != 1 || got[0] != "telegram:2" {
		t.Fatalf("ListSessions = %v, want [telegram:2]", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "telegram_2.json")); err != nil {
		t.Fatalf("other session file was affected: %v", err)
	}

	// A reloaded manager must not resurrect the deleted session.
	if got := NewSessionManager(dir).ListSessions(); len(got) != 1 || got[0] != "telegram:2" {
		t.Fatalf("reloaded ListSessions = %v, want [telegram:2]", got)
	}

	// Unknown and already-deleted sessions are a no-op.
	if err := sm.DeleteSession("telegram:1"); err != nil {
		t.Fatalf("DeleteSession(again): %v", err)
	}
	if err := sm.DeleteSession("never-existed"); err != nil {
		t.Fatalf("DeleteSession(missing): %v", err)
	}
}

func TestDeleteSession_RejectsInvalidKey(t *testing.T) {
	sm := NewSessionManager(t.TempDir())
	if err := sm.DeleteSession("."); err != os.ErrInvalid {
		t.Fatalf("DeleteSession(\".\") = %v, want os.ErrInvalid", err)
	}
}

func TestDeleteSession_InMemoryManager(t *testing.T) {
	sm := NewSessionManager("")
	if err := sm.AddMessage(context.Background(), "s1", "user", "hello"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	if got := sm.ListSessions(); len(got) != 1 {
		t.Fatalf("ListSessions = %v, want [s1]", got)
	}
	if err := sm.DeleteSession("s1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if got := sm.ListSessions(); len(got) != 0 {
		t.Fatalf("ListSessions = %v, want none", got)
	}
}

func TestGetHistory_ReturnsIsolatedCopy(t *testing.T) {
	sm := NewSessionManager("")
	ctx := context.Background()

	if err := sm.AddFullMessage(ctx, "s1", sampleSessionMessage()); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	history, err := sm.GetHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}

	// Mutate every nested field through the returned history.
	mutateSessionMessage(&history[0])

	again, err := sm.GetHistory(ctx, "s1")
	if err != nil {
		t.Fatalf("GetHistory (second): %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("history len = %d, want 1", len(again))
	}
	assertSampleSessionMessage(t, again[0])
	wantCreatedAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if again[0].CreatedAt == nil || !again[0].CreatedAt.Equal(wantCreatedAt) {
		t.Fatalf("stored CreatedAt = %v, want %v", again[0].CreatedAt, wantCreatedAt)
	}
}

func TestSetHistory_CopiesInput(t *testing.T) {
	sm := NewSessionManager("")
	ctx := context.Background()
	key := "s1"

	// SetHistory only updates existing sessions.
	if err := sm.AddMessage(ctx, key, "user", "seed"); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	input := []providers.Message{sampleSessionMessage()}
	input[0].CreatedAt = nil

	if err := sm.SetHistory(ctx, key, input); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	// CreatedAt normalization must apply to the stored copy only.
	if input[0].CreatedAt != nil {
		t.Fatalf("caller's CreatedAt = %v, want nil after SetHistory", input[0].CreatedAt)
	}

	stored, err := sm.GetHistory(ctx, key)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("history len = %d, want 1", len(stored))
	}
	if stored[0].CreatedAt == nil || stored[0].CreatedAt.IsZero() {
		t.Fatalf("stored CreatedAt = %v, want normalized non-zero timestamp", stored[0].CreatedAt)
	}
	normalizedAt := *stored[0].CreatedAt

	// Mutating the caller's message afterwards must not leak into storage.
	mutateSessionMessage(&input[0])

	again, err := sm.GetHistory(ctx, key)
	if err != nil {
		t.Fatalf("GetHistory (second): %v", err)
	}
	if len(again) != 1 {
		t.Fatalf("history len = %d, want 1", len(again))
	}
	assertSampleSessionMessage(t, again[0])
	if again[0].CreatedAt == nil || !again[0].CreatedAt.Equal(normalizedAt) {
		t.Fatalf("stored CreatedAt = %v, want %v", again[0].CreatedAt, normalizedAt)
	}
}

func TestTruncateHistory_ReleasesBackingArray(t *testing.T) {
	sm := NewSessionManager("")
	ctx := context.Background()
	key := "s1"

	for _, content := range []string{"one", "two", "three"} {
		if err := sm.AddMessage(ctx, key, "user", content); err != nil {
			t.Fatalf("AddMessage(%q): %v", content, err)
		}
	}

	if err := sm.TruncateHistory(ctx, key, 1); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}

	sm.mu.RLock()
	msgs := sm.sessions[key].Messages
	sm.mu.RUnlock()

	if len(msgs) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(msgs))
	}
	if cap(msgs) != len(msgs) {
		t.Fatalf(
			"cap(Messages) = %d, want %d: truncated history still references the old backing array",
			cap(msgs), len(msgs),
		)
	}
	if msgs[0].Content != "three" {
		t.Fatalf("surviving message = %q, want %q", msgs[0].Content, "three")
	}
}

func sampleSessionMessage() providers.Message {
	createdAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	return providers.Message{
		Role:      "assistant",
		Content:   "hello",
		CreatedAt: &createdAt,
		Media:     []string{"media-1", "media-2"},
		Attachments: []providers.Attachment{
			{Type: "image", Filename: "a.png"},
		},
		SystemParts: []providers.ContentBlock{
			{Type: "text", Text: "system", CacheControl: &providers.CacheControl{Type: "ephemeral"}},
		},
		ToolCalls: []providers.ToolCall{
			{
				ID:       "call-1",
				Function: &providers.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`},
				Arguments: map[string]any{
					"nested": map[string]any{"deep": "value"},
					"list":   []any{"a", map[string]any{"b": true}},
					"scalar": "keep",
				},
				ExtraContent: &providers.ExtraContent{
					Google:                  &providers.GoogleExtra{ThoughtSignature: "sig"},
					ToolFeedbackExplanation: "why",
				},
			},
		},
	}
}

func mutateSessionMessage(msg *providers.Message) {
	msg.Role = "mutated"
	msg.Content = "mutated"
	if msg.CreatedAt != nil {
		*msg.CreatedAt = msg.CreatedAt.Add(24 * time.Hour)
	}
	msg.Media[0] = "mutated"
	msg.Attachments[0].Filename = "mutated"
	msg.SystemParts[0].Text = "mutated"
	msg.SystemParts[0].CacheControl.Type = "mutated"
	msg.ToolCalls[0].Function.Name = "mutated"
	msg.ToolCalls[0].Function.Arguments = "{}"
	msg.ToolCalls[0].Arguments["scalar"] = "mutated"
	msg.ToolCalls[0].Arguments["nested"].(map[string]any)["deep"] = "mutated"
	msg.ToolCalls[0].Arguments["list"].([]any)[0] = "mutated"
	msg.ToolCalls[0].Arguments["list"].([]any)[1].(map[string]any)["b"] = false
	msg.ToolCalls[0].ExtraContent.Google.ThoughtSignature = "mutated"
	msg.ToolCalls[0].ExtraContent.ToolFeedbackExplanation = "mutated"
}

func assertSampleSessionMessage(t *testing.T, msg providers.Message) {
	t.Helper()

	if msg.Role != "assistant" || msg.Content != "hello" {
		t.Fatalf("message identity mutated: role=%q content=%q", msg.Role, msg.Content)
	}
	if want := []string{"media-1", "media-2"}; !slices.Equal(msg.Media, want) {
		t.Fatalf("Media = %v, want %v", msg.Media, want)
	}
	if want := []providers.Attachment{{Type: "image", Filename: "a.png"}}; !slices.Equal(msg.Attachments, want) {
		t.Fatalf("Attachments = %+v, want %+v", msg.Attachments, want)
	}
	if len(msg.SystemParts) != 1 || msg.SystemParts[0].Text != "system" {
		t.Fatalf("SystemParts = %+v, want one text block", msg.SystemParts)
	}
	if msg.SystemParts[0].CacheControl == nil || msg.SystemParts[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("SystemParts[0].CacheControl = %+v, want ephemeral", msg.SystemParts[0].CacheControl)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want one call", msg.ToolCalls)
	}
	call := msg.ToolCalls[0]
	if call.ID != "call-1" || call.Function == nil || call.Function.Name != "lookup" ||
		call.Function.Arguments != `{"q":"x"}` {
		t.Fatalf("ToolCalls[0] = %+v, want call-1 with function lookup", call)
	}
	if got := call.Arguments["scalar"]; got != "keep" {
		t.Fatalf("Arguments[scalar] = %v, want keep", got)
	}
	nested, ok := call.Arguments["nested"].(map[string]any)
	if !ok || nested["deep"] != "value" {
		t.Fatalf("Arguments[nested] = %v, want map with deep=value", call.Arguments["nested"])
	}
	list, ok := call.Arguments["list"].([]any)
	if !ok || len(list) != 2 || list[0] != "a" {
		t.Fatalf("Arguments[list] = %v, want [a, {b:true}]", call.Arguments["list"])
	}
	inner, ok := list[1].(map[string]any)
	if !ok || inner["b"] != true {
		t.Fatalf("Arguments[list][1] = %v, want map with b=true", list[1])
	}
	if call.ExtraContent == nil || call.ExtraContent.ToolFeedbackExplanation != "why" {
		t.Fatalf("ExtraContent = %+v, want explanation why", call.ExtraContent)
	}
	if call.ExtraContent.Google == nil || call.ExtraContent.Google.ThoughtSignature != "sig" {
		t.Fatalf("ExtraContent.Google = %+v, want signature sig", call.ExtraContent.Google)
	}
}
