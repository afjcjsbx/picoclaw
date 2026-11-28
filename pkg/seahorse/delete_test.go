package seahorse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteSessionFromDBRemovesSessionState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "seahorse.db")
	engine, err := NewEngine(Config{DBPath: dbPath}, nil)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	store := engine.GetRetrieval().Store()
	conv, err := store.GetOrCreateConversation(ctx, "session:delete")
	if err != nil {
		t.Fatalf("GetOrCreateConversation() error = %v", err)
	}
	if _, addErr := store.AddMessage(ctx, conv.ConversationID, "user", "private message", 5); addErr != nil {
		t.Fatalf("AddMessage() error = %v", addErr)
	}
	if _, summaryErr := store.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: conv.ConversationID,
		Kind:           SummaryKindLeaf,
		Content:        "private summary",
		TokenCount:     5,
	}); summaryErr != nil {
		t.Fatalf("CreateSummary() error = %v", summaryErr)
	}
	other, err := store.GetOrCreateConversation(ctx, "session:keep")
	if err != nil {
		t.Fatalf("GetOrCreateConversation(other) error = %v", err)
	}
	if _, addErr := store.AddMessage(ctx, other.ConversationID, "user", "other message", 5); addErr != nil {
		t.Fatalf("AddMessage(other) error = %v", addErr)
	}

	// Keep the engine open to verify the one-shot delete works while the
	// gateway owns the Seahorse process lock.
	if deleteErr := DeleteSessionFromDB(ctx, dbPath, "session:delete"); deleteErr != nil {
		t.Fatalf("DeleteSessionFromDB() error = %v", deleteErr)
	}

	deleted, err := store.GetConversationBySessionKey(ctx, "session:delete")
	if err != nil {
		t.Fatalf("GetConversationBySessionKey(deleted) error = %v", err)
	}
	if deleted != nil {
		t.Fatalf("deleted conversation still present: %#v", deleted)
	}
	messages, err := store.GetMessages(ctx, conv.ConversationID, 10, 0)
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("deleted messages still present: %#v", messages)
	}
	summaries, err := store.GetSummariesByConversation(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("GetSummariesByConversation() error = %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("deleted summaries still present: %#v", summaries)
	}
	messageMatches, err := store.SearchMessages(ctx, SearchInput{Pattern: "private"})
	if err != nil {
		t.Fatalf("SearchMessages() error = %v", err)
	}
	if len(messageMatches) != 0 {
		t.Fatalf("deleted messages remain in FTS: %#v", messageMatches)
	}
	summaryMatches, err := store.SearchSummaries(ctx, SearchInput{Pattern: "private"})
	if err != nil {
		t.Fatalf("SearchSummaries() error = %v", err)
	}
	if len(summaryMatches) != 0 {
		t.Fatalf("deleted summaries remain in FTS: %#v", summaryMatches)
	}
	kept, err := store.GetConversationBySessionKey(ctx, "session:keep")
	if err != nil {
		t.Fatalf("GetConversationBySessionKey(kept) error = %v", err)
	}
	if kept == nil {
		t.Fatal("unrelated conversation was deleted")
	}
}

func TestDeleteSessionFromDBAbsentDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	if err := DeleteSessionFromDB(context.Background(), dbPath, "session:missing"); err != nil {
		t.Fatalf("DeleteSessionFromDB() error = %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("DeleteSessionFromDB() created a missing database: %v", err)
	}
}
