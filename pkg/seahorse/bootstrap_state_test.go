package seahorse

import (
	"context"
	"testing"
)

func TestBootstrapEmptyHistoryClearsStaleRows(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	sessionKey := "test:bootstrap-empty"
	if _, err := e.Ingest(ctx, sessionKey, []Message{{Role: "user", Content: "stale", TokenCount: 3}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := e.Bootstrap(ctx, sessionKey, nil); err != nil {
		t.Fatalf("bootstrap empty: %v", err)
	}
	conv, _ := e.store.GetConversationBySessionKey(ctx, sessionKey)
	if conv == nil {
		t.Fatal("expected conversation row to remain")
	}
	msgs, _ := e.store.GetMessages(ctx, conv.ConversationID, 10, 0)
	if len(msgs) != 0 {
		t.Fatalf("messages after empty bootstrap = %d, want 0", len(msgs))
	}
	items, _ := e.store.GetContextItems(ctx, conv.ConversationID)
	if len(items) != 0 {
		t.Fatalf("context items after empty bootstrap = %d, want 0", len(items))
	}
}

func TestBootstrapTruncatesDatabaseTailNotPresentInHistory(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	sessionKey := "test:bootstrap-tail"
	full := []Message{
		{Role: "user", Content: "one", TokenCount: 1},
		{Role: "assistant", Content: "two", TokenCount: 1},
		{Role: "user", Content: "three", TokenCount: 1},
	}
	if err := e.Bootstrap(ctx, sessionKey, full); err != nil {
		t.Fatal(err)
	}
	if err := e.Bootstrap(ctx, sessionKey, full[:2]); err != nil {
		t.Fatal(err)
	}
	conv, _ := e.store.GetConversationBySessionKey(ctx, sessionKey)
	msgs, _ := e.store.GetMessages(ctx, conv.ConversationID, 10, 0)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 after truncating JSONL", len(msgs))
	}
	items, _ := e.store.GetContextItems(ctx, conv.ConversationID)
	if len(items) != 2 {
		t.Fatalf("context items = %d, want 2", len(items))
	}
}

func TestIngestWithExternalIDIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:external-id")
	msg := Message{Role: "user", Content: "once", TokenCount: 1, ExternalID: "jsonl:0:abc"}
	if _, err := s.AddMessagesBatch(ctx, conv.ConversationID, []Message{msg}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMessagesBatch(ctx, conv.ConversationID, []Message{msg}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.GetMessages(ctx, conv.ConversationID, 10, 0)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d, want 1 after idempotent retry", len(msgs))
	}
	items, _ := s.GetContextItems(ctx, conv.ConversationID)
	if len(items) != 1 {
		t.Fatalf("context items = %d, want 1 after idempotent retry", len(items))
	}
}

func TestDeleteMessagesAfterIDInvalidatesSummaries(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:delete-derived")
	msgs := []Message{
		{Role: "user", Content: "a", TokenCount: 1},
		{Role: "assistant", Content: "b", TokenCount: 1},
	}
	ids, _ := s.AddMessagesBatch(ctx, conv.ConversationID, msgs)
	sum, err := s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: conv.ConversationID,
		Kind:           SummaryKindLeaf,
		Content:        "derived",
		TokenCount:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.LinkSummaryToMessages(ctx, sum.SummaryID, ids); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendContextSummary(ctx, conv.ConversationID, sum.SummaryID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessagesAfterID(ctx, conv.ConversationID, ids[0]); err != nil {
		t.Fatal(err)
	}
	sums, _ := s.GetSummariesByConversation(ctx, conv.ConversationID)
	if len(sums) != 0 {
		t.Fatalf("summaries after message deletion = %d, want 0", len(sums))
	}
	items, _ := s.GetContextItems(ctx, conv.ConversationID)
	if len(items) != 0 {
		t.Fatalf("context items after message deletion = %d, want 0", len(items))
	}
}
