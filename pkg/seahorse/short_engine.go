package seahorse

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/textutil"
)

// Config holds engine configuration.
type Config struct {
	DBPath                   string   `json:"dbPath"`
	IgnoreSessionPatterns    []string `json:"ignoreSessionPatterns,omitempty"`
	StatelessSessionPatterns []string `json:"statelessSessionPatterns,omitempty"`
}

// CompleteFn is the LLM completion function type.
type CompleteFn func(ctx context.Context, prompt string, opts CompleteOptions) (string, error)

// CompleteOptions holds LLM completion parameters.
type CompleteOptions struct {
	Model       string
	MaxTokens   int
	Temperature float64
}

// IngestResult is the result of message ingestion.
type IngestResult struct {
	MessageCount int `json:"messageCount"`
	TokenCount   int `json:"tokenCount"`
}

// AssembleInput controls context assembly.
type AssembleInput struct {
	Budget int    `json:"budget"`
	Query  string `json:"query,omitempty"`
}

// AssembleResult contains assembled context.
type AssembleResult struct {
	Messages []Message `json:"messages"`
	Summary  string    `json:"summary"` // formatted XML summaries + system prompt addition
	// Token accounting for diagnostics and compaction decisions.
	FreshTailTokens int `json:"freshTailTokens"`
	SummaryTokens   int `json:"summaryTokens"`
	TokensUsed      int `json:"tokensUsed"`
	TokensEvicted   int `json:"tokensEvicted"`
	GapCount        int `json:"gapCount"`
}

// Engine is the main short-term memory engine.
type Engine struct {
	store             *Store
	compaction        *CompactionEngine
	compactionMu      sync.Mutex
	assembler         *Assembler
	assemblerMu       sync.Mutex
	retrieval         *RetrievalEngine
	config            Config
	complete          CompleteFn
	ignorePatterns    []*regexp.Regexp
	statelessPatterns []*regexp.Regexp

	locksOnce         sync.Once
	conversationLocks *conversationLocks
	releaseProcess    func() error
}

// CompactionEngine handles LLM-based summarization (defined in short_compaction.go).
type CompactionEngine struct {
	store          *Store
	config         Config
	complete       CompleteFn
	condensing     sync.Map // map[int64]struct{} — dedup for async condensed goroutines
	locks          *conversationLocks
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
}

// Assembler handles budget-aware context assembly (defined in short_assembler.go).
type Assembler struct {
	store  *Store
	config Config
}

// RetrievalEngine handles search and expansion (defined in short_retrieval.go).
type RetrievalEngine struct {
	store  *Store
	config Config
}

// Store returns the underlying store for direct access.
func (r *RetrievalEngine) Store() *Store {
	return r.store
}

// sqliteBusyTimeoutMS is how long a connection waits for a lock before failing
// with SQLITE_BUSY.
const sqliteBusyTimeoutMS = 5000

const (
	// sqliteMaxOpenConns bounds the connection pool. The pure-Go SQLite driver
	// keeps a page cache per connection, so an unbounded pool lets memory grow
	// with concurrent load. SQLite serializes writers, and in WAL mode a reader
	// does not block the writer, so two connections let one writer run next to
	// one reader (e.g. compaction alongside context assembly). Further
	// concurrent callers queue for a free connection, bounded by their context,
	// instead of opening another page cache, which keeps the memory footprint
	// predictable on constrained devices.
	sqliteMaxOpenConns = 2

	// sqliteTempStoreFile tells SQLite to spill temporary tables and sort
	// buffers to temporary files instead of the Go heap (temp_store = FILE),
	// which bounds the peak heap used by large compaction/sort queries. It
	// does not reduce total memory where the temp directory is RAM-backed
	// (tmpfs): the spill then uses kernel memory instead of the heap.
	sqliteTempStoreFile = 1
)

