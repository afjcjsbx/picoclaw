package pruner

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

func TestSelectAge(t *testing.T) {
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	infos := []Info{
		{Key: "old", NewestAt: now.Add(-40 * 24 * time.Hour)},
		{Key: "recent", NewestAt: now.Add(-2 * 24 * time.Hour)},
		{Key: "unknown"},
	}
	got := Select(infos, config.SessionPruneConfig{MaxAgeDays: 30}, now)

	want := []string{"old"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %v, want %v", got, want)
	}
}

func TestSelectMaxSessionsKeepsNewest(t *testing.T) {
	now := time.Now()
	infos := []Info{
		{Key: "a", NewestAt: now.Add(-10 * time.Hour)},
		{Key: "b", NewestAt: now.Add(-1 * time.Hour)},
		{Key: "c", NewestAt: now.Add(-5 * time.Hour)},
	}
	got := Select(infos, config.SessionPruneConfig{MaxSessions: 2}, now)

	// Oldest-first: "a" (10h) is dropped; "c" (5h) and "b" (1h) are kept.
	want := []string{"a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %v, want %v", got, want)
	}
}

func TestSelectCombinesAgeAndCount(t *testing.T) {
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	infos := []Info{
		{Key: "ancient", NewestAt: now.Add(-90 * 24 * time.Hour)},
		{Key: "middle", NewestAt: now.Add(-3 * 24 * time.Hour)},
		{Key: "fresh", NewestAt: now.Add(-1 * time.Hour)},
	}
	got := Select(infos, config.SessionPruneConfig{MaxAgeDays: 30, MaxSessions: 2}, now)

	// age deletes "ancient"; count keeps the 2 newest ("middle","fresh").
	want := []string{"ancient"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select() = %v, want %v", got, want)
	}
}

func TestSelectUndatedNeverDeleted(t *testing.T) {
	now := time.Now()
	infos := []Info{
		{Key: "unknown"},
		{Key: "fresh", NewestAt: now.Add(-1 * time.Hour)},
		{Key: "stale", NewestAt: now.Add(-5 * time.Hour)},
	}
	// Age-only: an undated session must not be age-pruned.
	if got := Select(infos, config.SessionPruneConfig{MaxAgeDays: 1}, now); len(got) != 0 {
		t.Fatalf("Select(age) = %v, want none", got)
	}
	// Count: the undated session occupies a slot but is never the one dropped;
	// the oldest dated session goes instead.
	got := Select(infos, config.SessionPruneConfig{MaxSessions: 2}, now)
	if !reflect.DeepEqual(got, []string{"stale"}) {
		t.Fatalf("Select(count) = %v, want [stale]", got)
	}
	// Cap smaller than the number of dated sessions still never touches it.
	got = Select(infos, config.SessionPruneConfig{MaxSessions: 1}, now)
	if !reflect.DeepEqual(got, []string{"stale", "fresh"}) {
		t.Fatalf("Select(count=1) = %v, want [stale fresh]", got)
	}
}

func TestSelectStoreActivityKeepsRecentSession(t *testing.T) {
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, time.UTC)
	infos := []Info{
		// Stale seahorse index, but the JSONL store was written an hour ago.
		{Key: "lagging", NewestAt: now.Add(-90 * 24 * time.Hour), StoreAt: now.Add(-time.Hour)},
		// Never indexed by seahorse; only the store knows it is old.
		{Key: "jsonl-old", StoreAt: now.Add(-60 * 24 * time.Hour)},
		// Never indexed and no activity info at all.
		{Key: "jsonl-undated"},
	}
	got := Select(infos, config.SessionPruneConfig{MaxAgeDays: 30}, now)
	if !reflect.DeepEqual(got, []string{"jsonl-old"}) {
		t.Fatalf("Select() = %v, want [jsonl-old]", got)
	}
}

func TestRunUsesStoreActivityForJSONLOnlySessions(t *testing.T) {
	now := time.Now()
	eng := newFakeEngine(1 << 20)
	store := newFakeStore("a-old", "b-recent", "c-recent")
	store.activity = map[string]time.Time{
		"a-old":    now.Add(-48 * time.Hour),
		"b-recent": now.Add(-time.Hour),
		"c-recent": now.Add(-2 * time.Hour),
	}

	// With no seahorse data every session is JSONL-only. Previously all of them
	// were undated and dropped alphabetically; now the oldest by store time goes.
	res := Run(context.Background(), eng, []Store{store}, config.SessionPruneConfig{
		Enabled:     true,
		MaxSessions: 2,
	})
	if !reflect.DeepEqual(res.Deleted, []string{"a-old"}) {
		t.Fatalf("Deleted = %v, want [a-old]", res.Deleted)
	}
}

