package memory

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/messageutil"
)

const (
	// numLockShards is the fixed number of mutexes used to serialize
	// per-session access. Using a sharded array instead of a map keeps
	// memory bounded regardless of how many sessions are created over
	// the lifetime of the process — important for a long-running daemon.
	numLockShards = 64

	// maxLineSize is the maximum size of a single JSON line in a .jsonl
	// file. Tool results (read_file, web search, etc.) can be large, so
	// we set a generous limit. The scanner starts at 64 KB and grows
	// only as needed up to this cap.
	maxLineSize = 10 * 1024 * 1024 // 10 MB

	// defaultGroupCommitBytes bounds the memory held by the write-behind
	// buffer before an automatic flush.
	defaultGroupCommitBytes = 256 * 1024
)

// SessionMeta holds per-session metadata stored in a .meta.json file.
//
// Scope is stored as raw JSON so pkg/memory can stay decoupled from the
// higher-level session package while still preserving structured scope data.
type SessionMeta struct {
	Key        string          `json:"key"`
	Summary    string          `json:"summary"`
	ForkedFrom string          `json:"forked_from,omitempty"`
	ForkIndex  int             `json:"fork_index,omitempty"`
	ForkTitle  string          `json:"fork_title,omitempty"`
	ForkDepth  int             `json:"fork_depth,omitempty"`
	Skip       int             `json:"skip"`
	Count      int             `json:"count"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Scope      json.RawMessage `json:"scope,omitempty"`
	Aliases    []string        `json:"aliases,omitempty"`
}

// sessionIndex caches canonical session keys and their aliases. The index is
// built lazily from the metadata files and kept in sync by metadata writes so
// ResolveSessionKey does not scan the whole sessions directory on every call.
type sessionIndex struct {
	mu        sync.RWMutex
	canonical map[string]string
	loaded    bool
}

func (ix *sessionIndex) lookup(key string) (string, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if !ix.loaded {
		return "", false
	}
	canonical, ok := ix.canonical[key]
	return canonical, ok
}

// store updates an already-built index after metadata for canonical changed.
// It removes stale aliases before inserting the new ones.
func (ix *sessionIndex) store(canonical string, aliases []string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if !ix.loaded {
		return
	}
	for key, value := range ix.canonical {
		if value == canonical {
			delete(ix.canonical, key)
		}
	}
	ix.canonical[canonical] = canonical
	for _, alias := range aliases {
		if alias != "" && alias != canonical {
			ix.canonical[alias] = canonical
		}
	}
}

func (ix *sessionIndex) remove(canonical string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if !ix.loaded {
		return
	}
	for key, value := range ix.canonical {
		if key == canonical || value == canonical {
			delete(ix.canonical, key)
		}
	}
}

func (ix *sessionIndex) invalidate() {
	ix.mu.Lock()
	ix.loaded = false
	ix.canonical = nil
	ix.mu.Unlock()
}

func (ix *sessionIndex) load(dir, key string) (string, bool, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.loaded {
		canonical, ok := ix.canonical[key]
		return canonical, ok, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, fmt.Errorf("memory: read sessions dir: %w", err)
	}

	metas := make([]SessionMeta, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}

		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			log.Printf("memory: skipping unreadable meta %s: %v", entry.Name(), readErr)
			continue
		}

		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			log.Printf("memory: skipping corrupt meta %s: %v", entry.Name(), err)
			continue
		}
		if meta.Key == "" {
			continue
		}
		metas = append(metas, meta)
	}

	canonical := make(map[string]string, len(metas))
	for _, meta := range metas {
		canonical[meta.Key] = meta.Key
	}
	// Aliases take precedence over direct keys so an explicit metadata
	// mapping wins over a legacy file with the same name.
	for _, meta := range metas {
		for _, alias := range meta.Aliases {
			if alias != "" && alias != meta.Key {
				canonical[alias] = meta.Key
			}
		}
	}

	ix.canonical = canonical
	ix.loaded = true

	resolved, ok := canonical[key]
	return resolved, ok, nil
}

// JSONLStore implements Store using append-only JSONL files.
//
// Each session is stored as two files:
//
//	{sanitized_key}.jsonl      — one JSON-encoded message per line, append-only
//	{sanitized_key}.meta.json  — session metadata (summary, logical truncation offset)
//
// Messages are never physically deleted from the JSONL file. Instead,
// TruncateHistory records a "skip" offset in the metadata file and
// GetHistory ignores lines before that offset. This keeps all writes
// append-only, which is both fast and crash-safe.
type JSONLStore struct {
	dir   string
	locks [numLockShards]sync.Mutex
	index sessionIndex

	// durableWrites keeps the original fsync-per-write durability. When
	// false, message appends skip fsync and metadata files are replaced
	// without fsync (still atomically, via temp + rename): files stay
	// consistent after a crash, but the most recent writes may be lost on
	// power failure. Intended for flash-constrained devices where the
	// per-message fsync cost and flash wear are not acceptable.
	durableWrites bool

	// groupCommit buffers message appends and flushes them with one
	// open/write/fsync per session. Reads and explicit Flush calls drain the
	// buffer, so read-after-write stays synchronous. The trade-off is that a
	// crash before a flush loses the buffered messages.
	groupCommit      bool
	groupCommitBytes int

	pendingMu      sync.Mutex
	pending        map[string][][]byte
	pendingCounts  map[string]int
	pendingCreated map[string]time.Time
	pendingUpdated map[string]time.Time
	// pendingMeta holds metadata deltas whose JSONL lines were already
	// appended but whose metadata write failed. They are retried by the next
	// flush so a failed metadata write cannot drop acknowledged messages.
	pendingMeta  map[string]metaDelta
	pendingBytes int
	flushMu      sync.Mutex
}

// StoreOption configures a JSONLStore created by NewJSONLStore.
type StoreOption func(*JSONLStore)

// WithDurableWrites controls whether writes are fsynced before they are
// acknowledged. It defaults to true; see JSONLStore.durableWrites for the
// trade-off when it is disabled.
func WithDurableWrites(durable bool) StoreOption {
	return func(s *JSONLStore) {
		s.durableWrites = durable
	}
}

// WithGroupCommit enables write-behind message appends. Messages are buffered
// and flushed with a single open/write/fsync per session when the buffer grows
// past 256 KB, when a read method is called, on Save, or on Close. This trades
// per-message durability for far fewer fsyncs; a crash before the next flush
// loses the buffered messages.
func WithGroupCommit(enabled bool) StoreOption {
	return func(s *JSONLStore) {
		s.groupCommit = enabled
	}
}

// NewJSONLStore creates a new JSONL-backed store rooted at dir.
func NewJSONLStore(dir string, opts ...StoreOption) (*JSONLStore, error) {
	err := os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("memory: create directory: %w", err)
	}
	s := &JSONLStore{
		dir:              dir,
		durableWrites:    true,
		groupCommitBytes: defaultGroupCommitBytes,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// sessionLock returns a mutex for the given session key.
// Keys are mapped to a fixed pool of shards via FNV hash, so
// memory usage is O(1) regardless of total session count.
func (s *JSONLStore) sessionLock(key string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(key))
	return &s.locks[h.Sum32()%numLockShards]
}

func (s *JSONLStore) jsonlPath(key string) string {
	return filepath.Join(s.dir, sanitizeKey(key)+".jsonl")
}

func (s *JSONLStore) metaPath(key string) string {
	return filepath.Join(s.dir, sanitizeKey(key)+".meta.json")
}

// sanitizeKey converts a session key to a safe filename component.
// Mirrors pkg/session.sanitizeFilename so that migration paths match.
// Replaces ':' with '_' (session key separator) and '/' and '\' with '_'
// so composite IDs (e.g. Telegram forum "chatID/threadID", Slack "channel/thread_ts")
// do not create subdirectories or break on Windows.
func sanitizeKey(key string) string {
	s := strings.ReplaceAll(key, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	return s
}

// readMeta loads the metadata file for a session.
// Returns a zero-value sessionMeta if the file does not exist.
func (s *JSONLStore) readMeta(key string) (SessionMeta, error) {
	data, err := os.ReadFile(s.metaPath(key))
	if os.IsNotExist(err) {
		return SessionMeta{Key: key}, nil
	}
	if err != nil {
		return SessionMeta{}, fmt.Errorf("memory: read meta: %w", err)
	}
	var meta SessionMeta
	err = json.Unmarshal(data, &meta)
	if err != nil {
		return SessionMeta{}, fmt.Errorf("memory: decode meta: %w", err)
	}
	if meta.Key == "" {
		meta.Key = key
	}
	return meta, nil
}

// writeMeta atomically writes the metadata file using the project's
// standard WriteFileAtomic (temp + fsync + rename). When durable writes are
// disabled the file is still replaced atomically, but without fsync.
func (s *JSONLStore) writeMeta(key string, meta SessionMeta) error {
	if strings.TrimSpace(meta.Key) == "" {
		meta.Key = key
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("memory: encode meta: %w", err)
	}
	if !s.durableWrites {
		return fileutil.WriteFileAtomicNoSync(s.metaPath(key), data, 0o644)
	}
	return fileutil.WriteFileAtomic(s.metaPath(key), data, 0o644)
}

func cloneRawJSON(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func normalizeAliases(canonicalKey string, aliases []string) []string {
	if len(aliases) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	canonicalKey = strings.TrimSpace(canonicalKey)
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == canonicalKey {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		normalized = append(normalized, alias)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (s *JSONLStore) sessionExists(key string) bool {
	if key == "" {
		return false
	}
	if _, err := os.Stat(s.jsonlPath(key)); err == nil {
		return true
	}
	if _, err := os.Stat(s.metaPath(key)); err == nil {
		return true
	}
	return false
}

// DeleteSession removes a session's JSONL data and metadata files. Missing
// files are ignored, so deleting an unknown session is a no-op. Used by
// automatic session pruning.
func (s *JSONLStore) DeleteSession(_ context.Context, sessionKey string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	var firstErr error
	for _, path := range []string{s.jsonlPath(sessionKey), s.metaPath(sessionKey)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = fmt.Errorf("memory: delete session file %s: %w", path, err)
		}
	}
	if firstErr == nil {
		s.index.remove(sessionKey)
	}
	return firstErr
}

// LastActivity returns when the session was last written: the newest of the
// metadata update time and the modification time of its files. It returns the
// zero time when the session is unknown. Used by automatic session pruning,
// where the JSONL store (not the seahorse index) is the source of truth.
func (s *JSONLStore) LastActivity(sessionKey string) time.Time {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return time.Time{}
	}
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		log.Printf("memory: flush before last activity: %v", err)
	}
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	if meta, err := s.readMeta(sessionKey); err == nil && !meta.UpdatedAt.IsZero() {
		return meta.UpdatedAt
	}
	var last time.Time
	for _, path := range []string{s.jsonlPath(sessionKey), s.metaPath(sessionKey)} {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last
}

// GetSessionMeta returns the current metadata snapshot for sessionKey.
func (s *JSONLStore) GetSessionMeta(_ context.Context, sessionKey string) (SessionMeta, error) {
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return SessionMeta{}, err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return SessionMeta{}, err
	}
	meta.Scope = cloneRawJSON(meta.Scope)
	if len(meta.Aliases) > 0 {
		meta.Aliases = append([]string(nil), meta.Aliases...)
	}
	return meta, nil
}

// UpsertSessionMeta stores structured session metadata while preserving
// summary/count/skip timestamps maintained by the core JSONL store.
func (s *JSONLStore) UpsertSessionMeta(
	_ context.Context,
	sessionKey string,
	scope json.RawMessage,
	aliases []string,
) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	normalizedAliases := normalizeAliases(sessionKey, aliases)
	if bytes.Equal(meta.Scope, scope) && slices.Equal(meta.Aliases, normalizedAliases) {
		return nil
	}
	meta.Scope = cloneRawJSON(scope)
	meta.Aliases = normalizedAliases
	now := time.Now()
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.UpdatedAt = now

	if err := s.writeMeta(sessionKey, meta); err != nil {
		return err
	}
	s.index.store(sessionKey, meta.Aliases)
	return nil
}

// PromoteAliasHistory atomically promotes the first non-empty alias session
// into the canonical session when the canonical session is still empty.
//
// Main-session aliases (e.g. "agent:main:main" or its opaque form) are
// skipped during promotion.  The main session is a shared global fallback
// and promoting its history into individual sessions would attach stale
// messages to every new Web UI session (issue #2972).
func (s *JSONLStore) PromoteAliasHistory(
	_ context.Context,
	sessionKey string,
	scope json.RawMessage,
	aliases []string,
) (bool, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false, nil
	}

	aliases = normalizeAliases(sessionKey, aliases)

	// Fast path for the common case: the canonical session already has
	// visible content, so promotion cannot do anything. This avoids reading
	// the canonical JSONL and repeating the check for every alias.
	hasContent, err := s.canonicalHasVisibleContent(sessionKey)
	if err != nil {
		return false, err
	}
	if hasContent {
		return false, nil
	}

	for _, alias := range aliases {
		if isMainSessionAlias(alias) {
			continue
		}
		unlock := s.lockSessionPair(sessionKey, alias)
		promoted, err := s.promoteAliasHistoryLocked(sessionKey, alias, scope, aliases)
		unlock()
		if promoted {
			// The canonical metadata and its aliases changed.
			s.index.invalidate()
		}
		if err != nil || promoted {
			return promoted, err
		}
	}

	return false, nil
}

// isMainSessionAlias reports whether alias is the legacy or opaque main-session
// key.  The main session ("agent:<agent>:main") is a shared fallback and should
// not have its history promoted into individual per-channel sessions.
func isMainSessionAlias(alias string) bool {
	if alias == "" {
		return false
	}
	// Legacy form: "agent:main:main" (exactly 3 colon-separated parts)
	// Must not match "agent:sales:direct:main" etc.
	if strings.HasPrefix(alias, "agent:") && strings.HasSuffix(alias, ":main") {
		parts := strings.SplitN(alias, ":", 4)
		if len(parts) == 3 {
			return true
		}
	}
	// Opaque form: "sk_v1_" + SHA256("agent:main:main")
	if strings.HasPrefix(alias, "sk_v1_") {
		for _, agentID := range []string{"main", "Main", "MAIN"} {
			legacy := "agent:" + agentID + ":main"
			hash := sha256.Sum256([]byte(legacy))
			if "sk_v1_"+hex.EncodeToString(hash[:]) == alias {
				return true
			}
		}
	}
	return false
}

// ResolveSessionKey returns the canonical session key for a candidate key.
// It short-circuits direct canonical keys when possible, then scans metadata
// once to resolve aliases or canonical metadata keys.
func (s *JSONLStore) ResolveSessionKey(_ context.Context, sessionKey string) (string, bool, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return "", false, nil
	}

	if canonical, ok := s.index.lookup(sessionKey); ok {
		return canonical, true, nil
	}

	canonical, found, err := s.index.load(s.dir, sessionKey)
	if err != nil {
		return "", false, err
	}
	if found {
		return canonical, true, nil
	}

	// Preserve direct sessions that predate metadata files. This also covers
	// keys created while the index was being built.
	if s.sessionExists(sessionKey) {
		s.index.store(sessionKey, nil)
		return sessionKey, true, nil
	}

	return "", false, nil
}

func (s *JSONLStore) lockSessionPair(keyA, keyB string) func() {
	lockA := s.sessionLock(keyA)
	lockB := s.sessionLock(keyB)
	if lockA == lockB {
		lockA.Lock()
		return func() { lockA.Unlock() }
	}
	if keyA <= keyB {
		lockA.Lock()
		lockB.Lock()
		return func() {
			lockB.Unlock()
			lockA.Unlock()
		}
	}
	lockB.Lock()
	lockA.Lock()
	return func() {
		lockA.Unlock()
		lockB.Unlock()
	}
}

func (s *JSONLStore) promoteAliasHistoryLocked(
	sessionKey string,
	alias string,
	scope json.RawMessage,
	aliases []string,
) (bool, error) {
	canonicalMeta, err := s.readMeta(sessionKey)
	if err != nil {
		return false, err
	}
	canonicalHasContent, err := s.canonicalMetaHasVisibleContent(sessionKey, canonicalMeta)
	if err != nil {
		return false, err
	}
	if canonicalHasContent {
		return false, nil
	}

	aliasMeta, err := s.readMeta(alias)
	if err != nil {
		return false, err
	}
	aliasHistory, err := readMessages(s.jsonlPath(alias), aliasMeta.Skip)
	if err != nil {
		return false, err
	}
	aliasSummary := strings.TrimSpace(aliasMeta.Summary)
	if len(aliasHistory) == 0 && aliasSummary == "" {
		return false, nil
	}

	previousJSONL, hadPreviousJSONL, err := s.readRawJSONL(sessionKey)
	if err != nil {
		return false, err
	}

	now := time.Now()
	if canonicalMeta.CreatedAt.IsZero() {
		canonicalMeta.CreatedAt = now
	}
	canonicalMeta.Scope = cloneRawJSON(scope)
	canonicalMeta.Aliases = normalizeAliases(sessionKey, aliases)
	canonicalMeta.ForkedFrom = aliasMeta.ForkedFrom
	canonicalMeta.ForkIndex = aliasMeta.ForkIndex
	canonicalMeta.ForkTitle = aliasMeta.ForkTitle
	canonicalMeta.ForkDepth = aliasMeta.ForkDepth
	canonicalMeta.Skip = 0
	canonicalMeta.Count = len(aliasHistory)
	canonicalMeta.UpdatedAt = now
	if aliasSummary != "" {
		canonicalMeta.Summary = aliasSummary
	}

	if err := s.rewriteJSONL(sessionKey, aliasHistory); err != nil {
		return false, err
	}
	if err := s.writeMeta(sessionKey, canonicalMeta); err != nil {
		if rollbackErr := s.restoreRawJSONL(sessionKey, previousJSONL, hadPreviousJSONL); rollbackErr != nil {
			return false, fmt.Errorf("memory: write promoted meta: %w (rollback jsonl: %v)", err, rollbackErr)
		}
		return false, err
	}
	return true, nil
}

func (s *JSONLStore) canonicalHasVisibleContent(sessionKey string) (bool, error) {
	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return false, err
	}
	return s.canonicalMetaHasVisibleContent(sessionKey, meta)
}

// canonicalMetaHasVisibleContent reports whether the canonical session has any
// visible content without reading the whole JSONL. Count/Skip cover sessions
// with current metadata; the file-size check keeps legacy metadata (where
// Count may be missing) safe.
func (s *JSONLStore) canonicalMetaHasVisibleContent(
	sessionKey string,
	meta SessionMeta,
) (bool, error) {
	if meta.Count > meta.Skip || strings.TrimSpace(meta.Summary) != "" {
		return true, nil
	}

	info, err := os.Stat(s.jsonlPath(sessionKey))
	if err == nil {
		return info.Size() > 0, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("memory: stat canonical jsonl: %w", err)
}

func (s *JSONLStore) readRawJSONL(sessionKey string) ([]byte, bool, error) {
	data, err := os.ReadFile(s.jsonlPath(sessionKey))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("memory: read jsonl: %w", err)
	}
	return data, true, nil
}

func (s *JSONLStore) restoreRawJSONL(sessionKey string, data []byte, existed bool) error {
	path := s.jsonlPath(sessionKey)
	if !existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("memory: remove jsonl rollback: %w", err)
		}
		return nil
	}
	if err := fileutil.WriteFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("memory: restore jsonl rollback: %w", err)
	}
	return nil
}

// readMessages reads valid JSON lines from a .jsonl file, skipping
// the first `skip` lines without unmarshaling them. This avoids the
// cost of json.Unmarshal on logically truncated messages.
// Malformed trailing lines (e.g. from a crash) are silently skipped.
func readMessages(path string, skip int) ([]providers.Message, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return []providers.Message{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: open jsonl: %w", err)
	}
	defer f.Close()

	var msgs []providers.Message
	scanner := bufio.NewScanner(f)
	// Allow large lines for tool results (read_file, web search, etc.).
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	lineNum := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineNum++
		if lineNum <= skip {
			continue
		}
		var msg providers.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			// Corrupt line — likely a partial write from a crash.
			// Log so operators know data was skipped, but don't
			// fail the entire read; this is the standard JSONL
			// recovery pattern.
			log.Printf("memory: skipping corrupt line %d in %s: %v",
				lineNum, filepath.Base(path), err)
			continue
		}
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		msgs = append(msgs, msg)
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("memory: scan jsonl: %w", scanner.Err())
	}

	if msgs == nil {
		msgs = []providers.Message{}
	}
	return msgs, nil
}

// scanRetainedMessageLines returns the total number of non-empty raw JSONL
// lines plus the raw line numbers that survive readMessages filtering.
// TruncateHistory uses this to compute keepLast against retained messages
// while preserving the raw-line skip offset stored in metadata.
func scanRetainedMessageLines(path string) (int, []int, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, []int{}, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("memory: open jsonl: %w", err)
	}
	defer f.Close()

	rawCount := 0
	retained := make([]int, 0)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		rawCount++

		var msg providers.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if messageutil.IsTransientAssistantThoughtMessage(msg) {
			continue
		}
		retained = append(retained, rawCount)
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, err
	}
	return rawCount, retained, nil
}

func (s *JSONLStore) AddMessage(
	_ context.Context, sessionKey, role, content string,
) error {
	return s.addMsg(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

func (s *JSONLStore) AddFullMessage(
	_ context.Context, sessionKey string, msg providers.Message,
) error {
	return s.addMsg(sessionKey, msg)
}

// addMsg is the shared implementation for AddMessage and AddFullMessage.
func (s *JSONLStore) addMsg(sessionKey string, msg providers.Message) error {
	if messageutil.IsTransientAssistantThoughtMessage(msg) {
		return nil
	}

	now := time.Now()

	if msg.CreatedAt == nil {
		msg.CreatedAt = &now
	}

	// Append the message as a single JSON line.
	line, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("memory: marshal message: %w", err)
	}
	line = append(line, '\n')

	if s.groupCommit {
		return s.enqueueMessage(sessionKey, line, now)
	}
	return s.appendMessage(sessionKey, line, now)
}

func (s *JSONLStore) appendMessage(sessionKey string, line []byte, now time.Time) error {
	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	f, err := os.OpenFile(
		s.jsonlPath(sessionKey),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		return fmt.Errorf("memory: open jsonl for append: %w", err)
	}
	_, writeErr := f.Write(line)
	if writeErr != nil {
		f.Close()
		return fmt.Errorf("memory: append message: %w", writeErr)
	}
	// Flush to physical storage before closing. This matches the
	// durability guarantee of writeMeta and rewriteJSONL (which use
	// WriteFileAtomic with fsync). Without Sync, a power loss could
	// leave the append in the kernel page cache only — lost on reboot.
	// The store drops the Sync when durable writes are disabled.
	if s.durableWrites {
		if syncErr := f.Sync(); syncErr != nil {
			f.Close()
			return fmt.Errorf("memory: sync jsonl: %w", syncErr)
		}
	}
	if closeErr := f.Close(); closeErr != nil {
		return fmt.Errorf("memory: close jsonl: %w", closeErr)
	}

	// Update metadata.
	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	if meta.Count == 0 && meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.Count++
	meta.UpdatedAt = now

	return s.writeMeta(sessionKey, meta)
}

func (s *JSONLStore) enqueueMessage(sessionKey string, line []byte, at time.Time) error {
	s.pendingMu.Lock()
	s.ensurePendingLocked()
	if _, ok := s.pendingCreated[sessionKey]; !ok {
		s.pendingCreated[sessionKey] = at
	}
	s.pending[sessionKey] = append(s.pending[sessionKey], line)
	s.pendingCounts[sessionKey]++
	s.pendingUpdated[sessionKey] = at
	s.pendingBytes += len(line)
	shouldFlush := s.pendingBytes >= s.groupCommitBytes
	s.pendingMu.Unlock()

	if shouldFlush {
		return s.Flush(context.Background())
	}
	return nil
}

type pendingBatch struct {
	key       string
	lines     [][]byte
	count     int
	bytes     int
	createdAt time.Time
	updatedAt time.Time
}

// metaDelta is the metadata change produced by a batch. It is kept separately
// when the JSONL append succeeded but the metadata write failed, so retrying
// the flush updates metadata without appending the same lines twice.
type metaDelta struct {
	count     int
	createdAt time.Time
	updatedAt time.Time
}

func (s *JSONLStore) ensurePendingLocked() {
	if s.pending == nil {
		s.pending = make(map[string][][]byte)
		s.pendingCounts = make(map[string]int)
		s.pendingCreated = make(map[string]time.Time)
		s.pendingUpdated = make(map[string]time.Time)
		s.pendingMeta = make(map[string]metaDelta)
	}
}

// Flush writes all buffered messages and their metadata. It is a no-op when
// group commit is disabled. A failure in one session does not drop or block
// the buffers of the other sessions: the failed lines stay queued for the next
// flush and the errors are aggregated. Readers that only need one session use
// FlushSession instead.
func (s *JSONLStore) Flush(_ context.Context) error {
	if !s.groupCommit {
		return nil
	}

	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.pendingMu.Lock()
	keys := make([]string, 0, len(s.pending)+len(s.pendingMeta))
	for key := range s.pending {
		keys = append(keys, key)
	}
	for key := range s.pendingMeta {
		if _, ok := s.pending[key]; !ok {
			keys = append(keys, key)
		}
	}
	s.pendingMu.Unlock()

	return s.flushKeysLocked(keys)
}

// FlushSession writes the buffered messages of one session. Reads call it so a
// session read neither blocks on nor fails because of unrelated sessions.
func (s *JSONLStore) FlushSession(_ context.Context, sessionKey string) error {
	if !s.groupCommit {
		return nil
	}

	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	return s.flushKeyLocked(sessionKey)
}

func (s *JSONLStore) flushKeysLocked(keys []string) error {
	var errs []error
	for _, key := range keys {
		if err := s.flushKeyLocked(key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// flushKeyLocked writes one session's buffered append and metadata. The caller
// holds flushMu, which serializes flushes for the whole store.
func (s *JSONLStore) flushKeyLocked(key string) error {
	batch, pendingMeta := s.takePendingLocked(key)
	if batch == nil && pendingMeta == nil {
		return nil
	}

	l := s.sessionLock(key)
	l.Lock()
	defer l.Unlock()

	if batch != nil {
		written, err := s.appendBatchLocked(*batch)
		if err != nil {
			if written {
				// The lines reached the file. Keep only the metadata delta
				// queued: retrying the append would duplicate messages.
				s.restoreMetaLocked(key, mergeMetaDelta(pendingMeta, metaDeltaFromBatch(*batch)))
			} else {
				s.restorePendingLocked(*batch)
				s.restoreMetaLocked(key, pendingMeta)
			}
			return err
		}
		pendingMeta = mergeMetaDelta(pendingMeta, metaDeltaFromBatch(*batch))
	}

	if err := s.updateMetaAfterBatchLocked(key, pendingMeta); err != nil {
		s.restoreMetaLocked(key, pendingMeta)
		return err
	}
	return nil
}

// takePendingLocked removes and returns the buffered append and any deferred
// metadata delta for key. Both are nil when the session has nothing queued.
func (s *JSONLStore) takePendingLocked(key string) (*pendingBatch, *metaDelta) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	var deferred *metaDelta
	if delta, ok := s.pendingMeta[key]; ok {
		delete(s.pendingMeta, key)
		deferred = &delta
	}

	lines, ok := s.pending[key]
	if !ok {
		return nil, deferred
	}

	batch := &pendingBatch{
		key:       key,
		lines:     lines,
		count:     s.pendingCounts[key],
		bytes:     totalLineBytes(lines),
		createdAt: s.pendingCreated[key],
		updatedAt: s.pendingUpdated[key],
	}
	delete(s.pending, key)
	delete(s.pendingCounts, key)
	delete(s.pendingCreated, key)
	delete(s.pendingUpdated, key)
	s.pendingBytes -= batch.bytes
	if s.pendingBytes < 0 {
		s.pendingBytes = 0
	}
	return batch, deferred
}

// restorePendingLocked puts a batch that was not written back in front of any
// lines enqueued after the failed flush, preserving append order.
func (s *JSONLStore) restorePendingLocked(batch pendingBatch) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.ensurePendingLocked()

	if newer, ok := s.pending[batch.key]; ok {
		merged := make([][]byte, 0, len(batch.lines)+len(newer))
		merged = append(merged, batch.lines...)
		merged = append(merged, newer...)
		s.pending[batch.key] = merged
	} else {
		s.pending[batch.key] = batch.lines
	}
	s.pendingCounts[batch.key] += batch.count
	if created, ok := s.pendingCreated[batch.key]; !ok || batch.createdAt.Before(created) {
		s.pendingCreated[batch.key] = batch.createdAt
	}
	if updated, ok := s.pendingUpdated[batch.key]; !ok || batch.updatedAt.After(updated) {
		s.pendingUpdated[batch.key] = batch.updatedAt
	}
	s.pendingBytes += batch.bytes
}

func (s *JSONLStore) restoreMetaLocked(key string, delta *metaDelta) {
	if delta == nil {
		return
	}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	s.ensurePendingLocked()

	if existing, ok := s.pendingMeta[key]; ok {
		s.pendingMeta[key] = *mergeMetaDelta(&existing, delta)
		return
	}
	s.pendingMeta[key] = *delta
}

// appendBatchLocked appends the batch lines. written reports whether the bytes
// were handed to the file: when true, appending them again would duplicate
// messages, so only metadata may be retried.
func (s *JSONLStore) appendBatchLocked(batch pendingBatch) (bool, error) {
	var buf bytes.Buffer
	buf.Grow(batch.bytes)
	for _, line := range batch.lines {
		buf.Write(line)
	}

	f, err := os.OpenFile(
		s.jsonlPath(batch.key),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
	if err != nil {
		return false, fmt.Errorf("memory: open jsonl for append: %w", err)
	}
	n, writeErr := f.Write(buf.Bytes())
	if writeErr != nil {
		f.Close()
		// A partial write may have appended a prefix, so retrying the batch
		// would duplicate or corrupt those lines. Report the bytes as written
		// and leave only the metadata delta queued.
		return n > 0, fmt.Errorf("memory: append message: %w", writeErr)
	}
	if s.durableWrites {
		if syncErr := f.Sync(); syncErr != nil {
			f.Close()
			return true, fmt.Errorf("memory: sync jsonl: %w", syncErr)
		}
	}
	if closeErr := f.Close(); closeErr != nil {
		return true, fmt.Errorf("memory: close jsonl: %w", closeErr)
	}
	return true, nil
}

func (s *JSONLStore) updateMetaAfterBatchLocked(key string, delta *metaDelta) error {
	if delta == nil || (delta.count == 0 && delta.createdAt.IsZero() && delta.updatedAt.IsZero()) {
		return nil
	}

	meta, err := s.readMeta(key)
	if err != nil {
		return err
	}
	if meta.Count == 0 && meta.CreatedAt.IsZero() && !delta.createdAt.IsZero() {
		meta.CreatedAt = delta.createdAt
	}
	meta.Count += delta.count
	if delta.updatedAt.After(meta.UpdatedAt) {
		meta.UpdatedAt = delta.updatedAt
	}
	return s.writeMeta(key, meta)
}

func metaDeltaFromBatch(batch pendingBatch) *metaDelta {
	return &metaDelta{
		count:     batch.count,
		createdAt: batch.createdAt,
		updatedAt: batch.updatedAt,
	}
}

func mergeMetaDelta(base, extra *metaDelta) *metaDelta {
	if base == nil {
		return extra
	}
	if extra == nil {
		return base
	}
	merged := *base
	merged.count += extra.count
	if merged.createdAt.IsZero() || (!extra.createdAt.IsZero() && extra.createdAt.Before(merged.createdAt)) {
		merged.createdAt = extra.createdAt
	}
	if extra.updatedAt.After(merged.updatedAt) {
		merged.updatedAt = extra.updatedAt
	}
	return &merged
}

func totalLineBytes(lines [][]byte) int {
	total := 0
	for _, line := range lines {
		total += len(line)
	}
	return total
}

func (s *JSONLStore) GetHistory(
	_ context.Context, sessionKey string,
) ([]providers.Message, error) {
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return nil, err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return nil, err
	}

	// Pass meta.Skip so readMessages skips those lines without
	// unmarshaling them — avoids wasted CPU on truncated messages.
	msgs, err := readMessages(s.jsonlPath(sessionKey), meta.Skip)
	if err != nil {
		return nil, err
	}

	return msgs, nil
}

func (s *JSONLStore) GetSummary(
	_ context.Context, sessionKey string,
) (string, error) {
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return "", err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return "", err
	}
	return meta.Summary, nil
}

func (s *JSONLStore) SetSummary(
	_ context.Context, sessionKey, summary string,
) error {
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	now := time.Now()
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.Summary = summary
	meta.UpdatedAt = now

	return s.writeMeta(sessionKey, meta)
}

func (s *JSONLStore) TruncateHistory(
	_ context.Context, sessionKey string, keepLast int,
) error {
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}

	rawCount, retainedRawLines, scanErr := scanRetainedMessageLines(s.jsonlPath(sessionKey))
	if scanErr != nil {
		return scanErr
	}
	meta.Count = rawCount
	if meta.Skip > meta.Count {
		meta.Skip = meta.Count
	}

	activeStart := sort.Search(len(retainedRawLines), func(i int) bool {
		return retainedRawLines[i] > meta.Skip
	})
	activeRetainedCount := len(retainedRawLines) - activeStart

	switch {
	case keepLast <= 0 || activeRetainedCount == 0:
		meta.Skip = meta.Count
	case keepLast < activeRetainedCount:
		activeRawLines := retainedRawLines[activeStart:]
		meta.Skip = activeRawLines[activeRetainedCount-keepLast-1]
	}
	meta.UpdatedAt = time.Now()

	return s.writeMeta(sessionKey, meta)
}

func (s *JSONLStore) SetHistory(
	_ context.Context,
	sessionKey string,
	history []providers.Message,
) error {
	history = messageutil.FilterInvalidHistoryMessages(history)

	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	now := time.Now()
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = now
	}
	meta.Skip = 0
	meta.Count = len(history)
	meta.UpdatedAt = now

	for i := range history {
		if history[i].CreatedAt == nil {
			history[i].CreatedAt = &now
		}
	}

	// Write meta BEFORE rewriting the JSONL file. If we crash between
	// the two writes, meta has Skip=0 and the old file is still intact,
	// so GetHistory reads from line 1 — returning "too many" messages
	// rather than losing data. The next SetHistory call corrects this.
	err = s.writeMeta(sessionKey, meta)
	if err != nil {
		return err
	}

	return s.rewriteJSONL(sessionKey, history)
}

// Compact physically rewrites the JSONL file, dropping all logically
// skipped lines. This reclaims disk space that accumulates after
// repeated TruncateHistory calls.
//
// It is safe to call at any time; if there is nothing to compact
// (skip == 0) the method returns immediately.
func (s *JSONLStore) Compact(
	_ context.Context, sessionKey string,
) error {
	if err := s.FlushSession(context.Background(), sessionKey); err != nil {
		return err
	}

	l := s.sessionLock(sessionKey)
	l.Lock()
	defer l.Unlock()

	meta, err := s.readMeta(sessionKey)
	if err != nil {
		return err
	}
	if meta.Skip == 0 {
		return nil
	}

	// Read only the active messages, skipping truncated lines
	// without unmarshaling them.
	active, err := readMessages(s.jsonlPath(sessionKey), meta.Skip)
	if err != nil {
		return err
	}

	// Write meta BEFORE rewriting the JSONL file. If the process
	// crashes between the two writes, meta has Skip=0 and the old
	// (uncompacted) file is still intact, so GetHistory reads from
	// line 1 — returning previously-truncated messages rather than
	// losing data. The next Compact or TruncateHistory corrects this.
	meta.Skip = 0
	meta.Count = len(active)
	meta.UpdatedAt = time.Now()

	err = s.writeMeta(sessionKey, meta)
	if err != nil {
		return err
	}

	return s.rewriteJSONL(sessionKey, active)
}

// rewriteJSONL atomically replaces the JSONL file with the given messages
// using the project's standard WriteFileAtomic (temp + fsync + rename).
func (s *JSONLStore) rewriteJSONL(
	sessionKey string, msgs []providers.Message,
) error {
	msgs = messageutil.FilterInvalidHistoryMessages(msgs)

	var buf bytes.Buffer
	for i, msg := range msgs {
		line, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("memory: marshal message %d: %w", i, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return fileutil.WriteFileAtomic(s.jsonlPath(sessionKey), buf.Bytes(), 0o644)
}

// ListSessions returns all known session keys by reading .meta.json files.
func (s *JSONLStore) ListSessions() []string {
	if err := s.Flush(context.Background()); err != nil {
		log.Printf("memory: flush before listing sessions: %v", err)
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	var keys []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		// Read the meta file to get the original key
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.Key != "" {
			keys = append(keys, meta.Key)
		}
	}
	return keys
}

func (s *JSONLStore) Close() error {
	return s.Flush(context.Background())
}