// sqliteDSN returns the driver DSN for path with the per-connection settings
// every pooled connection must have. database/sql opens connections lazily and
// recycles them, so running "PRAGMA busy_timeout" once via db.Exec only
// configures a single connection and leaves the others failing immediately with
// SQLITE_BUSY while a long write (such as a pruning VACUUM) holds the lock.
func sqliteDSN(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return fmt.Sprintf(
		"%s%s_busy_timeout=%d&_txlock=immediate&_pragma=synchronous(NORMAL)&_pragma=temp_store(%d)"+
			"&_pragma=foreign_keys(1)&_pragma=journal_size_limit(67108864)&_pragma=wal_autocheckpoint(1000)",
		path, sep, sqliteBusyTimeoutMS, sqliteTempStoreFile,
	)
}

// NewEngine creates a new short-term memory engine.
func NewEngine(config Config, completeFn CompleteFn) (*Engine, error) {
	dir := filepath.Dir(config.DBPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	releaseProcess, err := acquireProcessLock(config.DBPath)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", sqliteDSN(config.DBPath))
	if err != nil {
		_ = releaseProcess()
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Bound the pool so the per-connection page cache cannot grow unchecked on
	// memory-constrained devices (see sqliteMaxOpenConns). Idle is tied to the
	// cap rather than left at database/sql's default of 2, so changing the cap
	// never makes the pool close and reopen connections (and re-run their
	// pragmas) on every release.
	db.SetMaxOpenConns(sqliteMaxOpenConns)
	db.SetMaxIdleConns(sqliteMaxOpenConns)

	if err := backupBeforeMigration(db, config.DBPath); err != nil {
		_ = db.Close()
		_ = releaseProcess()
		return nil, fmt.Errorf("backup before migration: %w", err)
	}

	// journal_mode is stored in the database file, so setting it once is
	// enough. busy_timeout and synchronous are per-connection settings and
	// are applied to every pooled connection through the DSN (see sqliteDSN).
	if _, err := db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		_ = db.Close()
		_ = releaseProcess()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	if err := runSchema(db); err != nil {
		_ = db.Close()
		_ = releaseProcess()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	store := &Store{db: db}
	if err := store.GCOrphans(context.Background()); err != nil {
		_ = db.Close()
		_ = releaseProcess()
		return nil, fmt.Errorf("orphan cleanup: %w", err)
	}

	// Prepend hardcoded ignore patterns (spec lines 1326-1328)
	ignorePatterns := make([]string, 0, 1+len(config.IgnoreSessionPatterns))
	ignorePatterns = append(ignorePatterns, "heartbeat")
	ignorePatterns = append(ignorePatterns, config.IgnoreSessionPatterns...)

	retrieval := &RetrievalEngine{store: store, config: config}

	return &Engine{
		store:             store,
		compaction:        nil,
		assembler:         nil,
		retrieval:         retrieval,
		config:            config,
		complete:          completeFn,
		ignorePatterns:    compileSessionPatterns(ignorePatterns),
		statelessPatterns: compileSessionPatterns(config.StatelessSessionPatterns),
		releaseProcess:    releaseProcess,
	}, nil
}

// compileSessionPattern converts a glob pattern to a compiled regex.
// Pattern rules:
//   - *  matches any sequence of non-colon characters ([^:]*)
//   - ** matches any sequence of characters including colons (.*)
//   - All other characters are treated literally
//   - Pattern is anchored (^...$)
func compileSessionPattern(pattern string) *regexp.Regexp {
	var b strings.Builder
	b.WriteByte('^')

	i := 0
	for i < len(pattern) {
		if i+1 < len(pattern) && pattern[i] == '*' && pattern[i+1] == '*' {
			b.WriteString(".*")
			i += 2
			continue
		}
		if pattern[i] == '*' {
			b.WriteString("[^:]*")
			i++
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		i++
	}

	b.WriteByte('$')
	return regexp.MustCompile(b.String())
}

// compileSessionPatterns compiles multiple glob patterns into regex patterns.
func compileSessionPatterns(patterns []string) []*regexp.Regexp {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		result = append(result, compileSessionPattern(p))
	}
	return result
}

// shouldIgnoreSession returns true if the session key matches any ignore pattern.
func (e *Engine) shouldIgnoreSession(sessionKey string) bool {
	for _, p := range e.ignorePatterns {
		if p.MatchString(sessionKey) {
			return true
		}
	}
	return false
}

// isStatelessSession returns true if the session key matches any stateless pattern.
func (e *Engine) isStatelessSession(sessionKey string) bool {
	for _, p := range e.statelessPatterns {
		if p.MatchString(sessionKey) {
			return true
		}
	}
	return false
}

// Ingest adds messages to a conversation identified by sessionKey.
func (e *Engine) Ingest(
	ctx context.Context,
	sessionKey string,
	messages []Message,
) (*IngestResult, error) {
	if e.shouldIgnoreSession(sessionKey) {
		return nil, nil
	}
	if e.isStatelessSession(sessionKey) {
		return nil, nil
	}

	conv, err := e.store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	unlock := e.lockConversation(conv.ConversationID)
	defer unlock()
	return e.ingestConversation(ctx, conv, messages)
}

// ingestConversation performs the write while the caller holds the
// conversation lock. Bootstrap uses it to append a delta without recursively
// taking the non-reentrant lock.
func (e *Engine) ingestConversation(
	ctx context.Context,
	conv *Conversation,
	messages []Message,
) (*IngestResult, error) {
	var totalTokens int
	for _, msg := range messages {
		totalTokens += msg.TokenCount
	}
	// Insert the batch and append it to the context in the same transaction per
	// chunk (see AddMessagesBatch): a commit per message, plus one for the
	// context append, is the dominant cost with the pure-Go sqlite driver on
	// low-end hardware. The adapter ingests one message at a time, so the hot
	// path goes from two commits to one; bootstrap passes the remaining history
	// and gets one commit per IngestBatchChunkSize messages instead of N+1.
	if _, err := e.store.AddMessagesBatch(ctx, conv.ConversationID, messages); err != nil {
		return nil, fmt.Errorf("add messages: %w", err)
	}

	logger.InfoCF("seahorse", "ingest", map[string]any{
		"conv_id":  conv.ConversationID,
		"messages": len(messages),
		"tokens":   totalTokens,
	})
	return &IngestResult{
		MessageCount: len(messages),
		TokenCount:   totalTokens,
	}, nil
}

// Close releases resources.
func (e *Engine) Close() error {
	// Signal compaction goroutines to stop
	if e.compaction != nil {
		e.compaction.Close()
	}
	if e.store != nil && e.store.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = e.store.Optimize(ctx)
		cancel()
		err := e.store.db.Close()
		if e.releaseProcess != nil {
			if releaseErr := e.releaseProcess(); err == nil {
				err = releaseErr
			}
		}
		return err
	}
	if e.releaseProcess != nil {
		return e.releaseProcess()
	}
	return nil
}

// GetRetrieval returns the retrieval engine for tool implementations.
func (e *Engine) GetRetrieval() *RetrievalEngine {
	return e.retrieval
}

// Assemble builds budget-constrained context for a session.
func (e *Engine) Assemble(
	ctx context.Context,
	sessionKey string,
	input AssembleInput,
) (*AssembleResult, error) {
	if e.shouldIgnoreSession(sessionKey) {
		return nil, nil
	}

	conv, err := e.store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	e.initAssemblerOnce()
	return e.assembler.Assemble(ctx, conv.ConversationID, input)
}

// Compact compresses conversation history for a session.
func (e *Engine) Compact(
	ctx context.Context,
	sessionKey string,
	input CompactInput,
) (*CompactResult, error) {
	if e.shouldIgnoreSession(sessionKey) || e.isStatelessSession(sessionKey) {
		return &CompactResult{}, nil
	}

	conv, err := e.store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	e.initCompactionOnce()
	return e.compaction.Compact(ctx, conv.ConversationID, input)
}

// CompactNow synchronously compacts a session on user request (/compact).
// instructions (may be empty) tells the summarizer which details to prioritize.
func (e *Engine) CompactNow(
	ctx context.Context,
	sessionKey string,
	instructions string,
	protectTailMessages ...int,
) (*CompactResult, error) {
	if e.shouldIgnoreSession(sessionKey) || e.isStatelessSession(sessionKey) {
		return &CompactResult{}, nil
	}

	conv, err := e.store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	e.initCompactionOnce()
	return e.compaction.CompactNow(ctx, conv.ConversationID, instructions, protectTailMessages...)
}

// CompactUntilUnder aggressively compacts until context is under budget.
// Used for emergency compaction after LLM overflow (retry reason).
func (e *Engine) CompactUntilUnder(
	ctx context.Context,
	sessionKey string,
	budget int,
	protectTailMessages ...int,
) (*CompactResult, error) {
	if e.shouldIgnoreSession(sessionKey) || e.isStatelessSession(sessionKey) {
		return &CompactResult{}, nil
	}

	conv, err := e.store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	e.initCompactionOnce()
	return e.compaction.CompactUntilUnder(ctx, conv.ConversationID, budget, protectTailMessages...)
}

// initCompactionOnce lazily initializes the compaction engine.
func (e *Engine) initCompactionOnce() {
	if e.compaction == nil {
		e.compactionMu.Lock()
		defer e.compactionMu.Unlock()
		if e.compaction == nil {
			shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
			e.compaction = &CompactionEngine{
				store:          e.store,
				config:         e.config,
				complete:       e.complete,
				locks:          e.conversationLockManager(),
				shutdownCtx:    shutdownCtx,
				shutdownCancel: shutdownCancel,
			}
		}
	}
}

// initAssemblerOnce lazily initializes the assembler.
func (e *Engine) initAssemblerOnce() {
	if e.assembler == nil {
		e.assemblerMu.Lock()
		defer e.assemblerMu.Unlock()
		if e.assembler == nil {
			e.assembler = &Assembler{store: e.store, config: e.config}
		}
	}
}

// IngestMessages is an alias for Ingest.
func (e *Engine) IngestMessages(
	ctx context.Context,
	sessionKey string,
	messages []Message,
) (*IngestResult, error) {
	return e.Ingest(ctx, sessionKey, messages)
}

// ClearSession removes all stored data for a session (messages, summaries, context).
// If the session has no prior seahorse record, it is a no-op.
func (e *Engine) ClearSession(ctx context.Context, sessionKey string) error {
	conv, err := e.store.GetConversationBySessionKey(ctx, sessionKey)
	if err != nil {
		return err
	}
	if conv == nil {
		return nil // session never ingested, nothing to clear
	}
	unlock := e.lockConversation(conv.ConversationID)
	defer unlock()
	return e.store.ClearConversation(ctx, conv.ConversationID)
}

// DeleteSession removes all stored data for a session and the conversation row
// itself. Used by session pruning so no orphaned conversation rows remain.
func (e *Engine) DeleteSession(ctx context.Context, sessionKey string) error {
	conv, err := e.store.GetConversationBySessionKey(ctx, sessionKey)
	if err != nil {
		return err
	}
	if conv == nil {
		return nil // session never ingested, nothing to delete
	}
	unlock := e.lockConversation(conv.ConversationID)
	defer unlock()
	return e.store.DeleteConversation(ctx, conv.ConversationID)
}

// SessionStatuses returns status for every conversation in the database.
func (e *Engine) SessionStatuses(ctx context.Context) ([]SessionStatus, error) {
	return e.store.GetAllSessionStatuses(ctx)
}

// ContextTokens returns the number of tokens represented by the stored context
// of a session, without creating a conversation for an unknown key.
func (e *Engine) ContextTokens(ctx context.Context, sessionKey string) (int, error) {
	conv, err := e.store.GetConversationBySessionKey(ctx, sessionKey)
	if err != nil {
		return 0, err
	}
	if conv == nil {
		return 0, nil
	}
	return e.store.GetContextTokenCount(ctx, conv.ConversationID)
}

// ConversationCount returns the number of conversations in the database.
func (e *Engine) ConversationCount(ctx context.Context) (int, error) {
	return e.store.ConversationCount(ctx)
}

// DBFileSize returns the combined size in bytes of the SQLite database file and
// its write-ahead log (if present).
func (e *Engine) DBFileSize() (int64, error) {
	path := e.config.DBPath
	if strings.TrimSpace(path) == "" {
		return 0, nil
	}
	var total int64
	if info, err := os.Stat(path); err == nil {
		total += info.Size()
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if info, err := os.Stat(path + "-wal"); err == nil {
		total += info.Size()
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	return total, nil
}

// Vacuum reclaims unused disk space in the underlying SQLite database.
func (e *Engine) Vacuum(ctx context.Context) error {
	if e.config.DBPath != "" {
		if info, err := os.Stat(e.config.DBPath); err == nil {
			if free, ok := diskFreeBytes(e.config.DBPath); ok {
				// VACUUM rewrites the file; leave headroom for the new copy and
				// SQLite's temporary files.
				needed := info.Size() + info.Size()/10 + 16<<20
				if free < needed {
					return fmt.Errorf(
						"insufficient disk space for VACUUM: free=%d needed=%d",
						free,
						needed,
					)
				}
			}
		}
	}
	return e.store.Vacuum(ctx)
}

// Optimize runs SQLite planner maintenance after pruning or before shutdown.
func (e *Engine) Optimize(ctx context.Context) error {
	return e.store.Optimize(ctx)
}

// ReclaimableBytes reports how much of the database file is free space that a
// Vacuum would return to the filesystem.
func (e *Engine) ReclaimableBytes(ctx context.Context) (int64, error) {
	return e.store.ReclaimableBytes(ctx)
}

// Checkpoint folds the write-ahead log into the database file. When truncate is
// true the -wal file is also shrunk to zero bytes.
func (e *Engine) Checkpoint(ctx context.Context, truncate bool) error {
	return e.store.CheckpointWAL(ctx, truncate)
}

// Bootstrap reconciles a session's messages with the database.
// Called once at startup for each known session.
// Bootstrap reconciles JSONL history with SQLite by ingesting only the delta.
// Simple approach: find longest matching prefix and append delta.
// If any mismatch is detected, clear and rebuild.
func (e *Engine) Bootstrap(ctx context.Context, sessionKey string, messages []Message) error {
	if e.shouldIgnoreSession(sessionKey) {
		return nil
	}
	if e.isStatelessSession(sessionKey) {
		return nil
	}
	if len(messages) == 0 {
		// The JSONL is canonical. An empty canonical history means the session
		// was cleared or truncated; stale SQLite rows must not survive it.
		conv, err := e.store.GetConversationBySessionKey(ctx, sessionKey)
		if err != nil {
			return fmt.Errorf("bootstrap: get conversation: %w", err)
		}
		if conv == nil {
			return nil
		}
		unlock := e.lockConversation(conv.ConversationID)
		defer unlock()
		return e.store.InvalidateConversation(ctx, conv.ConversationID)
	}

	conv, err := e.store.GetOrCreateConversation(ctx, sessionKey)
	if err != nil {
		return fmt.Errorf("bootstrap: get conversation: %w", err)
	}
	unlock := e.lockConversation(conv.ConversationID)
	defer unlock()

	// Source-identified messages make a retried bootstrap idempotent even when
	// a previous attempt committed only some chunks.
	for i := range messages {
		if messages[i].ExternalID == "" {
			messages[i].ExternalID = bootstrapExternalID(i, messages[i])
		}
	}

	// Read one extra row so a database tail longer than the canonical JSONL is
	// detectable instead of being hidden by the SQL LIMIT.
	dbMsgs, err := e.store.GetMessages(ctx, conv.ConversationID, len(messages)+1, 0)
	if err != nil {
		return fmt.Errorf("bootstrap: get messages: %w", err)
	}

	// Migration repair path: old SeaHorse rows may be missing reasoning_content
	// even though the canonical JSONL history already has it. Backfill those
	// rows in place so we do not treat this as edited history and leave stale
	// summaries/context behind after a partial raw-message rebuild.
	if _, err := e.repairBootstrapReasoningContent(ctx, dbMsgs, messages); err != nil {
		return fmt.Errorf("bootstrap: repair reasoning_content: %w", err)
	}
	if _, err := e.repairBootstrapModelName(ctx, dbMsgs, messages); err != nil {
		return fmt.Errorf("bootstrap: repair model_name: %w", err)
	}
	if _, err := e.repairBootstrapCreatedAt(ctx, dbMsgs, messages); err != nil {
		return fmt.Errorf("bootstrap: repair created_at: %w", err)
	}
	// Fast path: after in-place repairs, a same-count exact match is a no-op.
	// The repair flags are not needed here: the comparison already reads the
	// repaired rows from dbMsgs, which repairBootstrapField updates in place.
	if len(dbMsgs) == len(messages) {
		matched := true
		for i := range messages {
			if !messagesMatch(dbMsgs[i], messages[i], messageMatchOptions{}) {
				matched = false
				break
			}
		}
		if matched {
			return nil // DB is up to date
		}
	}

	// Find longest matching prefix from the start
	anchor := -1
	compareLen := min(len(dbMsgs), len(messages))

	for i := range compareLen {
		if messagesMatch(dbMsgs[i], messages[i], messageMatchOptions{}) {
			anchor = i
		} else {
			// Mismatch detected - log details and rebuild
			logger.InfoCF("seahorse", "bootstrap: mismatch detected", map[string]any{
				"conv_id":        conv.ConversationID,
				"index":          i,
				"db_role":        dbMsgs[i].Role,
				"db_content":     textutil.TruncateRunes(dbMsgs[i].Content, 50),
				"db_parts":       len(dbMsgs[i].Parts),
				"db_model_name":  dbMsgs[i].ModelName,
				"msg_role":       messages[i].Role,
				"msg_content":    textutil.TruncateRunes(messages[i].Content, 50),
				"msg_parts":      len(messages[i].Parts),
				"msg_model_name": messages[i].ModelName,
			})
			break
		}
	}

	extraDB := len(dbMsgs) > len(messages)

	// If we hit a mismatch before reaching the end of DB messages, delete delta and re-ingest.
	// Note: anchor can be -1 if first message didn't match (history completely changed).
	if anchor >= 0 && (anchor < len(dbMsgs)-1 || extraDB) && len(dbMsgs) > 0 {
		anchorID := dbMsgs[anchor].ID
		logger.InfoCF("seahorse", "bootstrap: history edit detected", map[string]any{
			"conv_id":     conv.ConversationID,
			"db_count":    len(dbMsgs),
			"anchor":      anchor,
			"anchor_id":   anchorID,
			"msg_count":   len(messages),
			"delta_start": anchor + 1,
			"extra_db":    extraDB,
		})

		if err := e.store.InvalidateDerivedState(ctx, conv.ConversationID); err != nil {
			return fmt.Errorf("bootstrap: invalidate derived state: %w", err)
		}
		if err := e.store.DeleteMessagesAfterID(ctx, conv.ConversationID, 0); err != nil {
			return fmt.Errorf("bootstrap: delete messages: %w", err)
		}

		// A derived-state invalidation also removes the context items of the
		// retained prefix. Rebuild the whole history so every message is
		// represented exactly once.
		if _, err := e.ingestConversation(ctx, conv, messages); err != nil {
			return fmt.Errorf("bootstrap: re-ingest: %w", err)
		}
		return nil
	}

	// Normal case: append delta after anchor
	if anchor >= 0 && anchor < len(messages)-1 {
		delta := messages[anchor+1:]
		if len(delta) > 0 {
			if _, err := e.ingestConversation(ctx, conv, delta); err != nil {
				return fmt.Errorf("bootstrap: ingest delta: %w", err)
			}
		}
	} else if anchor == -1 && len(dbMsgs) > 0 {
		// First message changed (history completely different) - rebuild from scratch
		logger.InfoCF("seahorse", "bootstrap: history replaced, rebuilding", map[string]any{
			"conv_id":   conv.ConversationID,
			"db_count":  len(dbMsgs),
			"msg_count": len(messages),
		})
		if err := e.store.InvalidateDerivedState(ctx, conv.ConversationID); err != nil {
			return fmt.Errorf("bootstrap: invalidate derived state: %w", err)
		}
		if err := e.store.DeleteMessagesAfterID(ctx, conv.ConversationID, 0); err != nil {
			return fmt.Errorf("bootstrap: delete all messages: %w", err)
		}
		if len(messages) > 0 {
			if _, err := e.ingestConversation(ctx, conv, messages); err != nil {
				return fmt.Errorf("bootstrap: re-ingest all: %w", err)
			}
		}
	} else if anchor == -1 && len(dbMsgs) == 0 {
		// DB is empty, ingest everything
		if _, err := e.ingestConversation(ctx, conv, messages); err != nil {
			return fmt.Errorf("bootstrap: ingest all: %w", err)
		}
	}

	return nil
}

// bootstrapExternalID derives a stable identity from the canonical history
// position and message payload. Re-running bootstrap with the same JSONL
// produces the same IDs; editing history changes them and triggers a rebuild.
func bootstrapExternalID(index int, msg Message) string {
	h := sha256.New()
	fmt.Fprintf(
		h,
		"%d\x00%s\x00%s\x00%s\x00%s\x00",
		index,
		msg.Role,
		msg.Content,
		msg.ModelName,
		msg.ReasoningContent,
	)
	for _, p := range msg.Parts {
		fmt.Fprintf(
			h,
			"%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00",
			p.Type,
			p.Text,
			p.Name,
			p.Arguments,
			p.ToolCallID,
			p.MediaURI,
		)
	}
	return fmt.Sprintf("jsonl:%d:%x", index, h.Sum(nil))
}

type bootstrapFieldRepair[T comparable] struct {
	want          func(Message) T
	have          func(Message) T
	equal         func(T, T) bool
	onlyIfMissing bool
	// created_at requires a timestamp on every overlapping message before applying any updates.
	rejectZeroBeforeEqual bool
	apply                 func(context.Context, int64, T) error
	set                   func(*Message, T)
	logKind               string
}

func repairBootstrapField[T comparable](
	ctx context.Context,
	dbMsgs, messages []Message,
	r bootstrapFieldRepair[T],
) (bool, error) {
	if len(dbMsgs) == 0 || len(messages) == 0 {
		return false, nil
	}

	type update struct {
		index     int
		messageID int64
		value     T
	}
	var updates []update
	var zero T

	for i := range min(len(messages), len(dbMsgs)) {
		if !messagesMatch(dbMsgs[i], messages[i], messageMatchOptions{
			IgnoreReasoningContent: true,
			IgnoreModelName:        true,
			IgnoreCreatedAt:        true,
		}) {
			return false, nil
		}

		want, have := r.want(messages[i]), r.have(dbMsgs[i])
		if r.rejectZeroBeforeEqual && want == zero {
			return false, nil
		}
		if r.equal != nil {
			if r.equal(have, want) {
				continue
			}
		} else if have == want {
			continue
		}
		if want == zero || (r.onlyIfMissing && have != zero) {
			return false, nil
		}
		updates = append(updates, update{index: i, messageID: dbMsgs[i].ID, value: want})
	}

	if len(updates) == 0 {
		return false, nil
	}
	for _, update := range updates {
		if err := r.apply(ctx, update.messageID, update.value); err != nil {
			return false, err
		}
		r.set(&dbMsgs[update.index], update.value)
	}

	logger.InfoCF("seahorse", "bootstrap: repaired "+r.logKind, map[string]any{
		"messages": len(updates),
	})
	return true, nil
}

func (e *Engine) repairBootstrapReasoningContent(
	ctx context.Context,
	dbMsgs, messages []Message,
) (bool, error) {
	return repairBootstrapField(ctx, dbMsgs, messages, bootstrapFieldRepair[string]{
		want:          func(m Message) string { return m.ReasoningContent },
		have:          func(m Message) string { return m.ReasoningContent },
		onlyIfMissing: true,
		apply:         e.store.UpdateMessageReasoningContent,
		set:           func(m *Message, v string) { m.ReasoningContent = v },
		logKind:       "missing reasoning_content",
	})
}

func (e *Engine) repairBootstrapModelName(
	ctx context.Context,
	dbMsgs, messages []Message,
) (bool, error) {
	return repairBootstrapField(ctx, dbMsgs, messages, bootstrapFieldRepair[string]{
		want:    func(m Message) string { return m.ModelName },
		have:    func(m Message) string { return m.ModelName },
		apply:   e.store.UpdateMessageModelName,
		set:     func(m *Message, v string) { m.ModelName = v },
		logKind: "missing model_name",
	})
}

func (e *Engine) repairBootstrapCreatedAt(
	ctx context.Context,
	dbMsgs, messages []Message,
) (bool, error) {
	return repairBootstrapField(ctx, dbMsgs, messages, bootstrapFieldRepair[time.Time]{
		want:                  func(m Message) time.Time { return normalizeMessageCreatedAt(m.CreatedAt) },
		have:                  func(m Message) time.Time { return m.CreatedAt },
		equal:                 time.Time.Equal,
		rejectZeroBeforeEqual: true,
		apply:                 e.store.UpdateMessageCreatedAt,
		set:                   func(m *Message, v time.Time) { m.CreatedAt = v },
		logKind:               "message created_at",
	})
}

type messageMatchOptions struct {
	IgnoreReasoningContent bool
	IgnoreModelName        bool
	IgnoreCreatedAt        bool
}

// messagesMatch compares two messages by role and payload, plus the optional
// metadata fields used by bootstrap repair. TokenCount is intentionally ignored
// because bootstrap may re-estimate it differently.
func messagesMatch(a, b Message, opts messageMatchOptions) bool {
	if a.Role != b.Role {
		return false
	}
	if !opts.IgnoreReasoningContent && a.ReasoningContent != b.ReasoningContent {
		return false
	}
	if !opts.IgnoreModelName && a.ModelName != b.ModelName {
		return false
	}
	if !opts.IgnoreCreatedAt && !messageCreatedAtMatches(a.CreatedAt, b.CreatedAt) {
		return false
	}
	// If either message has Parts, compare Parts
	if len(a.Parts) > 0 || len(b.Parts) > 0 {
		return partsMatch(a.Parts, b.Parts)
	}
	// Simple text messages: compare Content
	return a.Content == b.Content
}

// messageCreatedAtMatches treats missing timestamps as compatible so bootstrap
// can preserve legacy histories while still enforcing exact equality once both
// sides carry canonical created_at values.
func messageCreatedAtMatches(a, b time.Time) bool {
	na := normalizeMessageCreatedAt(a)
	nb := normalizeMessageCreatedAt(b)
	if na.IsZero() || nb.IsZero() {
		return true
	}
	return na.Equal(nb)
}

// partsMatch compares two slices of MessagePart for equality.
func partsMatch(a, b []MessagePart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Type != b[i].Type {
			return false
		}
		switch a[i].Type {
		case "text":
			if a[i].Text != b[i].Text {
				return false
			}
		case "tool_use":
			if a[i].Name != b[i].Name || a[i].Arguments != b[i].Arguments ||
				a[i].ToolCallID != b[i].ToolCallID {
				return false
			}
		case "tool_result":
			if a[i].ToolCallID != b[i].ToolCallID || a[i].Text != b[i].Text {
				return false
			}
		case "media":
			if a[i].MediaURI != b[i].MediaURI || a[i].MimeType != b[i].MimeType {
				return false
			}
		}
	}
	return true
}