func TestSelectDisabledThresholds(t *testing.T) {
	now := time.Now()
	infos := []Info{{Key: "a", NewestAt: now.Add(-1000 * time.Hour)}}
	if got := Select(infos, config.SessionPruneConfig{}, now); got != nil {
		t.Fatalf("Select() = %v, want nil", got)
	}
}

func TestRunAppliesAgeCountAndVacuum(t *testing.T) {
	now := time.Now()
	eng := newFakeEngine(1 << 20)
	eng.statuses = []seahorse.SessionStatus{
		{SessionKey: "a", NewestAt: now.Add(-100 * 24 * time.Hour)},
		{SessionKey: "b", NewestAt: now.Add(-50 * 24 * time.Hour)},
		{SessionKey: "c", NewestAt: now.Add(-1 * time.Hour)},
	}
	store := newFakeStore("a", "b", "c")

	res := Run(context.Background(), eng, []Store{store}, config.SessionPruneConfig{
		Enabled:     true,
		MaxAgeDays:  30,
		MaxSessions: 2,
		Vacuum:      boolPtr(true),
	})

	if len(res.Deleted) == 0 {
		t.Fatal("expected deletions, got none")
	}
	if eng.vacuumCount == 0 {
		t.Fatal("expected VACUUM to be called")
	}
	for _, key := range []string{"a", "b"} {
		if !contains(store.deleted, key) {
			t.Fatalf("store did not delete %q; deleted=%v", key, store.deleted)
		}
		if !contains(eng.deleted, key) {
			t.Fatalf("engine did not delete %q; deleted=%v", key, eng.deleted)
		}
	}
}

func TestRunEnforcesDBSize(t *testing.T) {
	now := time.Now()
	const mb = 1 << 20
	// Each remaining session accounts for 1 MB of DB size.
	eng := newFakeEngine(mb)
	eng.statuses = []seahorse.SessionStatus{
		{SessionKey: "s1", NewestAt: now.Add(-5 * time.Hour)},
		{SessionKey: "s2", NewestAt: now.Add(-4 * time.Hour)},
		{SessionKey: "s3", NewestAt: now.Add(-3 * time.Hour)},
		{SessionKey: "s4", NewestAt: now.Add(-2 * time.Hour)},
		{SessionKey: "s5", NewestAt: now.Add(-1 * time.Hour)},
	}
	store := newFakeStore("s1", "s2", "s3", "s4", "s5")

	res := Run(context.Background(), eng, []Store{store}, config.SessionPruneConfig{
		Enabled:     true,
		MaxDBSizeMB: 2,
		Vacuum:      boolPtr(true),
	})

	// Size cap of 2 MB with 1 MB/session leaves at most 2 sessions.
	if len(eng.statuses) > 2 {
		t.Fatalf("expected <=2 sessions after DB size enforcement, got %d", len(eng.statuses))
	}
	if res.DBBytesAfter > 2*mb {
		t.Fatalf("DBBytesAfter = %d, want <= %d", res.DBBytesAfter, 2*mb)
	}
}

// TestRunDeletesFromOwningStore covers multi-agent setups: the seahorse DB is
// shared but every agent has its own session store, so a session must be
// removed from the store that owns it and other stores left untouched.
func TestRunDeletesFromOwningStore(t *testing.T) {
	now := time.Now()
	eng := newFakeEngine(1 << 20)
	eng.statuses = []seahorse.SessionStatus{
		{SessionKey: "main-old", NewestAt: now.Add(-100 * 24 * time.Hour)},
		{SessionKey: "other-old", NewestAt: now.Add(-90 * 24 * time.Hour)},
		{SessionKey: "other-new", NewestAt: now.Add(-time.Hour)},
	}
	mainStore := newFakeStore("main-old")
	otherStore := newFakeStore("other-old", "other-new")

	res := Run(context.Background(), eng, []Store{mainStore, otherStore}, config.SessionPruneConfig{
		Enabled:    true,
		MaxAgeDays: 30,
	})

	if len(res.Deleted) != 2 {
		t.Fatalf("Deleted = %v, want main-old and other-old", res.Deleted)
	}
	if !contains(mainStore.deleted, "main-old") {
		t.Fatalf("main store deleted = %v, want main-old", mainStore.deleted)
	}
	if !contains(otherStore.deleted, "other-old") {
		t.Fatalf("other store deleted = %v, want other-old", otherStore.deleted)
	}
	if contains(mainStore.deleted, "other-old") || contains(otherStore.deleted, "main-old") {
		t.Fatalf("session deleted from a store that does not own it: main=%v other=%v",
			mainStore.deleted, otherStore.deleted)
	}
	if got := otherStore.ListSessions(); !reflect.DeepEqual(got, []string{"other-new"}) {
		t.Fatalf("other store sessions = %v, want [other-new]", got)
	}
}

