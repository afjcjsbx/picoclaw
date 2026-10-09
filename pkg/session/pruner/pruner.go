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

// Store is the subset of the session store used for pruning.
type Store interface {
	ListSessions() []string
	DeleteSession(key string) error
}

// Info describes a single session for pruning decisions.
type Info struct {
	Key      string
	NewestAt time.Time
	OldestAt time.Time
	Messages int
}

// lastActivity returns the most recent known activity timestamp for the
// session, falling back to the oldest message. A zero value means the session
// has no recorded activity and is treated as the oldest.
func (i Info) lastActivity() time.Time {
	if !i.NewestAt.IsZero() {
		return i.NewestAt
	}
	return i.OldestAt
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
// A threshold of 0 disables the corresponding check. Sessions with unknown
// timestamps are only selected by the session-count check.
func Select(infos []Info, cfg config.SessionPruneConfig, now time.Time) []string {
	if len(infos) == 0 {
		return nil
	}

	// Sort oldest-first so the count cap drops the least recent sessions.
	sorted := make([]Info, len(infos))
	copy(sorted, infos)
	sort.SliceStable(sorted, func(a, b int) bool {
		ta, tb := sorted[a].lastActivity(), sorted[b].lastActivity()
		if ta.Equal(tb) {
			return sorted[a].Key < sorted[b].Key
		}
		// Zero timestamps sort first (oldest).
		if ta.IsZero() {
			return true
		}
		if tb.IsZero() {
			return false
		}
		return ta.Before(tb)
	})

	selected := make(map[string]struct{})

	if cfg.MaxSessions > 0 && len(sorted) > cfg.MaxSessions {
		for _, info := range sorted[:len(sorted)-cfg.MaxSessions] {
			selected[info.Key] = struct{}{}
		}
	}

	if cfg.MaxAgeDays > 0 {
		cutoff := now.Add(-time.Duration(cfg.MaxAgeDays) * 24 * time.Hour)
		for _, info := range sorted {
			last := info.lastActivity()
			if last.IsZero() {
				continue // no activity to compare, leave age-based deletion alone
			}
			if last.Before(cutoff) {
				selected[info.Key] = struct{}{}
			}
		}
	}

	if len(selected) == 0 {
		return nil
	}

	// Preserve oldest-first ordering in the output.
	out := make([]string, 0, len(selected))
	for _, info := range sorted {
		if _, ok := selected[info.Key]; ok {
			out = append(out, info.Key)
		}
	}
	return out
}

// Run executes one prune pass: it applies the age and session-count thresholds,
// then enforces the database-size guard by deleting the oldest remaining
// sessions until the database fits (or nothing is left to delete). When Vacuum
// is enabled the database is vacuumed after deletions to reclaim space.
func Run(ctx context.Context, eng Engine, store Store, cfg config.SessionPruneConfig) Result {
	var result Result
	if !cfg.Enabled || eng == nil {
		return result
	}
	if !cfg.IsActive() {
		return result
	}

	result.DBBytesBefore, _ = eng.DBFileSize()

	infos := collectInfos(ctx, eng, store)
	toDelete := Select(infos, cfg, time.Now())

	deleted := 0
	for _, key := range toDelete {
		if deleteSession(ctx, eng, store, key) {
			result.Deleted = append(result.Deleted, key)
			deleted++
		}
	}

	if deleted > 0 {
		reclaim(ctx, eng, cfg)
	}

	if cfg.MaxDBSizeMB > 0 {
		deleted += enforceDBSize(ctx, eng, store, cfg, &result)
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
// under MaxDBSizeMB. It estimates how many sessions to remove from the average
// session size, deletes them in a batch, then reclaims and re-measures. The
// loop stops as soon as a pass makes no progress, so a database that does not
// shrink (for example when Vacuum is disabled) never causes runaway deletion.
// Returns the number of sessions deleted.
func enforceDBSize(
	ctx context.Context,
	eng Engine,
	store Store,
	cfg config.SessionPruneConfig,
	result *Result,
) int {
	limit := int64(cfg.MaxDBSizeMB) << 20 // MB -> bytes
	deleted := 0

	for iteration := 0; iteration < maxDBSizeIterations; iteration++ {
		size, err := eng.DBFileSize()
		if err != nil || size <= limit {
			break
		}

		statuses, err := eng.SessionStatuses(ctx)
		if err != nil || len(statuses) == 0 {
			break
		}

		// Estimate how many of the oldest sessions must go to close the gap.
		over := size - limit
		avg := size / int64(len(statuses))
		if avg <= 0 {
			avg = 1
		}
		need := int((over + avg - 1) / avg)
		if need < 1 {
			need = 1
		}
		if need > len(statuses) {
			need = len(statuses)
		}

		removed := 0
		for removed < need {
			oldest := oldestSessionKey(statuses)
			if oldest == "" {
				break
			}
			if !deleteSession(ctx, eng, store, oldest) {
				break
			}
			result.Deleted = append(result.Deleted, oldest)
			deleted++
			removed++
			statuses = withoutKey(statuses, oldest)
		}
		if removed == 0 {
			break
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

func collectInfos(ctx context.Context, eng Engine, store Store) []Info {
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
			Messages: status.Messages,
		})
		known[status.SessionKey] = struct{}{}
	}

	// Include sessions that exist only as JSONL files (not yet bootstrapped
	// into seahorse). Their timestamps are unknown, so the count cap treats
	// them as oldest; age-based deletion leaves them untouched.
	if store != nil {
		for _, key := range store.ListSessions() {
			if _, ok := known[key]; ok {
				continue
			}
			infos = append(infos, Info{Key: key})
		}
	}
	return infos
}

func withoutKey(statuses []seahorse.SessionStatus, key string) []seahorse.SessionStatus {
	out := statuses[:0]
	for _, status := range statuses {
		if status.SessionKey != key {
			out = append(out, status)
		}
	}
	return out
}

func oldestSessionKey(statuses []seahorse.SessionStatus) string {
	if len(statuses) == 0 {
		return ""
	}
	oldest := statuses[0]
	oldestTs := oldest.NewestAt
	if oldestTs.IsZero() {
		oldestTs = oldest.OldestAt
	}
	for _, status := range statuses[1:] {
		ts := status.NewestAt
		if ts.IsZero() {
			ts = status.OldestAt
		}
		if ts.IsZero() {
			return status.SessionKey
		}
		if oldestTs.IsZero() || ts.Before(oldestTs) {
			oldest = status
			oldestTs = ts
		}
	}
	return oldest.SessionKey
}

func deleteSession(ctx context.Context, eng Engine, store Store, key string) bool {
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
	if store != nil {
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
