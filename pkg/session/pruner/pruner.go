// Package pruner implements automatic pruning of old chat sessions.
//
// On startup the seahorse context manager bootstraps every session into a
// SQLite database (pkg/agent/context_seahorse.go). The cost of that bootstrap
// grows with the number of sessions and the size of their history, so a
// long-lived instance can accumulate hundreds of sessions and a large
// seahorse.db, making the gateway take minutes to become ready.
//
// This package selects and deletes the oldest sessions according to a
// configurable policy (age, session count and database size). Both the backing
// session store (JSONL files) and the seahorse database are cleaned up, then
// the database is vacuumed to reclaim disk space.
//
// Pruning is opt-in and never deletes the most recent sessions unless the
// configured thresholds explicitly require it.
package pruner

import (
	"context"
	"sort"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

// Engine is the subset of the seahorse engine used for pruning.
type Engine interface {
	SessionStatuses(ctx context.Context) ([]seahorse.SessionStatus, error)
	DeleteSession(ctx context.Context, sessionKey string) error
	DBFileSize() (int64, error)
	Vacuum(ctx context.Context) error
	Checkpoint(ctx context.Context, truncate bool) error
}

// Store is the subset of a session store used for pruning. Each agent owns its
// own store (workspace/sessions), so a prune pass receives every store and
// deletes a session from whichever stores list it.
type Store interface {
	ListSessions() []string
	DeleteSession(key string) error
}

// ActivityStore is optionally implemented by stores that know when a session
// was last written. The JSONL store is the canonical history, whereas the
// seahorse database is only an index that can lag behind it (crash, session
// not bootstrapped yet), so the store timestamp keeps recently active sessions
// from looking stale or undated.
type ActivityStore interface {
	// LastActivity returns the last time the session was written, or the zero
	// time when unknown.
	LastActivity(key string) time.Time
}

// Info describes a single session for pruning decisions.
type Info struct {
	Key      string
	NewestAt time.Time // newest message in the seahorse database
	OldestAt time.Time // oldest message in the seahorse database
	StoreAt  time.Time // last write recorded by the owning session store
	Messages int
	InDB     bool // the session has a conversation in the seahorse database
}

// lastActivity returns the most recent known activity timestamp for the
// session: the newest of the seahorse message time and the session store write
// time, falling back to the oldest seahorse message. A zero value means the
// session is undated; undated sessions are never selected for deletion.
func (i Info) lastActivity() time.Time {
	last := i.NewestAt
	if i.StoreAt.After(last) {
		last = i.StoreAt
	}
	if last.IsZero() {
		return i.OldestAt
	}
	return last
}

// Result reports what a prune pass did.
type Result struct {
	Deleted       []string
	DBBytesBefore int64
	DBBytesAfter  int64
}

// maxDBSizeIterations bounds the database-size enforcement loop.
const maxDBSizeIterations = 32

// Select returns the session keys that should be deleted for the age and
// session-count thresholds, ordered oldest-first. The database-size guard is
// handled separately by Run because it depends on the file size on disk.
//
// A session is selected when:
//   - its last activity is older than MaxAgeDays, or
//   - it is not among the MaxSessions most recently active sessions.
//
// A threshold of 0 disables the corresponding check. Sessions whose activity is
// unknown (undated) are never selected: they still count toward MaxSessions, but
// the sessions dropped to honor the cap are always the oldest dated ones, so a
// session is never deleted on the basis of a timestamp we do not have.
func Select(infos []Info, cfg config.SessionPruneConfig, now time.Time) []string {
	if len(infos) == 0 {
		return nil
	}

	dated := datedOldestFirst(infos)
	selected := make(map[string]struct{})

	if cfg.MaxSessions > 0 && len(infos) > cfg.MaxSessions {
		excess := len(infos) - cfg.MaxSessions
		if excess > len(dated) {
			excess = len(dated)
		}
		for _, info := range dated[:excess] {
			selected[info.Key] = struct{}{}
		}
	}

	if cfg.MaxAgeDays > 0 {
		cutoff := now.Add(-time.Duration(cfg.MaxAgeDays) * 24 * time.Hour)
		for _, info := range dated {
			if info.lastActivity().Before(cutoff) {
				selected[info.Key] = struct{}{}
			}
		}
	}

	if len(selected) == 0 {
		return nil
	}

	// Preserve oldest-first ordering in the output.
	out := make([]string, 0, len(selected))
	for _, info := range dated {
		if _, ok := selected[info.Key]; ok {
			out = append(out, info.Key)
		}
	}
	return out
}

// datedOldestFirst returns the sessions with a known activity time, ordered
// oldest-first (ties broken by key for determinism).
func datedOldestFirst(infos []Info) []Info {
	dated := make([]Info, 0, len(infos))
	for _, info := range infos {
		if !info.lastActivity().IsZero() {
			dated = append(dated, info)
		}
	}
	sort.SliceStable(dated, func(a, b int) bool {
		ta, tb := dated[a].lastActivity(), dated[b].lastActivity()
		if ta.Equal(tb) {
			return dated[a].Key < dated[b].Key
		}
		return ta.Before(tb)
	})
	return dated
}

// owners maps a session key to every store that lists it.
type owners map[string][]Store

// indexOwners builds the key -> stores index from the given stores. Nil stores
// and duplicate store instances are ignored.
func indexOwners(stores []Store) owners {
	idx := make(owners)
	seen := make(map[Store]struct{}, len(stores))
	for _, store := range stores {
		if store == nil {
			continue
		}
		if _, dup := seen[store]; dup {
			continue
		}
		seen[store] = struct{}{}
		for _, key := range store.ListSessions() {
			idx[key] = append(idx[key], store)
		}
	}
	return idx
}

// Run executes one prune pass: it applies the age and session-count thresholds,
// then enforces the database-size guard by deleting the oldest remaining
// sessions until the database fits (or nothing is left to delete). When Vacuum
// is enabled the database is vacuumed after deletions to reclaim space.
//
// The seahorse database is shared by every agent while each agent has its own
// session store, so stores must contain the store of every agent. A session is
// deleted from the seahorse database and from each store that lists it.
func Run(ctx context.Context, eng Engine, stores []Store, cfg config.SessionPruneConfig) Result {
	var result Result
	if !cfg.Enabled || eng == nil {
		return result
	}
	if !cfg.IsActive() {
		return result
	}

	result.DBBytesBefore, _ = eng.DBFileSize()

	idx := indexOwners(stores)
	infos := collectInfos(ctx, eng, idx)
	toDelete := Select(infos, cfg, time.Now())

	deleted := 0
	for _, key := range toDelete {
		if deleteSession(ctx, eng, idx, key) {
			result.Deleted = append(result.Deleted, key)
			deleted++
		}
	}

	if deleted > 0 {
		reclaim(ctx, eng, cfg)
	}

	if cfg.MaxDBSizeMB > 0 {
		deleted += enforceDBSize(ctx, eng, idx, infos, cfg, &result)
	}

	if deleted > 0 {
		reclaim(ctx, eng, cfg)
	}

	result.DBBytesAfter, _ = eng.DBFileSize()
	if deleted > 0 {
		logger.InfoCF("session-prune", "Pruned chat sessions", map[string]any{
			"deleted":         len(result.Deleted),
			"max_age_days":    cfg.MaxAgeDays,
			"max_sessions":    cfg.MaxSessions,
			"max_db_size_mb":  cfg.MaxDBSizeMB,
			"db_bytes_before": result.DBBytesBefore,
			"db_bytes_after":  result.DBBytesAfter,
		})
	}
	return result
}

// enforceDBSize deletes the oldest remaining sessions until the database is
// under MaxDBSizeMB. Sessions are ranked exactly like Select ranks them (newest
// activity wins, undated sessions are never touched). It estimates how many
// sessions to remove from the average session size, deletes them in a batch,
// then reclaims and re-measures. The loop stops as soon as a pass makes no
// progress, so a database that does not shrink (for example when Vacuum is
// disabled) never causes runaway deletion. Only sessions that live in the
// seahorse database are candidates, since JSONL-only sessions occupy no
// database space. Returns the number of sessions deleted.
func enforceDBSize(
	ctx context.Context,
	eng Engine,
	idx owners,
	infos []Info,
	cfg config.SessionPruneConfig,
	result *Result,
) int {
	limit := int64(cfg.MaxDBSizeMB) << 20 // MB -> bytes

	gone := make(map[string]struct{}, len(result.Deleted))
	for _, key := range result.Deleted {
		gone[key] = struct{}{}
	}
	inDB := make([]Info, 0, len(infos))
	for _, info := range infos {
		if _, deleted := gone[info.Key]; info.InDB && !deleted {
			inDB = append(inDB, info)
		}
	}
	candidates := datedOldestFirst(inDB)
	remaining := len(inDB)

	deleted := 0
	for iteration := 0; iteration < maxDBSizeIterations && len(candidates) > 0; iteration++ {
		size, err := eng.DBFileSize()
		if err != nil || size <= limit {
			break
		}

		// Estimate how many of the oldest sessions must go to close the gap.
		over := size - limit
		avg := size / int64(max(remaining, 1))
		if avg <= 0 {
			avg = 1
		}
		need := int((over + avg - 1) / avg)
		need = min(max(need, 1), len(candidates))

		removed := 0
		for _, info := range candidates[:need] {
			if ctx.Err() != nil {
				break
			}
			if !deleteSession(ctx, eng, idx, info.Key) {
				continue
			}
			result.Deleted = append(result.Deleted, info.Key)
			deleted++
			removed++
			remaining--
		}
		candidates = candidates[need:]
		if removed == 0 {
			continue
		}

		reclaim(ctx, eng, cfg)

		after, err := eng.DBFileSize()
		if err != nil || after >= size {
			break // no reclaim progress; stop rather than delete everything
		}
	}
	return deleted
}

func reclaim(ctx context.Context, eng Engine, cfg config.SessionPruneConfig) {
	if cfg.VacuumEnabled() {
		if err := eng.Vacuum(ctx); err != nil {
			logger.WarnCF("session-prune", "VACUUM failed", map[string]any{"error": err.Error()})
		}
		return
	}
	if err := eng.Checkpoint(ctx, true); err != nil {
		logger.WarnCF("session-prune", "WAL checkpoint failed", map[string]any{"error": err.Error()})
	}
}

func collectInfos(ctx context.Context, eng Engine, idx owners) []Info {
	statuses, err := eng.SessionStatuses(ctx)
	if err != nil {
		logger.WarnCF("session-prune", "Failed to list seahorse sessions", map[string]any{"error": err.Error()})
	}

	infos := make([]Info, 0, len(statuses))
	known := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		infos = append(infos, Info{
			Key:      status.SessionKey,
			NewestAt: status.NewestAt,
			OldestAt: status.OldestAt,
			StoreAt:  storeActivity(idx[status.SessionKey], status.SessionKey),
			Messages: status.Messages,
			InDB:     true,
		})
		known[status.SessionKey] = struct{}{}
	}

	// Include sessions that exist only as JSONL files (not yet bootstrapped
	// into seahorse). Their only timestamp is the one kept by the store.
	keys := make([]string, 0, len(idx))
	for key := range idx {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := known[key]; ok {
			continue
		}
		infos = append(infos, Info{Key: key, StoreAt: storeActivity(idx[key], key)})
	}
	return infos
}

// storeActivity returns the newest write time reported by the stores that own
// the session, or the zero time when none of them track activity.
func storeActivity(stores []Store, key string) time.Time {
	var last time.Time
	for _, store := range stores {
		as, ok := store.(ActivityStore)
		if !ok {
			continue
		}
		if at := as.LastActivity(key); at.After(last) {
			last = at
		}
	}
	return last
}

func deleteSession(ctx context.Context, eng Engine, idx owners, key string) bool {
	if key == "" {
		return false
	}
	ok := false
	if err := eng.DeleteSession(ctx, key); err != nil {
		logger.WarnCF("session-prune", "Failed to delete seahorse session", map[string]any{
			"session": key,
			"error":   err.Error(),
		})
	} else {
		ok = true
	}
	for _, store := range idx[key] {
		if err := store.DeleteSession(key); err != nil {
			logger.WarnCF("session-prune", "Failed to delete session store files", map[string]any{
				"session": key,
				"error":   err.Error(),
			})
		} else {
			ok = true
		}
	}
	return ok
}