func TestRunIgnoresNilAndDuplicateStores(t *testing.T) {
	now := time.Now()
	eng := newFakeEngine(1 << 20)
	eng.statuses = []seahorse.SessionStatus{
		{SessionKey: "old", NewestAt: now.Add(-100 * 24 * time.Hour)},
		{SessionKey: "new", NewestAt: now.Add(-time.Hour)},
	}
	store := newFakeStore("old", "new")

	res := Run(context.Background(), eng, []Store{nil, store, store}, config.SessionPruneConfig{
		Enabled:    true,
		MaxAgeDays: 30,
	})
	if len(res.Deleted) != 1 || res.Deleted[0] != "old" {
		t.Fatalf("Deleted = %v, want [old]", res.Deleted)
	}
	if len(store.deleted) != 1 {
		t.Fatalf("duplicate store deleted %d times, want 1: %v", len(store.deleted), store.deleted)
	}
}

func TestRunNoopWhenDisabled(t *testing.T) {
	eng := newFakeEngine(1 << 20)
	eng.statuses = []seahorse.SessionStatus{{SessionKey: "a", NewestAt: time.Now().Add(-1000 * time.Hour)}}
	store := newFakeStore("a")

	res := Run(context.Background(), eng, []Store{store}, config.SessionPruneConfig{
		Enabled:    false,
		MaxAgeDays: 1,
	})
	if len(res.Deleted) != 0 || eng.vacuumCount != 0 {
		t.Fatalf("disabled pruning should be a no-op, got %+v", res)
	}
}

// --- fakes ---

type fakeEngine struct {
	perSessionBytes int64
	statuses        []seahorse.SessionStatus
	deleted         []string
	vacuumCount     int
	checkpointCount int
}

func newFakeEngine(perSessionBytes int64) *fakeEngine {
	return &fakeEngine{perSessionBytes: perSessionBytes}
}

func (f *fakeEngine) SessionStatuses(context.Context) ([]seahorse.SessionStatus, error) {
	out := make([]seahorse.SessionStatus, len(f.statuses))
	copy(out, f.statuses)
	return out, nil
}

func (f *fakeEngine) DeleteSession(_ context.Context, sessionKey string) error {
	for i, s := range f.statuses {
		if s.SessionKey == sessionKey {
			f.statuses = append(f.statuses[:i], f.statuses[i+1:]...)
			f.deleted = append(f.deleted, sessionKey)
			return nil
		}
	}
	return nil
}

func (f *fakeEngine) DBFileSize() (int64, error) {
	return int64(len(f.statuses)) * f.perSessionBytes, nil
}

func (f *fakeEngine) Vacuum(context.Context) error {
	f.vacuumCount++
	return nil
}

func (f *fakeEngine) Checkpoint(context.Context, bool) error {
	f.checkpointCount++
	return nil
}

type fakeStore struct {
	sessions []string
	deleted  []string
	activity map[string]time.Time
}

func (f *fakeStore) LastActivity(key string) time.Time {
	return f.activity[key]
}

func newFakeStore(keys ...string) *fakeStore {
	return &fakeStore{sessions: append([]string(nil), keys...)}
}

func (f *fakeStore) ListSessions() []string {
	out := make([]string, len(f.sessions))
	copy(out, f.sessions)
	return out
}

func (f *fakeStore) DeleteSession(key string) error {
	for i, k := range f.sessions {
		if k == key {
			f.sessions = append(f.sessions[:i], f.sessions[i+1:]...)
			break
		}
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func contains(list []string, val string) bool {
	sort.Strings(list)
	i := sort.SearchStrings(list, val)
	return i < len(list) && list[i] == val
}

func boolPtr(v bool) *bool {
	return &v
}
