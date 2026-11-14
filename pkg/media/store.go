package media

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// CleanupPolicy controls how the MediaStore treats the underlying file when
// a ref is released or expires.
type CleanupPolicy string

const (
	// CleanupPolicyDeleteOnCleanup means the file is store-managed and may be
	// deleted once the final ref for that path is gone.
	CleanupPolicyDeleteOnCleanup CleanupPolicy = "delete_on_cleanup"
	// CleanupPolicyForgetOnly means the store should only drop ref mappings and
	// must never delete the underlying file.
	CleanupPolicyForgetOnly CleanupPolicy = "forget_only"
)

// MediaMeta holds metadata about a stored media file.
type MediaMeta struct {
	Filename      string
	ContentType   string
	Source        string        // "telegram", "discord", "tool:image-gen", etc.
	CleanupPolicy CleanupPolicy // defaults to CleanupPolicyDeleteOnCleanup
}

// MediaStore manages the lifecycle of media files associated with processing scopes.
type MediaStore interface {
	// Store registers an existing local file under the given scope.
	// Returns a ref identifier (e.g. "media://<id>").
	// Store does not move or copy the file; it only records the mapping.
	// If meta.CleanupPolicy is empty, CleanupPolicyDeleteOnCleanup is assumed.
	Store(localPath string, meta MediaMeta, scope string) (ref string, err error)

	// Resolve returns the local file path for a given ref.
	Resolve(ref string) (localPath string, err error)

	// ResolveWithMeta returns the local file path and metadata for a given ref.
	ResolveWithMeta(ref string) (localPath string, meta MediaMeta, err error)

	// ReleaseAll deletes all files registered under the given scope
	// and removes the mapping entries. File-not-exist errors are ignored.
	ReleaseAll(scope string) error
}

// mediaEntry holds the path and metadata for a stored media file.
type mediaEntry struct {
	path     string
	meta     MediaMeta
	storedAt time.Time
	// lastAccess makes expiration idle-based; explicit pins protect active readers.
	lastAccess time.Time
}

type pathRefState struct {
	refCount       int
	deleteEligible bool
}

// MediaCleanerConfig configures the background TTL cleanup.
type MediaCleanerConfig struct {
	Enabled  bool
	MaxAge   time.Duration
	Interval time.Duration
}

// FileMediaStore is a pure in-memory implementation of MediaStore.
// Files are expected to already exist on disk, normally in TempDir().
type FileMediaStore struct {
	mu             sync.RWMutex
	refs           map[string]mediaEntry
	scopeToRefs    map[string]map[string]struct{}
	refToScope     map[string]string
	refToPath      map[string]string
	pathStates     map[string]pathRefState
	pathPins       map[string]int
	pendingDeletes map[string]struct{}
	removeFile     func(string) error

	cleanerCfg MediaCleanerConfig
	stop       chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
	nowFunc    func() time.Time // for testing
}

// NewFileMediaStore creates a new FileMediaStore without background cleanup.
func NewFileMediaStore() *FileMediaStore {
	return &FileMediaStore{
		refs:           make(map[string]mediaEntry),
		scopeToRefs:    make(map[string]map[string]struct{}),
		refToScope:     make(map[string]string),
		refToPath:      make(map[string]string),
		pathStates:     make(map[string]pathRefState),
		pathPins:       make(map[string]int),
		pendingDeletes: make(map[string]struct{}),
		removeFile:     os.Remove,
		nowFunc:        time.Now,
	}
}

// NewFileMediaStoreWithCleanup creates a FileMediaStore with TTL-based background cleanup.
func NewFileMediaStoreWithCleanup(cfg MediaCleanerConfig) *FileMediaStore {
	return &FileMediaStore{
		refs:           make(map[string]mediaEntry),
		scopeToRefs:    make(map[string]map[string]struct{}),
		refToScope:     make(map[string]string),
		refToPath:      make(map[string]string),
		pathStates:     make(map[string]pathRefState),
		pathPins:       make(map[string]int),
		pendingDeletes: make(map[string]struct{}),
		removeFile:     os.Remove,
		cleanerCfg:     cfg,
		stop:           make(chan struct{}),
		nowFunc:        time.Now,
	}
}

