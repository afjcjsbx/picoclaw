package index

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed persistence for the memory index.
type Store struct {
	db           *sql.DB
	path         string
	ftsAvailable bool
}

// OpenStore opens or creates the SQLite database at the given path,
// initializes all required tables, and returns a ready-to-use Store.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}

	if err := initTables(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init tables: %w", err)
	}

	s := &Store{db: db, path: path}
	s.ftsAvailable = s.initFTS()

	return s, nil
}

// initTables creates the core tables and indexes.
func initTables(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, source TEXT NOT NULL, hash TEXT NOT NULL, mtime INTEGER NOT NULL, size INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS chunks (id TEXT PRIMARY KEY, path TEXT NOT NULL, source TEXT NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL, hash TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', text TEXT NOT NULL, embedding TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_path ON chunks(path)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_source ON chunks(source)`,
		`CREATE TABLE IF NOT EXISTS embedding_cache (provider TEXT NOT NULL, model TEXT NOT NULL, provider_key TEXT NOT NULL, hash TEXT NOT NULL, embedding TEXT NOT NULL, dims INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (provider, model, provider_key, hash))`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:min(len(stmt), 60)], err)
		}
	}
	return nil
}

// initFTS creates the FTS5 virtual table and sync triggers.
// Returns true if FTS5 is available, false otherwise.
func (s *Store) initFTS() bool {
	// Use a standalone FTS5 table (not content-sync) for simplicity and reliability.
	// Data is kept in sync manually during chunk upsert/delete operations.
	_, err := s.db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(id, text, path UNINDEXED, source UNINDEXED, start_line UNINDEXED, end_line UNINDEXED)`)
	return err == nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Path returns the database file path.
func (s *Store) Path() string {
	return s.path
}

// FTSAvailable reports whether FTS5 full-text search is available.
func (s *Store) FTSAvailable() bool {
	return s.ftsAvailable
}

// Reopen closes the current connection and opens a fresh one.
// Used for recovery from readonly database errors.
func (s *Store) Reopen() error {
	if s.db != nil {
		s.db.Close()
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return fmt.Errorf("reopen sqlite: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return fmt.Errorf("set busy_timeout on reopen: %w", err)
	}
	if err := initTables(db); err != nil {
		db.Close()
		return fmt.Errorf("init tables on reopen: %w", err)
	}
	s.db = db
	s.ftsAvailable = s.initFTS()
	return nil
}

// IsReadonlyError checks if an error indicates a readonly database.
func IsReadonlyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "readonly") ||
		strings.Contains(msg, "read-only") ||
		strings.Contains(msg, "SQLITE_READONLY")
}

// ---------------------------------------------------------------------------
// Meta operations
// ---------------------------------------------------------------------------

// GetMeta retrieves a value from the meta table by key.
// Returns an empty string and no error if the key does not exist.
func (s *Store) GetMeta(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get meta %q: %w", key, err)
	}
	return value, nil
}

// SetMeta stores a key-value pair in the meta table.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)", key, value)
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// File operations
// ---------------------------------------------------------------------------

// UpsertFile inserts or replaces a file record.
func (s *Store) UpsertFile(f FileRecord) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO files (path, source, hash, mtime, size) VALUES (?, ?, ?, ?, ?)",
		f.Path, f.Source, f.Hash, f.Mtime, f.Size,
	)
	if err != nil {
		return fmt.Errorf("upsert file %q: %w", f.Path, err)
	}
	return nil
}

// DeleteFile removes a file record by path.
func (s *Store) DeleteFile(path string) error {
	_, err := s.db.Exec("DELETE FROM files WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("delete file %q: %w", path, err)
	}
	return nil
}

// ListFiles returns all file records.
func (s *Store) ListFiles() ([]FileRecord, error) {
	rows, err := s.db.Query("SELECT path, source, hash, mtime, size FROM files")
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.Path, &f.Source, &f.Hash, &f.Mtime, &f.Size); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// GetFile returns a single file record by path, or nil if not found.
func (s *Store) GetFile(path string) (*FileRecord, error) {
	var f FileRecord
	err := s.db.QueryRow(
		"SELECT path, source, hash, mtime, size FROM files WHERE path = ?", path,
	).Scan(&f.Path, &f.Source, &f.Hash, &f.Mtime, &f.Size)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get file %q: %w", path, err)
	}
	return &f, nil
}

// ---------------------------------------------------------------------------
// Chunk operations
// ---------------------------------------------------------------------------

// UpsertChunks inserts or replaces multiple chunks in a single transaction.
func (s *Store) UpsertChunks(chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO chunks (id, path, source, start_line, end_line, hash, model, text, embedding, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare upsert chunks: %w", err)
	}
	defer stmt.Close()

	// Prepare FTS statement if available
	var ftsStmt *sql.Stmt
	if s.ftsAvailable {
		// Delete old FTS entries first, then insert new
		ftsStmt, err = tx.Prepare("INSERT INTO chunks_fts (id, text, path, source, start_line, end_line) VALUES (?, ?, ?, ?, ?, ?)")
		if err != nil {
			return fmt.Errorf("prepare fts upsert: %w", err)
		}
		defer ftsStmt.Close()
	}

	for _, c := range chunks {
		if _, err := stmt.Exec(c.ID, c.Path, c.Source, c.StartLine, c.EndLine, c.Hash, c.Model, c.Text, c.Embedding, c.UpdatedAt); err != nil {
			return fmt.Errorf("upsert chunk %q: %w", c.ID, err)
		}
		if ftsStmt != nil {
			if _, err := ftsStmt.Exec(c.ID, c.Text, c.Path, c.Source, c.StartLine, c.EndLine); err != nil {
				return fmt.Errorf("upsert fts chunk %q: %w", c.ID, err)
			}
		}
	}

	return tx.Commit()
}

// DeleteChunksByPath removes all chunks associated with a given file path.
func (s *Store) DeleteChunksByPath(path string) error {
	if s.ftsAvailable {
		if _, err := s.db.Exec("DELETE FROM chunks_fts WHERE path = ?", path); err != nil {
			return fmt.Errorf("delete fts chunks for path %q: %w", path, err)
		}
	}
	_, err := s.db.Exec("DELETE FROM chunks WHERE path = ?", path)
	if err != nil {
		return fmt.Errorf("delete chunks for path %q: %w", path, err)
	}
	return nil
}

// ListChunksByPath returns all chunks for a given file path.
func (s *Store) ListChunksByPath(path string) ([]Chunk, error) {
	rows, err := s.db.Query(
		"SELECT id, path, source, start_line, end_line, hash, model, text, embedding, updated_at FROM chunks WHERE path = ?",
		path,
	)
	if err != nil {
		return nil, fmt.Errorf("list chunks by path %q: %w", path, err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

// ListAllChunks returns every chunk in the store, suitable for vector search iteration.
func (s *Store) ListAllChunks() ([]Chunk, error) {
	rows, err := s.db.Query(
		"SELECT id, path, source, start_line, end_line, hash, model, text, embedding, updated_at FROM chunks",
	)
	if err != nil {
		return nil, fmt.Errorf("list all chunks: %w", err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

// CountChunks returns the total number of chunks.
func (s *Store) CountChunks() (int, error) {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&count); err != nil {
		return 0, fmt.Errorf("count chunks: %w", err)
	}
	return count, nil
}

// CountChunksBySource returns a map from source name to chunk count.
func (s *Store) CountChunksBySource() (map[string]int, error) {
	rows, err := s.db.Query("SELECT source, COUNT(*) FROM chunks GROUP BY source")
	if err != nil {
		return nil, fmt.Errorf("count chunks by source: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var source string
		var count int
		if err := rows.Scan(&source, &count); err != nil {
			return nil, fmt.Errorf("scan source count: %w", err)
		}
		counts[source] = count
	}
	return counts, rows.Err()
}

// scanChunks is a helper that scans rows into a slice of Chunk.
func scanChunks(rows *sql.Rows) ([]Chunk, error) {
	var chunks []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.Path, &c.Source, &c.StartLine, &c.EndLine, &c.Hash, &c.Model, &c.Text, &c.Embedding, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chunk row: %w", err)
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// ---------------------------------------------------------------------------
// Embedding cache operations
// ---------------------------------------------------------------------------

// GetCachedEmbeddings performs a batch lookup of cached embeddings by hash.
// It returns a map keyed by hash for the entries that were found.
func (s *Store) GetCachedEmbeddings(provider, model, providerKey string, hashes []string) (map[string]EmbeddingCacheEntry, error) {
	if len(hashes) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(hashes))
	args := make([]interface{}, 0, 3+len(hashes))
	args = append(args, provider, model, providerKey)
	for i, h := range hashes {
		placeholders[i] = "?"
		args = append(args, h)
	}

	query := fmt.Sprintf(
		"SELECT provider, model, provider_key, hash, embedding, dims, updated_at FROM embedding_cache WHERE provider = ? AND model = ? AND provider_key = ? AND hash IN (%s)",
		strings.Join(placeholders, ", "),
	)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cached embeddings: %w", err)
	}
	defer rows.Close()

	result := make(map[string]EmbeddingCacheEntry, len(hashes))
	for rows.Next() {
		var e EmbeddingCacheEntry
		if err := rows.Scan(&e.Provider, &e.Model, &e.ProviderKey, &e.Hash, &e.Embedding, &e.Dims, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan cache entry: %w", err)
		}
		result[e.Hash] = e
	}
	return result, rows.Err()
}

// SetCachedEmbeddings batch-inserts or replaces embedding cache entries.
func (s *Store) SetCachedEmbeddings(entries []EmbeddingCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO embedding_cache (provider, model, provider_key, hash, embedding, dims, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare set cache: %w", err)
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.Exec(e.Provider, e.Model, e.ProviderKey, e.Hash, e.Embedding, e.Dims, e.UpdatedAt); err != nil {
			return fmt.Errorf("set cache entry: %w", err)
		}
	}

	return tx.Commit()
}

// ListAllCacheEntries returns all embedding cache entries.
func (s *Store) ListAllCacheEntries() ([]EmbeddingCacheEntry, error) {
	rows, err := s.db.Query("SELECT provider, model, provider_key, hash, embedding, dims, updated_at FROM embedding_cache")
	if err != nil {
		return nil, fmt.Errorf("list cache entries: %w", err)
	}
	defer rows.Close()

	var entries []EmbeddingCacheEntry
	for rows.Next() {
		var e EmbeddingCacheEntry
		if err := rows.Scan(&e.Provider, &e.Model, &e.ProviderKey, &e.Hash, &e.Embedding, &e.Dims, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan cache entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// CountCacheEntries returns the total number of embedding cache entries.
func (s *Store) CountCacheEntries() (int, error) {
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM embedding_cache").Scan(&count); err != nil {
		return 0, fmt.Errorf("count cache entries: %w", err)
	}
	return count, nil
}

// EvictOldestCacheEntries deletes the oldest cache entries, keeping only the
// most recent keepCount entries ordered by updated_at.
func (s *Store) EvictOldestCacheEntries(keepCount int) error {
	_, err := s.db.Exec(
		`DELETE FROM embedding_cache WHERE rowid NOT IN (
			SELECT rowid FROM embedding_cache ORDER BY updated_at DESC LIMIT ?
		)`, keepCount,
	)
	if err != nil {
		return fmt.Errorf("evict oldest cache entries: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// FTS search
// ---------------------------------------------------------------------------

// SearchFTS performs a full-text search using FTS5 and returns matching chunks
// along with their BM25 rank scores. The rank values are negative; more
// negative means more relevant.
// Returns an error if FTS5 is not available.
func (s *Store) SearchFTS(query string, limit int) ([]Chunk, []float64, error) {
	if !s.ftsAvailable {
		return nil, nil, fmt.Errorf("FTS5 is not available")
	}

	rows, err := s.db.Query(
		`SELECT c.id, c.path, c.source, c.start_line, c.end_line, c.hash, c.model, c.text, c.embedding, c.updated_at, f.rank
		FROM chunks_fts f
		JOIN chunks c ON c.id = f.id
		WHERE chunks_fts MATCH ?
		ORDER BY f.rank
		LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()

	var chunks []Chunk
	var ranks []float64
	for rows.Next() {
		var c Chunk
		var rank float64
		if err := rows.Scan(&c.ID, &c.Path, &c.Source, &c.StartLine, &c.EndLine, &c.Hash, &c.Model, &c.Text, &c.Embedding, &c.UpdatedAt, &rank); err != nil {
			return nil, nil, fmt.Errorf("scan fts row: %w", err)
		}
		chunks = append(chunks, c)
		ranks = append(ranks, rank)
	}
	return chunks, ranks, rows.Err()
}
