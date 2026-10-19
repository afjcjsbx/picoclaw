package session_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/session"
)

// countingResolveStore wraps a real JSONL store and counts calls to
// ResolveSessionKey, so tests can tell whether the backend resolved an alias
// through the store or served it from its alias cache.
type countingResolveStore struct {
	*memory.JSONLStore
	resolveCalls atomic.Int64
}

func (s *countingResolveStore) ResolveSessionKey(ctx context.Context, sessionKey string) (string, bool, error) {
	s.resolveCalls.Add(1)
	return s.JSONLStore.ResolveSessionKey(ctx, sessionKey)
}

func newCountingBackend(t *testing.T) (*session.JSONLBackend, *countingResolveStore) {
	t.Helper()
	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	counting := &countingResolveStore{JSONLStore: store}
	return session.NewJSONLBackend(counting), counting
}

const (
	aliasCacheCanonical = "sk_v1_alias_cache_canonical"
	aliasCacheAlias     = "agent:main:direct:legacy-user"
)

// TestJSONLBackend_AliasResolutionIsCached verifies that resolving the same
// alias twice consults the metadata store only once.
func TestJSONLBackend_AliasResolutionIsCached(t *testing.T) {
	backend, counting := newCountingBackend(t)
	ctx := context.Background()

	// Write the metadata directly on the store: EnsureSessionMetadata seeds
	// the cache and would hide the resolve-then-cache path under test.
	if err := counting.UpsertSessionMeta(ctx, aliasCacheCanonical, nil, []string{aliasCacheAlias}); err != nil {
		t.Fatalf("UpsertSessionMeta: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, err := backend.GetHistory(ctx, aliasCacheAlias); err != nil {
			t.Fatalf("GetHistory(%q) #%d: %v", aliasCacheAlias, i+1, err)
		}
	}
	if got := counting.resolveCalls.Load(); got != 1 {
		t.Fatalf("store ResolveSessionKey calls = %d, want 1 (second lookup must hit the cache)", got)
	}
}

// TestJSONLBackend_EnsureSessionMetadataSeedsAliasCache verifies that the
// aliases written by EnsureSessionMetadata are resolved without consulting
// the store again.
func TestJSONLBackend_EnsureSessionMetadataSeedsAliasCache(t *testing.T) {
	backend, counting := newCountingBackend(t)
	ctx := context.Background()

	backend.EnsureSessionMetadata(aliasCacheCanonical, nil, []string{aliasCacheAlias})

	if _, err := backend.GetHistory(ctx, aliasCacheAlias); err != nil {
		t.Fatalf("GetHistory(%q): %v", aliasCacheAlias, err)
	}
	if got := counting.resolveCalls.Load(); got != 0 {
		t.Fatalf("store ResolveSessionKey calls = %d, want 0 (upsert seeds the cache)", got)
	}
}

// TestJSONLBackend_DeleteSessionInvalidatesAliasCache verifies that deleting
// the canonical session drops aliases cached against it, so a later lookup
// consults the store and falls back to the input key.
func TestJSONLBackend_DeleteSessionInvalidatesAliasCache(t *testing.T) {
	backend, counting := newCountingBackend(t)
	ctx := context.Background()

	if err := counting.UpsertSessionMeta(ctx, aliasCacheCanonical, nil, []string{aliasCacheAlias}); err != nil {
		t.Fatalf("UpsertSessionMeta: %v", err)
	}

	// First resolution fills the cache; the second must not hit the store.
	for i := 0; i < 2; i++ {
		if got := backend.ResolveSessionKey(aliasCacheAlias); got != aliasCacheCanonical {
			t.Fatalf("ResolveSessionKey(%q) #%d = %q, want %q", aliasCacheAlias, i+1, got, aliasCacheCanonical)
		}
	}
	if got := counting.resolveCalls.Load(); got != 1 {
		t.Fatalf("store ResolveSessionKey calls = %d, want 1 before delete", got)
	}

	if err := backend.DeleteSession(aliasCacheCanonical); err != nil {
		t.Fatalf("DeleteSession(%q): %v", aliasCacheCanonical, err)
	}

	// The metadata is gone, so the alias must be resolved again and fall back
	// to the input key instead of a stale cache entry pointing at the deleted
	// session.
	if got := backend.ResolveSessionKey(aliasCacheAlias); got != aliasCacheAlias {
		t.Fatalf("ResolveSessionKey(%q) after delete = %q, want fallback %q", aliasCacheAlias, got, aliasCacheAlias)
	}
	if got := counting.resolveCalls.Load(); got != 2 {
		t.Fatalf("store ResolveSessionKey calls = %d, want 2 after delete (cache entry must be dropped)", got)
	}
}

// BenchmarkResolveSessionKeyAlias measures alias resolution against a store
// holding 100 extra sessions: directly through the store (uncached) versus
// through the backend's alias cache (steady state).
func BenchmarkResolveSessionKeyAlias(b *testing.B) {
	store, err := memory.NewJSONLStore(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { store.Close() })

	ctx := context.Background()
	if err := store.UpsertSessionMeta(ctx, aliasCacheCanonical, nil, []string{aliasCacheAlias}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("agent:main:direct:user-%d", i)
		if err := store.UpsertSessionMeta(ctx, key, nil, nil); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("store", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, found, err := store.ResolveSessionKey(ctx, aliasCacheAlias); err != nil || !found {
				b.Fatalf("ResolveSessionKey(%q) = found %v, err %v", aliasCacheAlias, found, err)
			}
		}
	})

	b.Run("cached", func(b *testing.B) {
		backend := session.NewJSONLBackend(store)
		// Warm the cache before the timed loop.
		if got := backend.ResolveSessionKey(aliasCacheAlias); got != aliasCacheCanonical {
			b.Fatalf("ResolveSessionKey(%q) = %q, want %q", aliasCacheAlias, got, aliasCacheCanonical)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if got := backend.ResolveSessionKey(aliasCacheAlias); got != aliasCacheCanonical {
				b.Fatalf("ResolveSessionKey(%q) = %q, want %q", aliasCacheAlias, got, aliasCacheCanonical)
			}
		}
	})
}
