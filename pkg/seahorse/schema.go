package seahorse

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// SQL statements for FTS5 tables with trigram tokenizer.
const (
	schemaVersion = 2

	sqlCreateSummariesFTS = `CREATE VIRTUAL TABLE IF NOT EXISTS summaries_fts USING fts5(
		summary_id UNINDEXED,
		content,
		content='summaries',
		content_rowid='rowid',
		tokenize="trigram"
	)`
	sqlCreateMessagesFTS = `CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		message_id UNINDEXED,
		content,
		content='messages',
		content_rowid='message_id',
		tokenize="trigram"
	)`
	sqlCheckFTS5Available    = `CREATE VIRTUAL TABLE IF NOT EXISTS _fts5_check USING fts5(content)`
	sqlCheckTrigramAvailable = `CREATE VIRTUAL TABLE IF NOT EXISTS _trigram_check USING fts5(content, tokenize="trigram")`
	sqlDropFTS5Check         = `DROP TABLE IF EXISTS _fts5_check`
	sqlDropTrigramCheck      = `DROP TABLE IF EXISTS _trigram_check`
)

// runSchema creates or upgrades the database schema.
// All schemas are idempotent (safe to run multiple times).
func runSchema(db *sql.DB) error {
	// Check FTS5 support before creating tables
	if err := checkFTS5Support(db); err != nil {
		return fmt.Errorf("FTS5 check: %w", err)
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			conversation_id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_key     TEXT NOT NULL UNIQUE,
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
			version         INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS messages (
			message_id      INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL REFERENCES conversations(conversation_id),
			role            TEXT NOT NULL,
			content         TEXT NOT NULL DEFAULT '',
			model_name      TEXT NOT NULL DEFAULT '',
			reasoning_content TEXT NOT NULL DEFAULT '',
			external_id     TEXT NOT NULL DEFAULT '',
			token_count     INTEGER NOT NULL DEFAULT 0 CHECK (token_count >= 0),
			created_at      TEXT NOT NULL DEFAULT (datetime('now'))
		)`,

		`CREATE TABLE IF NOT EXISTS message_parts (
			part_id     INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id  INTEGER NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
			type        TEXT NOT NULL,
			text        TEXT,
			name        TEXT,
			arguments   TEXT,
			tool_call_id TEXT,
			media_uri   TEXT,
			mime_type   TEXT,
			ordinal     INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS summaries (
			summary_id                TEXT PRIMARY KEY,
			conversation_id           INTEGER NOT NULL REFERENCES conversations(conversation_id),
			kind                      TEXT NOT NULL CHECK (kind IN ('leaf','condensed')),
			depth                     INTEGER NOT NULL DEFAULT 0 CHECK (depth >= 0),
			content                   TEXT NOT NULL,
			token_count               INTEGER NOT NULL DEFAULT 0 CHECK (token_count >= 0),
			earliest_at               TEXT,
			latest_at                 TEXT,
			descendant_count          INTEGER NOT NULL DEFAULT 0,
			descendant_token_count    INTEGER NOT NULL DEFAULT 0,
			source_message_token_count INTEGER NOT NULL DEFAULT 0,
			model                     TEXT,
			invalidated_at            TEXT,
			created_at                TEXT NOT NULL DEFAULT (datetime('now'))
		)`,

		`CREATE TABLE IF NOT EXISTS summary_parents (
			summary_id        TEXT NOT NULL REFERENCES summaries(summary_id) ON DELETE CASCADE,
			parent_summary_id TEXT NOT NULL REFERENCES summaries(summary_id) ON DELETE CASCADE,
			PRIMARY KEY (summary_id, parent_summary_id)
		)`,

		`CREATE TABLE IF NOT EXISTS summary_messages (
			summary_id TEXT NOT NULL REFERENCES summaries(summary_id) ON DELETE CASCADE,
			message_id INTEGER NOT NULL REFERENCES messages(message_id) ON DELETE CASCADE,
			ordinal    INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (summary_id, message_id)
		)`,

		`CREATE TABLE IF NOT EXISTS context_items (
			conversation_id INTEGER NOT NULL,
			ordinal         INTEGER NOT NULL,
			item_type       TEXT NOT NULL CHECK (item_type IN ('message','summary')),
			summary_id      TEXT,
			message_id      INTEGER,
			token_count     INTEGER NOT NULL DEFAULT 0 CHECK (token_count >= 0),
			created_at      TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (conversation_id, ordinal),
			CHECK (
				(item_type = 'message' AND message_id IS NOT NULL AND summary_id IS NULL) OR
				(item_type = 'summary' AND summary_id IS NOT NULL AND message_id IS NULL)
			)
		)`,

		// FTS5 virtual table with trigram tokenizer for CJK support
		sqlCreateSummariesFTS,

		// FTS5 virtual table for message search with trigram tokenizer
		sqlCreateMessagesFTS,

		// Indexes for common query patterns
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id, message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_created ON messages(conversation_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_message_parts_message ON message_parts(message_id, ordinal)`,
		`CREATE INDEX IF NOT EXISTS idx_summaries_conversation ON summaries(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_summaries_created ON summaries(conversation_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_summaries_kind_depth ON summaries(conversation_id, kind, depth)`,
		`CREATE INDEX IF NOT EXISTS idx_summary_parents_parent ON summary_parents(parent_summary_id)`,
		`CREATE INDEX IF NOT EXISTS idx_summary_messages_message ON summary_messages(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_items_conv ON context_items(conversation_id, ordinal)`,
		`CREATE INDEX IF NOT EXISTS idx_context_items_message ON context_items(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_context_items_summary ON context_items(conversation_id, summary_id)`,

		// Drop old triggers before creating new ones so existing DBs get updated bodies.
		// (CREATE TRIGGER IF NOT EXISTS does NOT replace an existing trigger body.)
		`DROP TRIGGER IF EXISTS summaries_ai`,
		`DROP TRIGGER IF EXISTS summaries_ad`,
		`DROP TRIGGER IF EXISTS summaries_au`,
		`DROP TRIGGER IF EXISTS messages_ai`,
		`DROP TRIGGER IF EXISTS messages_ad`,
		`DROP TRIGGER IF EXISTS messages_au`,
		`DROP TRIGGER IF EXISTS messages_touch`,

		// FTS5 triggers to keep summaries_fts in sync with summaries table.
		`CREATE TRIGGER summaries_ai AFTER INSERT ON summaries BEGIN
			INSERT INTO summaries_fts (rowid, summary_id, content)
			VALUES (new.rowid, new.summary_id, substr(new.content, 1, 65536));
		END`,
		`CREATE TRIGGER summaries_ad AFTER DELETE ON summaries BEGIN
			INSERT INTO summaries_fts (summaries_fts, rowid, summary_id, content)
			VALUES ('delete', old.rowid, old.summary_id, substr(old.content, 1, 65536));
		END`,
		`CREATE TRIGGER summaries_au AFTER UPDATE ON summaries BEGIN
			INSERT INTO summaries_fts (summaries_fts, rowid, summary_id, content)
			VALUES ('delete', old.rowid, old.summary_id, substr(old.content, 1, 65536));
			INSERT INTO summaries_fts (rowid, summary_id, content)
			VALUES (new.rowid, new.summary_id, substr(new.content, 1, 65536));
		END`,

		// FTS5 triggers to keep messages_fts in sync with messages table.
		// messages_fts uses the message_id as its FTS5 rowid so DELETE/UPDATE
		// target a row in O(log n) instead of scanning the whole FTS table.
		`CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts (rowid, message_id, content)
			VALUES (new.message_id, new.message_id, substr(new.content, 1, 65536));
		END`,
		`CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts (messages_fts, rowid, message_id, content)
			VALUES ('delete', old.message_id, old.message_id, substr(old.content, 1, 65536));
		END`,
		`CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
			INSERT INTO messages_fts (messages_fts, rowid, message_id, content)
			VALUES ('delete', old.message_id, old.message_id, substr(old.content, 1, 65536));
			INSERT INTO messages_fts (rowid, message_id, content)
			VALUES (new.message_id, new.message_id, substr(new.content, 1, 65536));
		END`,

		`CREATE TRIGGER messages_touch AFTER INSERT ON messages BEGIN
			UPDATE conversations SET updated_at = datetime('now')
			WHERE conversation_id = new.conversation_id;
		END`,
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, s := range stmts {
		if _, err := tx.Exec(s); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if err := ensureMessagesReasoningContentColumn(db); err != nil {
		return err
	}
	if err := ensureMessagesModelNameColumn(db); err != nil {
		return err
	}
	if err := ensureMessagesExternalIDColumn(db); err != nil {
		return err
	}
	if err := ensureConversationsVersionColumn(db); err != nil {
		return err
	}
	if err := ensureSummariesInvalidatedAtColumn(db); err != nil {
		return err
	}
	if err := ensureMessagesFTSRowid(db); err != nil {
		return err
	}
	if err := ensureSummariesFTSExternalContent(db); err != nil {
		return err
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	return nil
}

// backupBeforeMigration writes a consistent snapshot to <path>.bak before a
// schema upgrade. VACUUM INTO includes committed WAL pages, unlike a raw copy
// of the main database file. A failed snapshot aborts startup rather than
// migrating without a recoverable backup.
func backupBeforeMigration(db *sql.DB, path string) error {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file::memory:") {
		return nil
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version >= schemaVersion {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() == 0 {
		return nil
	}

	backupPath := path + ".bak"
	tmpPath := backupPath + ".tmp"
	_ = os.Remove(tmpPath)
	if _, err := db.ExecContext(context.Background(), "VACUUM INTO ?", tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("backup database: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("secure backup database: %w", err)
	}
	// Windows cannot rename over an existing file, so remove the previous
	// snapshot only after the new one has been written successfully.
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace backup database: %w", err)
	}
	if err := os.Rename(tmpPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("commit backup database: %w", err)
	}
	return nil
}

func ensureConversationsVersionColumn(db *sql.DB) error {
	hasColumn, err := tableHasColumn(db, "conversations", "version")
	if err != nil {
		return fmt.Errorf("check conversations.version: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE conversations ADD COLUMN version INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add conversations.version: %w", err)
	}
	return nil
}

func ensureSummariesInvalidatedAtColumn(db *sql.DB) error {
	hasColumn, err := tableHasColumn(db, "summaries", "invalidated_at")
	if err != nil {
		return fmt.Errorf("check summaries.invalidated_at: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE summaries ADD COLUMN invalidated_at TEXT`); err != nil {
		return fmt.Errorf("add summaries.invalidated_at: %w", err)
	}
	return nil
}

func ensureMessagesExternalIDColumn(db *sql.DB) error {
	hasColumn, err := tableHasColumn(db, "messages", "external_id")
	if err != nil {
		return fmt.Errorf("check messages.external_id: %w", err)
	}
	if !hasColumn {
		if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN external_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add messages.external_id: %w", err)
		}
	}
	// A partial unique index allows ordinary messages (empty external_id) to
	// remain unconstrained while making source-identified messages idempotent.
	if _, err := db.Exec(
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_external_id
		 ON messages(conversation_id, external_id) WHERE external_id <> ''`,
	); err != nil {
		return fmt.Errorf("create messages external_id index: %w", err)
	}
	return nil
}

func ensureMessagesReasoningContentColumn(db *sql.DB) error {
	hasColumn, err := tableHasColumn(db, "messages", "reasoning_content")
	if err != nil {
		return fmt.Errorf("check messages.reasoning_content: %w", err)
	}
	if hasColumn {
		return nil
	}

	if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN reasoning_content TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add messages.reasoning_content: %w", err)
	}
	return nil
}

func ensureMessagesModelNameColumn(db *sql.DB) error {
	hasColumn, err := tableHasColumn(db, "messages", "model_name")
	if err != nil {
		return fmt.Errorf("check messages.model_name: %w", err)
	}
	if hasColumn {
		return nil
	}

	if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN model_name TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add messages.model_name: %w", err)
	}
	return nil
}

// ensureMessagesFTSRowid migrates legacy messages_fts tables to the external
// content form. The base messages table remains the single copy of content;
// FTS5 only stores the index. message_id is both the rowid and an UNINDEXED
// column so existing joins keep working.
func ensureMessagesFTSRowid(db *sql.DB) error {
	var ddl string
	err := db.QueryRow(
		`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'messages_fts'`,
	).Scan(&ddl)
	if err == sql.ErrNoRows {
		return nil // table not created yet; schema will create the current form
	}
	if err != nil {
		return fmt.Errorf("read messages_fts schema: %w", err)
	}
	if strings.Contains(strings.ToLower(ddl), "content='messages'") {
		return nil // already external content
	}
	return rebuildMessagesFTS(db)
}

// ensureSummariesFTSExternalContent migrates summaries_fts to an external
// content table keyed by the summaries rowid. summary_id is UNINDEXED so it no
// longer pollutes search results.
func ensureSummariesFTSExternalContent(db *sql.DB) error {
	var ddl string
	err := db.QueryRow(
		`SELECT COALESCE(sql, '') FROM sqlite_master WHERE type = 'table' AND name = 'summaries_fts'`,
	).Scan(&ddl)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read summaries_fts schema: %w", err)
	}
	if strings.Contains(strings.ToLower(ddl), "content='summaries'") {
		return nil
	}
	return rebuildSummariesFTS(db)
}

// rebuildMessagesFTS drops and recreates messages_fts with the current
// definition and repopulates it from the messages table, aligning rowid with
// message_id.
func rebuildMessagesFTS(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin messages_fts rebuild: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DROP TABLE IF EXISTS messages_fts`); err != nil {
		return fmt.Errorf("drop messages_fts: %w", err)
	}
	if _, err := tx.Exec(sqlCreateMessagesFTS); err != nil {
		return fmt.Errorf("recreate messages_fts: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages_fts (rowid, message_id, content)
		 SELECT message_id, message_id, substr(content, 1, 65536) FROM messages`,
	); err != nil {
		return fmt.Errorf("repopulate messages_fts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit messages_fts rebuild: %w", err)
	}
	return nil
}

func rebuildSummariesFTS(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin summaries_fts rebuild: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DROP TABLE IF EXISTS summaries_fts`); err != nil {
		return fmt.Errorf("drop summaries_fts: %w", err)
	}
	if _, err := tx.Exec(sqlCreateSummariesFTS); err != nil {
		return fmt.Errorf("recreate summaries_fts: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO summaries_fts (rowid, summary_id, content)
		 SELECT rowid, summary_id, substr(content, 1, 65536) FROM summaries`,
	); err != nil {
		return fmt.Errorf("repopulate summaries_fts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit summaries_fts rebuild: %w", err)
	}
	return nil
}

func tableHasColumn(db *sql.DB, tableName, columnName string) (bool, error) {
	// pragma_table_info() table-valued function accepts bound parameters, unlike PRAGMA.
	var found int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, tableName, columnName,
	).Scan(&found)
	return found > 0, err
}

// checkFTS5Support verifies that SQLite has FTS5 with trigram tokenizer enabled.
// This is required for full-text search with CJK (Chinese, Japanese, Korean) support.
func checkFTS5Support(db *sql.DB) error {
	// Check if FTS5 is compiled in
	var fts5Enabled int
	err := db.QueryRow(`SELECT sqlite_compileoption_used('ENABLE_FTS5')`).Scan(&fts5Enabled)
	if err != nil {
		// sqlite_compileoption_used might not exist in older SQLite
		// Try a different approach: create a test FTS5 table
		_, testErr := db.Exec(sqlCheckFTS5Available)
		if testErr != nil {
			return fmt.Errorf(
				"SQLite FTS5 not available: %w (required for full-text search)",
				testErr,
			)
		}
		db.Exec(sqlDropFTS5Check)
	} else if fts5Enabled == 0 {
		return fmt.Errorf("SQLite was compiled without FTS5 support (required for full-text search)")
	}

	// Check if trigram tokenizer is available by trying to create a test table
	// Not all SQLite builds include the trigram tokenizer
	_, err = db.Exec(sqlCheckTrigramAvailable)
	if err != nil {
		logger.WarnCF(
			"seahorse",
			"SQLite trigram tokenizer not available, CJK search may be limited",
			map[string]any{"error": err.Error()},
		)
		// Trigram is not strictly required, just better for CJK
		// Don't return error, just log warning
	} else {
		db.Exec(sqlDropTrigramCheck)
	}

	return nil
}
