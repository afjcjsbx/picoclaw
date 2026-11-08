package seahorse

import (
	"context"
	"strings"
	"testing"
)

func addAssemblerToolTail(
	t *testing.T,
	s *Store,
	convID int64,
	startOrdinal int,
	count, tokens int,
) []ContextItem {
	t.Helper()
	items := make([]ContextItem, 0, count)
	for i := 0; i < count; i++ {
		msg, err := s.AddMessage(
			context.Background(),
			convID,
			"tool",
			"bulk tool output "+strings.Repeat("t", tokens*2),
			tokens,
		)
		if err != nil {
			t.Fatal(err)
		}
		items = append(items, ContextItem{
			Ordinal:    startOrdinal + i*100,
			ItemType:   "message",
			MessageID:  msg.ID,
			TokenCount: tokens,
		})
	}
	return items
}

func TestAssemblerKeepsGeneratedSummaryWithToolHeavyTail(t *testing.T) {
	s, convID := setupAssemblerStore(t)
	ctx := context.Background()

	summary, err := s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: convID,
		Kind:           SummaryKindLeaf,
		Content:        "DECISION: keep the generated summary",
		TokenCount:     2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.AddMessage(ctx, convID, "user", "inspect everything", 5)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]ContextItem, 0, FreshTailCount+1)
	items = append(items,
		ContextItem{Ordinal: 100, ItemType: "summary", SummaryID: summary.SummaryID, TokenCount: 2000},
		ContextItem{Ordinal: 200, ItemType: "message", MessageID: user.ID, TokenCount: 5},
	)
	items = append(items, addAssemblerToolTail(t, s, convID, 300, FreshTailCount-1, 8000)...)
	if err = s.UpsertContextItems(ctx, convID, items); err != nil {
		t.Fatal(err)
	}

	result, err := (&Assembler{store: s}).Assemble(ctx, convID, AssembleInput{Budget: 200000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Summary, "DECISION: keep the generated summary") {
		t.Fatalf("generated summary was evicted: %q", result.Summary)
	}
	if result.TokensUsed > 200000 {
		t.Fatalf("TokensUsed = %d, want <= 200000", result.TokensUsed)
	}
	if !containsMessage(result.Messages, "inspect everything") {
		t.Fatal("latest user instruction was evicted")
	}
}

func TestAssemblerKeepsLatestUserOutsideFreshTail(t *testing.T) {
	s, convID := setupAssemblerStore(t)
	ctx := context.Background()

	summary, err := s.CreateSummary(ctx, CreateSummaryInput{
		ConversationID: convID,
		Kind:           SummaryKindLeaf,
		Content:        "DECISION: keep the generated summary",
		TokenCount:     2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]ContextItem, 0, 42)
	items = append(items,
		ContextItem{Ordinal: 100, ItemType: "summary", SummaryID: summary.SummaryID, TokenCount: 2000},
	)
	for i := 0; i < 40; i++ {
		msg, addErr := s.AddMessage(ctx, convID, "assistant", "old context", 100)
		if addErr != nil {
			t.Fatal(addErr)
		}
		items = append(items, ContextItem{
			Ordinal:    200 + i*100,
			ItemType:   "message",
			MessageID:  msg.ID,
			TokenCount: 100,
		})
	}
	user, err := s.AddMessage(ctx, convID, "user", "APRI LA PR ADESSO", 100)
	if err != nil {
		t.Fatal(err)
	}
	items = append(items, ContextItem{
		Ordinal: 5000, ItemType: "message", MessageID: user.ID, TokenCount: 100,
	})
	items = append(items, addAssemblerToolTail(t, s, convID, 5100, 39, 4000)...)
	if err = s.UpsertContextItems(ctx, convID, items); err != nil {
		t.Fatal(err)
	}

	result, err := (&Assembler{store: s}).Assemble(ctx, convID, AssembleInput{Budget: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if !containsMessage(result.Messages, "APRI LA PR ADESSO") {
		t.Fatal("latest user instruction outside the normal fresh tail was evicted")
	}
	if !strings.Contains(result.Summary, "DECISION: keep the generated summary") {
		t.Fatalf("generated summary was evicted: %q", result.Summary)
	}
}

func containsMessage(messages []Message, content string) bool {
	for _, msg := range messages {
		if msg.Content == content {
			return true
		}
	}
	return false
}
