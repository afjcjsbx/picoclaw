// Cross-backend conformance suite for pkg/session.
//
// pkg/session ships two implementations of the SessionStore contract: the
// legacy SessionManager (in-memory plus JSON snapshot files) and the current
// JSONLBackend (backed by pkg/memory.JSONLStore). Each backend has its own
// tests, but nothing pinned the behavior they must share until this suite.
// Every check below runs against both backends, so accidental drift in either
// implementation fails CI instead of surfacing in production.
//
// The suite uses the public API only (package session_test), creates backends
// through their real constructors and registers Close in t.Cleanup.
//
// Known intentional differences between the backends are deliberately NOT
// asserted here:
//
//   - Alias and scope metadata: EnsureSessionMetadata, GetSessionScope and
//     ResolveSessionKey exist only on JSONLBackend; SessionManager models no
//     aliases (pkg/session/jsonl_backend.go).
//   - Writes to an unknown session: SessionManager.SetSummary, SetHistory and
//     TruncateHistory are no-ops unless the session already exists
//     (pkg/session/manager.go); the JSONL store creates a metadata file for
//     the key, which then appears in ListSessions (pkg/memory/jsonl.go).
//     The tests below therefore seed a session before calling those methods.
//   - Durability: SessionManager buffers writes in memory and persists them on
//     Save, while JSONLStore fsyncs every write and Save only compacts
//     (pkg/session/jsonl_backend.go, pkg/memory/jsonl.go).
//   - Invalid keys ("" and "."): SessionManager.Save and DeleteSession reject
//     them with os.ErrInvalid (pkg/session/manager.go); the JSONL store trims
//     and ignores an empty key (pkg/memory/jsonl.go).
//   - ListSessions over an empty store: SessionManager returns a non-nil empty
//     slice, JSONLStore returns nil; both report zero sessions.
package session_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// storeFactory pairs a backend name with its constructor. The returned store
// is closed via t.Cleanup.
type storeFactory struct {
	name string
	new  func(t *testing.T) session.SessionStore
}

var storeFactories = []storeFactory{
	{
		name: "memory",
		new: func(t *testing.T) session.SessionStore {
			t.Helper()
			store := session.NewSessionManager(t.TempDir())
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
	},
	{
		name: "jsonl",
		new: func(t *testing.T) session.SessionStore {
			t.Helper()
			store, err := memory.NewJSONLStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewJSONLStore: %v", err)
			}
			backend := session.NewJSONLBackend(store)
			t.Cleanup(func() { _ = backend.Close() })
			return backend
		},
	},
}

// extendedStore adds the pruner surface both concrete backends expose beyond
// session.SessionStore (see the interface comment in session_store.go), so the
// suite can lock DeleteSession and LastActivity behavior too.
type extendedStore interface {
	session.SessionStore
	DeleteSession(key string) error
	LastActivity(key string) time.Time
}

// Compile-time checks that both backends satisfy the wider test interface.
var (
	_ extendedStore = (*session.SessionManager)(nil)
	_ extendedStore = (*session.JSONLBackend)(nil)
)

// forEachStore runs check against a fresh instance of every backend.
func forEachStore(t *testing.T, check func(t *testing.T, store session.SessionStore)) {
	t.Helper()
	for _, factory := range storeFactories {
		t.Run(factory.name, func(t *testing.T) {
			check(t, factory.new(t))
		})
	}
}

func asExtendedStore(t *testing.T, store session.SessionStore) extendedStore {
	t.Helper()
	extended, ok := store.(extendedStore)
	if !ok {
		t.Fatalf("store %T does not expose DeleteSession/LastActivity", store)
	}
	return extended
}

func confAddMessage(t *testing.T, store session.SessionStore, key, role, content string) {
	t.Helper()
	if err := store.AddMessage(context.Background(), key, role, content); err != nil {
		t.Fatalf("AddMessage(%q) failed: %v", key, err)
	}
}

func confHistory(t *testing.T, store session.SessionStore, key string) []providers.Message {
	t.Helper()
	history, err := store.GetHistory(context.Background(), key)
	if err != nil {
		t.Fatalf("GetHistory(%q) failed: %v", key, err)
	}
	return history
}

func confContents(history []providers.Message) []string {
	contents := make([]string, len(history))
	for i := range history {
		contents[i] = history[i].Content
	}
	return contents
}

func confRequireContents(t *testing.T, history []providers.Message, want ...string) {
	t.Helper()
	if len(history) != len(want) {
		t.Fatalf("history contents = %v, want %v", confContents(history), want)
	}
	for i, content := range want {
		if history[i].Content != content {
			t.Fatalf("history[%d].Content = %q, want %q", i, history[i].Content, content)
		}
	}
}

func TestConformance_GetHistory_UnknownSession(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		history, err := store.GetHistory(context.Background(), "conf:never-written")
		if err != nil {
			t.Fatalf("GetHistory(unknown) error = %v, want nil", err)
		}
		if history == nil {
			t.Fatal("GetHistory(unknown) = nil, want non-nil empty slice")
		}
		if len(history) != 0 {
			t.Fatalf("GetHistory(unknown) = %d messages, want 0", len(history))
		}
	})
}