// Store registers a local file under the given scope. The file must exist.
func (s *FileMediaStore) Store(localPath string, meta MediaMeta, scope string) (string, error) {
	return s.store(localPath, meta, scope, false)
}

// StoreReadRef borrows external files but preserves ownership of existing
// managed copies, so loading an attachment does not disable its cleanup.
func StoreReadRef(store MediaStore, localPath string, meta MediaMeta, scope string) (string, error) {
	meta.CleanupPolicy = CleanupPolicyForgetOnly
	if s, ok := store.(*FileMediaStore); ok {
		return s.store(localPath, meta, scope, true)
	}
	return store.Store(localPath, meta, scope)
}

func (s *FileMediaStore) store(localPath string, meta MediaMeta, scope string, preserveOwnership bool) (string, error) {
	ref := "media://" + uuid.New().String()
	normalized, err := normalizeCleanupPolicy(meta.CleanupPolicy)
	if err != nil {
		return "", err
	}
	meta.CleanupPolicy = normalized

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check while holding the lifecycle lock so registration cannot race deletion.
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("media store: %s: %w", localPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("media store: %s is not a regular file", localPath)
	}
	if preserveOwnership && s.pathStates[localPath].deleteEligible {
		meta.CleanupPolicy = CleanupPolicyDeleteOnCleanup
	}
	delete(s.pendingDeletes, localPath)
	now := s.nowFunc()
	s.refs[ref] = mediaEntry{path: localPath, meta: meta, storedAt: now, lastAccess: now}
	if s.scopeToRefs[scope] == nil {
		s.scopeToRefs[scope] = make(map[string]struct{})
	}
	s.scopeToRefs[scope][ref] = struct{}{}
	s.refToScope[ref] = scope
	s.refToPath[ref] = localPath

	pathState := s.pathStates[localPath]
	if pathState.refCount == 0 {
		pathState.deleteEligible = meta.CleanupPolicy == CleanupPolicyDeleteOnCleanup
	} else if meta.CleanupPolicy == CleanupPolicyForgetOnly {
		// Be conservative: once a path is borrowed externally, never let this
		// lifecycle auto-delete it even if store-managed refs also exist.
		pathState.deleteEligible = false
	}
	pathState.refCount++
	s.pathStates[localPath] = pathState

	return ref, nil
}

// Resolve returns the local path for the given ref.
func (s *FileMediaStore) Resolve(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.refs[ref]
	if !ok {
		return "", fmt.Errorf("media store: unknown ref: %s", ref)
	}
	entry.lastAccess = s.nowFunc()
	s.refs[ref] = entry
	return entry.path, nil
}

// ResolveWithMeta returns the local path and metadata for the given ref.
func (s *FileMediaStore) ResolveWithMeta(ref string) (string, MediaMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.refs[ref]
	if !ok {
		return "", MediaMeta{}, fmt.Errorf("media store: unknown ref: %s", ref)
	}
	entry.lastAccess = s.nowFunc()
	s.refs[ref] = entry
	return entry.path, entry.meta, nil
}

// ReleaseAll drops a scope and deletes owned files after their final reference
// and reader pin are gone. Failed deletions remain queued for the next cleanup.
func (s *FileMediaStore) ReleaseAll(scope string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ref := range s.scopeToRefs[scope] {
		entry := s.refs[ref]
		if path, remove := s.releaseRefLocked(ref, entry.path); remove {
			s.pendingDeletes[path] = struct{}{}
		}
	}
	delete(s.scopeToRefs, scope)
	return s.retryDeletesLocked()
}

