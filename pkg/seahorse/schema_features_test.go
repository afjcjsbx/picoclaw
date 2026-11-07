package seahorse

import (
	"context"
	"errors"
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