func TestConformance_AddMessage_RoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		key := "agent:main:telegram:direct:peer-1"
		confAddMessage(t, store, key, "user", "hello conformance")

		history := confHistory(t, store, key)
		if len(history) != 1 {
			t.Fatalf("len(history) = %d, want 1", len(history))
		}
		if history[0].Role != "user" || history[0].Content != "hello conformance" {
			t.Fatalf("history[0] = %+v, want user/hello conformance", history[0])
		}
	})
}

func TestConformance_AddFullMessage_RoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		key := "agent:main:telegram:direct:peer-2"
		msg := providers.Message{
			Role:    "assistant",
			Content: "calling a tool",
			ToolCalls: []providers.ToolCall{
				{
					ID:       "call-1",
					Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"README.md"}`},
				},
			},
		}
		if err := store.AddFullMessage(context.Background(), key, msg); err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}

		history := confHistory(t, store, key)
		if len(history) != 1 {
			t.Fatalf("len(history) = %d, want 1", len(history))
		}
		stored := history[0]
		if stored.Role != "assistant" || stored.Content != "calling a tool" {
			t.Fatalf("stored = %+v, want assistant/calling a tool", stored)
		}
		if len(stored.ToolCalls) != 1 {
			t.Fatalf("len(ToolCalls) = %d, want 1", len(stored.ToolCalls))
		}
		call := stored.ToolCalls[0]
		if call.ID != "call-1" || call.Function == nil || call.Function.Name != "read_file" ||
			call.Function.Arguments != `{"path":"README.md"}` {
			t.Fatalf("ToolCalls[0] = %+v, want round-tripped read_file call", call)
		}
	})
}

func TestConformance_Summary_RoundTrip(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		ctx := context.Background()
		key := "agent:main:telegram:direct:peer-3"

		if got, err := store.GetSummary(ctx, key); err != nil {
			t.Fatalf("GetSummary(unknown) error = %v, want nil", err)
		} else if got != "" {
			t.Fatalf("GetSummary(unknown) = %q, want empty", got)
		}

		// Seed first: SessionManager.SetSummary only updates existing sessions
		// (see the file header), so a bare SetSummary would be a no-op there.
		confAddMessage(t, store, key, "user", "seed")

		if err := store.SetSummary(ctx, key, "conformance summary"); err != nil {
			t.Fatalf("SetSummary: %v", err)
		}
		if got, err := store.GetSummary(ctx, key); err != nil {
			t.Fatalf("GetSummary error = %v, want nil", err)
		} else if got != "conformance summary" {
			t.Fatalf("GetSummary = %q, want %q", got, "conformance summary")
		}
	})
}

func TestConformance_SetHistory_ReplacesHistory(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		ctx := context.Background()
		key := "agent:main:telegram:direct:peer-4"
		confAddMessage(t, store, key, "user", "old")

		replacement := []providers.Message{
			{Role: "user", Content: "new 1"},
			{Role: "assistant", Content: "new 2"},
		}
		if err := store.SetHistory(ctx, key, replacement); err != nil {
			t.Fatalf("SetHistory: %v", err)
		}

		history := confHistory(t, store, key)
		confRequireContents(t, history, "new 1", "new 2")
		if history[0].Role != "user" || history[1].Role != "assistant" {
			t.Fatalf("roles = [%q, %q], want [user, assistant]", history[0].Role, history[1].Role)
		}
	})
}

func TestConformance_TruncateHistory_KeepsTailInOrder(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		ctx := context.Background()
		key := "agent:main:telegram:direct:peer-5"
		for i := 1; i <= 5; i++ {
			confAddMessage(t, store, key, "user", fmt.Sprintf("msg %d", i))
		}

		if err := store.TruncateHistory(ctx, key, 2); err != nil {
			t.Fatalf("TruncateHistory(2): %v", err)
		}
		confRequireContents(t, confHistory(t, store, key), "msg 4", "msg 5")

		// keepLast larger than the remaining history is a no-op.
		if err := store.TruncateHistory(ctx, key, 10); err != nil {
			t.Fatalf("TruncateHistory(10): %v", err)
		}
		confRequireContents(t, confHistory(t, store, key), "msg 4", "msg 5")
	})
}

func TestConformance_TruncateHistory_NonPositiveEmpties(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		ctx := context.Background()
		key := "agent:main:telegram:direct:peer-6"
		for i := 1; i <= 3; i++ {
			confAddMessage(t, store, key, "user", fmt.Sprintf("msg %d", i))
		}

		if err := store.TruncateHistory(ctx, key, 0); err != nil {
			t.Fatalf("TruncateHistory(0): %v", err)
		}
		history := confHistory(t, store, key)
		if history == nil {
			t.Fatal("history after TruncateHistory(0) = nil, want non-nil empty slice")
		}
		confRequireContents(t, history)

		// A negative keepLast also empties, and the session stays usable.
		confAddMessage(t, store, key, "user", "after truncate")
		confRequireContents(t, confHistory(t, store, key), "after truncate")
		if err := store.TruncateHistory(ctx, key, -1); err != nil {
			t.Fatalf("TruncateHistory(-1): %v", err)
		}
		if history := confHistory(t, store, key); len(history) != 0 {
			t.Fatalf("history after TruncateHistory(-1) = %v, want empty", confContents(history))
		}
	})
}

func TestConformance_ListSessions_IncludesWrittenSession(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		if keys := store.ListSessions(); len(keys) != 0 {
			t.Fatalf("ListSessions before any write = %v, want none", keys)
		}

		key := "agent:main:telegram:direct:peer-7"
		confAddMessage(t, store, key, "user", "hello")
		if keys := store.ListSessions(); len(keys) != 1 || keys[0] != key {
			t.Fatalf("ListSessions = %v, want [%s]", keys, key)
		}
	})
}

func TestConformance_LastActivity(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		extended := asExtendedStore(t, store)

		if got := extended.LastActivity("conf:never-written"); !got.IsZero() {
			t.Fatalf("LastActivity(unknown) = %v, want zero time", got)
		}

		key := "agent:main:telegram:direct:peer-8"
		before := time.Now().Add(-time.Second)
		confAddMessage(t, store, key, "user", "hello")
		after := time.Now().Add(time.Second)

		got := extended.LastActivity(key)
		if got.IsZero() {
			t.Fatal("LastActivity(written session) = zero time, want the write time")
		}
		if got.Before(before) || got.After(after) {
			t.Fatalf("LastActivity(written session) = %v, want within [%v, %v]", got, before, after)
		}
	})
}

func TestConformance_DeleteSession(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		extended := asExtendedStore(t, store)
		keyA := "agent:main:telegram:direct:peer-9"
		keyB := "agent:main:telegram:direct:peer-10"
		confAddMessage(t, store, keyA, "user", "one")
		confAddMessage(t, store, keyB, "user", "two")

		if err := extended.DeleteSession(keyA); err != nil {
			t.Fatalf("DeleteSession(%q): %v", keyA, err)
		}
		if history := confHistory(t, store, keyA); len(history) != 0 {
			t.Fatalf("history after delete = %v, want empty", confContents(history))
		}
		keys := store.ListSessions()
		if slices.Contains(keys, keyA) {
			t.Fatalf("ListSessions = %v, still contains deleted key %q", keys, keyA)
		}
		if !slices.Contains(keys, keyB) {
			t.Fatalf("ListSessions = %v, want surviving key %q", keys, keyB)
		}
		confRequireContents(t, confHistory(t, store, keyB), "two")

		// Deleting an already-deleted or never-written session is a no-op.
		if err := extended.DeleteSession(keyA); err != nil {
			t.Fatalf("DeleteSession(%q) again: %v", keyA, err)
		}
		if err := extended.DeleteSession("conf:never-written"); err != nil {
			t.Fatalf("DeleteSession(unknown): %v", err)
		}
	})
}

func TestConformance_Save(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		ctx := context.Background()
		key := "agent:main:telegram:direct:peer-11"
		confAddMessage(t, store, key, "user", "hello")

		if err := store.Save(ctx, key); err != nil {
			t.Fatalf("Save(%q): %v", key, err)
		}
		confRequireContents(t, confHistory(t, store, key), "hello")

		// Saving a session that was never written is also a no-op, not an error.
		if err := store.Save(ctx, "conf:never-written"); err != nil {
			t.Fatalf("Save(unknown): %v", err)
		}
	})
}

func TestConformance_GetHistory_MutationIsolation(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		key := "agent:main:telegram:direct:peer-12"
		confAddMessage(t, store, key, "user", "original")

		history := confHistory(t, store, key)
		if len(history) != 1 {
			t.Fatalf("len(history) = %d, want 1", len(history))
		}
		history[0].Content = "mutated"

		confRequireContents(t, confHistory(t, store, key), "original")
	})
}

func TestConformance_SetHistory_FiltersInvalidRecords(t *testing.T) {
	forEachStore(t, func(t *testing.T, store session.SessionStore) {
		ctx := context.Background()
		key := "agent:main:telegram:direct:peer-13"
		confAddMessage(t, store, key, "user", "seed")

		// Both backends drop transient assistant thought records: a
		// reasoning-only assistant message with no content, tool calls, media
		// or attachments (messageutil.IsTransientAssistantThoughtMessage).
		thought := providers.Message{
			Role:             "assistant",
			ReasoningContent: "thinking hard",
		}
		valid := providers.Message{Role: "user", Content: "kept"}
		if err := store.SetHistory(ctx, key, []providers.Message{valid, thought}); err != nil {
			t.Fatalf("SetHistory: %v", err)
		}

		confRequireContents(t, confHistory(t, store, key), "kept")
	})
}