// CleanExpired expires idle, unpinned entries and retries failed deletions.
// Registration and deletion share the lock so a new reference cannot be
// registered between the final-reference check and removal of its file.
func (s *FileMediaStore) CleanExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	if s.cleanerCfg.MaxAge > 0 {
		cutoff := s.nowFunc().Add(-s.cleanerCfg.MaxAge)
		for ref, entry := range s.refs {
			if !entry.lastAccess.Before(cutoff) || s.pathPins[entry.path] > 0 {
				continue
			}
			if scope, ok := s.refToScope[ref]; ok {
				delete(s.scopeToRefs[scope], ref)
				if len(s.scopeToRefs[scope]) == 0 {
					delete(s.scopeToRefs, scope)
				}
			}
			if path, remove := s.releaseRefLocked(ref, entry.path); remove {
				s.pendingDeletes[path] = struct{}{}
			}
			removed++
		}
	}
	_ = s.retryDeletesLocked()
	return removed
}

func (s *FileMediaStore) retryDeletesLocked() error {
	var failures []error
	for path := range s.pendingDeletes {
		if s.pathPins[path] > 0 || s.pathStates[path].refCount > 0 {
			continue
		}
		if err := s.removeFile(path); err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Errorf("remove media %s: %w", path, err))
			logger.WarnCF("media", "Failed to remove media; will retry", map[string]any{"path": path, "error": err.Error()})
			continue
		}
		delete(s.pendingDeletes, path)
	}
	return errors.Join(failures...)
}

func normalizeCleanupPolicy(policy CleanupPolicy) (CleanupPolicy, error) {
	switch policy {
	case "", CleanupPolicyDeleteOnCleanup:
		return CleanupPolicyDeleteOnCleanup, nil
	case CleanupPolicyForgetOnly:
		return CleanupPolicyForgetOnly, nil
	default:
		return "", fmt.Errorf("media store: invalid cleanup policy: %q", policy)
	}
}

func (s *FileMediaStore) releaseRefLocked(ref, fallbackPath string) (string, bool) {
	path := fallbackPath
	if storedPath, ok := s.refToPath[ref]; ok {
		path = storedPath
		delete(s.refToPath, ref)
	}

	delete(s.refs, ref)
	delete(s.refToScope, ref)

	if path == "" {
		return "", false
	}

	pathState, ok := s.pathStates[path]
	if !ok {
		return "", false
	}
	if pathState.refCount <= 1 {
		delete(s.pathStates, path)
		return path, pathState.deleteEligible
	}

	pathState.refCount--
	s.pathStates[path] = pathState
	return "", false
}

// Start begins the background cleanup goroutine if cleanup is enabled.
// Safe to call multiple times; only the first call starts the goroutine.
func (s *FileMediaStore) Start() {
	if !s.cleanerCfg.Enabled || s.stop == nil {
		return
	}
	if s.cleanerCfg.Interval <= 0 || s.cleanerCfg.MaxAge <= 0 {
		logger.WarnCF("media", "cleanup: skipped due to invalid config", map[string]any{
			"interval": s.cleanerCfg.Interval.String(),
			"max_age":  s.cleanerCfg.MaxAge.String(),
		})
		return
	}

	s.startOnce.Do(func() {
		logger.InfoCF("media", "cleanup enabled", map[string]any{
			"interval": s.cleanerCfg.Interval.String(),
			"max_age":  s.cleanerCfg.MaxAge.String(),
		})

		go func() {
			ticker := time.NewTicker(s.cleanerCfg.Interval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					if n := s.CleanExpired(); n > 0 {
						logger.InfoCF("media", "cleanup: removed expired entries", map[string]any{
							"count": n,
						})
					}
				case <-s.stop:
					return
				}
			}
		}()
	})
}

// Stop terminates the background cleanup goroutine.
// Safe to call multiple times; only the first call closes the channel.
func (s *FileMediaStore) Stop() {
	if s.stop == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
	})
}
