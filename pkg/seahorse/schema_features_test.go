package seahorse

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaVersionAndIndexes(t *testing.T) {
	db := openTestDB(t)
	if err := runSchema(db); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("user_version = %d, want %d", version, schemaVersion)
	}
	for _, idx := range []string{
		"idx_context_items_message",
		"idx_context_items_summary",
		"idx_summaries_created",
		"idx_messages_conversation_id",
	} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("missing index %s: %v", idx, err)
		}
	}
}

func TestRunSchemaRebuildsLegacyTablesWithConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	legacy := []string{
		`CREATE TABLE conversations (
			conversation_id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_key TEXT NOT NULL UNIQUE,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE messages (
			message_id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			model_name TEXT NOT NULL DEFAULT '',
			token_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE message_parts (
			part_id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			text TEXT,
			name TEXT,
			arguments TEXT,
			tool_call_id TEXT,
			media_uri TEXT,
			mime_type TEXT,
			ordinal INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE summaries (
			summary_id TEXT PRIMARY KEY,
			conversation_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			depth INTEGER NOT NULL DEFAULT 0,
			content TEXT NOT NULL,
			token_count INTEGER NOT NULL DEFAULT 0,
			earliest_at TEXT,
			latest_at TEXT,
			descendant_count INTEGER NOT NULL DEFAULT 0,
			descendant_token_count INTEGER NOT NULL DEFAULT 0,
			source_message_token_count INTEGER NOT NULL DEFAULT 0,
			model TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE summary_parents (
			summary_id TEXT NOT NULL,
			parent_summary_id TEXT NOT NULL,
			PRIMARY KEY (summary_id, parent_summary_id)
		)`,
		`CREATE TABLE summary_messages (
			summary_id TEXT NOT NULL,
			message_id INTEGER NOT NULL,
			ordinal INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (summary_id, message_id)
		)`,
		`CREATE TABLE context_items (
			conversation_id INTEGER NOT NULL,
			ordinal INTEGER NOT NULL,
			item_type TEXT NOT NULL,
			summary_id TEXT,
			message_id INTEGER,
			token_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (conversation_id, ordinal)
		)`,
	}
	for _, stmt := range legacy {
		if _, err = db.Exec(stmt); err != nil {
			t.Fatalf("legacy schema: %v", err)
		}
	}
	if _, err = db.Exec(
		`INSERT INTO conversations (session_key, created_at, updated_at)
		 VALUES ('legacy:session', datetime('now'), datetime('now'))`,
	); err != nil {
		t.Fatalf("insert conversation: %v", err)
	}
	if _, err = db.Exec(
		`INSERT INTO messages (conversation_id, role, content, token_count, created_at)
		 VALUES (1, 'user', 'legacy message', 3, datetime('now'))`,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	if _, err = db.Exec(
		`INSERT INTO context_items (conversation_id, ordinal, item_type, message_id, token_count)
		 VALUES (1, 100, 'message', 1, 3)`,
	); err != nil {
		t.Fatalf("insert context item: %v", err)
	}

	if err = runSchema(db); err != nil {
		t.Fatalf("runSchema: %v", err)
	}

	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if count != 1 {
		t.Fatalf("messages after migration = %d, want 1", count)
	}

	rows, err := db.Query(`PRAGMA foreign_key_list(context_items)`)
	if err != nil {
		t.Fatalf("foreign_key_list: %v", err)
	}
	defer rows.Close()
	foreignTables := map[string]string{}
	for rows.Next() {
		var id, seq int
		var tableName, from, to, onUpdate, onDelete, match string
		if err = rows.Scan(&id, &seq, &tableName, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatalf("scan foreign_key_list: %v", err)
		}
		foreignTables[tableName] = onDelete
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("foreign_key_list: %v", err)
	}
	for _, parent := range []string{"conversations", "messages", "summaries"} {
		if foreignTables[parent] != "CASCADE" {
			t.Fatalf("context_items FK to %s on_delete = %q, want CASCADE", parent, foreignTables[parent])
		}
	}

	if _, err = db.Exec(
		`INSERT INTO context_items (conversation_id, ordinal, item_type, message_id, token_count)
		 VALUES (1, 200, 'bogus', 1, 1)`,
	); err == nil {
		t.Fatal("invalid item_type was accepted after migration")
	}

	if _, err = db.Exec(`DELETE FROM messages WHERE message_id = 1`); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM context_items`).Scan(&count); err != nil {
		t.Fatalf("count context items: %v", err)
	}
	if count != 0 {
		t.Fatalf("context items after message delete = %d, want 0 (cascade)", count)
	}
}

func TestSchemaMaintainsConversationUpdatedAt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:updated-at")
	if _, err := s.db.ExecContext(ctx,
		"UPDATE conversations SET updated_at='2000-01-01 00:00:00' WHERE conversation_id=?",
		conv.ConversationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMessagesBatch(ctx, conv.ConversationID,
		[]Message{{Role: "user", Content: "x", TokenCount: 1}}); err != nil {
		t.Fatal(err)
	}
	var updated string
	err := s.db.QueryRowContext(ctx,
		"SELECT updated_at FROM conversations WHERE conversation_id=?",
		conv.ConversationID).Scan(&updated)
	if err != nil {
		t.Fatal(err)
	}
	if updated == "2000-01-01 00:00:00" {
		t.Fatal("messages insert did not maintain conversations.updated_at")
	}
}

func TestIsBusyErrorRecognizesTransientMessages(t *testing.T) {
	if !isBusyError(errors.New("database is locked")) {
		t.Fatal("database is locked should be recognized as busy")
	}
	if isBusyError(errors.New("no such table")) {
		t.Fatal("unrelated error must not be treated as busy")
	}
}

func TestProcessLockRejectsSecondEngineAndReleasesOnClose(t *testing.T) {
	path := t.TempDir() + "/seahorse.db"
	first, err := NewEngine(Config{DBPath: path}, nil)
	if err != nil {
		t.Fatalf("first engine: %v", err)
	}
	if _, secondErr := NewEngine(Config{DBPath: path}, nil); secondErr == nil {
		first.Close()
		t.Fatal("second engine unexpectedly acquired the same database")
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close first engine: %v", closeErr)
	}
	second, err := NewEngine(Config{DBPath: path}, nil)
	if err != nil {
		t.Fatalf("engine after release: %v", err)
	}
	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("close second engine: %v", closeErr)
	}
}

func TestFTSUsesExternalContent(t *testing.T) {
	db := openTestDB(t)
	if err := runSchema(db); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	for table, content := range map[string]string{
		"messages_fts":  "messages",
		"summaries_fts": "summaries",
	} {
		var ddl string
		if err := db.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&ddl); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if !strings.Contains(strings.ToLower(ddl), "content='"+content+"'") {
			t.Errorf("%s is not external content: %s", table, ddl)
		}
	}
}

func TestSummarySearchReturnsDepthAndShortQueryFallsBack(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:search-features")
	if _, err := s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: conv.ConversationID,
		Kind:           SummaryKindLeaf,
		Depth:          2,
		Content:        "go database replica",
		TokenCount:     5,
	}); err != nil {
		t.Fatal(err)
	}

	results, err := s.SearchSummaries(ctx, SearchInput{
		Pattern:        "database",
		ConversationID: conv.ConversationID,
	})
	if err != nil {
		t.Fatalf("SearchSummaries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Depth != 2 {
		t.Fatalf("depth = %d, want 2", results[0].Depth)
	}

	// Two characters cannot be matched by the trigram tokenizer; the summary
	// path must still find it through LIKE.
	results, err = s.SearchSummaries(ctx, SearchInput{
		Pattern:        "go",
		ConversationID: conv.ConversationID,
	})
	if err != nil {
		t.Fatalf("short SearchSummaries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("short query results = %d, want 1", len(results))
	}
}

func TestMessageSearchSnippetIsCapped(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:snippet")
	content := "needle " + strings.Repeat("payload ", 1000)
	if _, err := s.AddMessage(ctx, conv.ConversationID, "tool", content, 1000); err != nil {
		t.Fatal(err)
	}
	results, err := s.SearchMessages(ctx, SearchInput{
		Pattern:        "needle",
		ConversationID: conv.ConversationID,
		Limit:          5,
	})
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if len(results[0].Snippet) >= len(content) {
		t.Fatalf(
			"snippet was not capped: %d bytes (full %d)",
			len(results[0].Snippet),
			len(content),
		)
	}
}
