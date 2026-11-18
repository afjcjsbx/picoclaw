package seahorse

import (
	"context"
	"errors"
	"testing"
)

func TestApplyLeafSummaryIsAtomicAndRejectsStaleCandidates(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, err := s.GetOrCreateConversation(ctx, "test:apply-leaf")
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	msgs := make([]Message, 8)
	for i := range msgs {
		msgs[i] = Message{Role: "user", Content: "message", TokenCount: 10}
	}
	ids, err := s.AddMessagesBatch(ctx, conv.ConversationID, msgs)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	items, err := s.GetContextItems(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("items: %v", err)
	}
	if len(items) != len(ids) {
		t.Fatalf("items = %d, want %d", len(items), len(ids))
	}
	version, err := s.GetConversationVersion(ctx, conv.ConversationID)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	in := ApplyLeafInput{
		ConversationID:  conv.ConversationID,
		ExpectedVersion: version,
		StartOrdinal:    items[0].Ordinal,
		EndOrdinal:      items[len(items)-1].Ordinal,
		MessageIDs:      ids,
		Summary: CreateSummaryInput{
			Kind:       SummaryKindLeaf,
			Content:    "summary",
			TokenCount: 5,
		},
	}
	summary, err := s.ApplyLeafSummary(ctx, in)
	if err != nil {
		t.Fatalf("ApplyLeafSummary: %v", err)
	}
	if summary == nil || summary.SummaryID == "" {
		t.Fatal("expected summary")
	}
	items, _ = s.GetContextItems(ctx, conv.ConversationID)
	if len(items) != 1 || items[0].SummaryID != summary.SummaryID {
		t.Fatalf("context after apply = %+v, want one summary", items)
	}
	afterVersion, _ := s.GetConversationVersion(ctx, conv.ConversationID)
	if afterVersion <= version {
		t.Fatalf("version = %d, want > %d", afterVersion, version)
	}

	// A second apply with the old version must fail without creating another
	// summary row.
	before, _ := s.GetSummariesByConversation(ctx, conv.ConversationID)
	if _, err := s.ApplyLeafSummary(ctx, in); !errors.Is(err, ErrStaleCandidates) {
		t.Fatalf("stale ApplyLeafSummary error = %v, want ErrStaleCandidates", err)
	}
	after, _ := s.GetSummariesByConversation(ctx, conv.ConversationID)
	if len(after) != len(before) {
		t.Fatalf("stale apply created summary: before=%d after=%d", len(before), len(after))
	}
}

func TestApplyCondensedSummaryRejectsMissingCandidatesWithoutOrphan(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:apply-condensed")
	convID := conv.ConversationID

	var candidates []string
	for i := 0; i < CondensedMinFanout; i++ {
		sum, err := s.CreateSummary(ctx, CreateSummaryInput{
			ConversationID: convID,
			Kind:           SummaryKindLeaf,
			Content:        "leaf",
			TokenCount:     500,
		})
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		candidates = append(candidates, sum.SummaryID)
		if err := s.AppendContextSummary(ctx, convID, sum.SummaryID); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	version, _ := s.GetConversationVersion(ctx, convID)
	// Remove one candidate after selection; the apply must not create a row.
	if err := s.ReplaceContextItemsWithSummary(ctx, convID, []string{candidates[0]}, candidates[0]); err != nil {
		t.Fatalf("remove candidate: %v", err)
	}
	before, _ := s.GetSummariesByConversation(ctx, convID)
	_, err := s.ApplyCondensedSummary(ctx, ApplyCondensedInput{
		ConversationID:      convID,
		ExpectedVersion:     version,
		CandidateSummaryIDs: candidates,
		InsertBeforeOrdinal: 1,
		Summary: CreateSummaryInput{
			Kind:       SummaryKindCondensed,
			Content:    "condensed",
			TokenCount: 200,
			ParentIDs:  candidates,
		},
	})
	if !errors.Is(err, ErrStaleCandidates) {
		t.Fatalf("error = %v, want ErrStaleCandidates", err)
	}
	after, _ := s.GetSummariesByConversation(ctx, convID)
	if len(after) != len(before) {
		t.Fatalf(
			"stale condensed apply created a summary: before=%d after=%d",
			len(before),
			len(after),
		)
	}
}

func TestConversationVersionBumpsOnStructuralMutations(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	conv, _ := s.GetOrCreateConversation(ctx, "test:version")
	v0, _ := s.GetConversationVersion(ctx, conv.ConversationID)
	if _, err := s.AddMessagesBatch(ctx, conv.ConversationID,
		[]Message{{Role: "user", Content: "x", TokenCount: 1}}); err != nil {
		t.Fatal(err)
	}
	v1, _ := s.GetConversationVersion(ctx, conv.ConversationID)
	if v1 != v0 {
		t.Fatalf("append changed context version: %d -> %d", v0, v1)
	}
	if err := s.ClearContextItems(ctx, conv.ConversationID); err != nil {
		t.Fatal(err)
	}
	v2, _ := s.GetConversationVersion(ctx, conv.ConversationID)
	if v2 <= v1 {
		t.Fatalf("structural clear did not bump version: %d -> %d", v1, v2)
	}
}
