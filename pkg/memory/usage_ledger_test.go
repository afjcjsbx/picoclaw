package memory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestLedgerRecordsOneEntryPerUsageMessage(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	msg := timedUsageMessage(at, "model-a", 100, 10)
	msg.Usage.CacheReadTokens = 60
	msg.Usage.ReasoningTokens = 4
	for _, m := range []providers.Message{
		msg,
		{Role: "user", Content: "no usage here"},
		timedUsageMessage(at.Add(time.Hour), "model-b", 200, 20),
	} {
		if err := store.AddFullMessage(ctx, "s", m); err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}
	}

	entries, first, err := ReadUsageLedger(store.dir, time.Time{})
	if err != nil {
		t.Fatalf("ReadUsageLedger: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (messages without usage are skipped)", len(entries))
	}
	if !first.Equal(at) {
		t.Errorf("first = %v, want %v", first, at)
	}
	e := entries[0]
	if e.Session != "s" || e.Model != "model-a" || !e.Time.Equal(at) {
		t.Errorf("entry = %+v", e)
	}
	if e.InputTokens != 100 || e.OutputTokens != 10 || e.CacheReadTokens != 60 || e.ReasoningTokens != 4 {
		t.Errorf("entry totals = %+v", e.Totals)
	}
}

func TestLedgerSplitsMessageByModel(t *testing.T) {
	store := newTestStore(t)
	msg := timedUsageMessage(time.Now(), "ignored", 0, 0)
	msg.Usage = &providers.UsageInfo{
		PromptTokens: 30, CompletionTokens: 3, TotalTokens: 33,
		ByModel: []providers.ModelUsage{
			{Model: "light", Usage: providers.UsageInfo{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}},
			{Model: "heavy", Usage: providers.UsageInfo{PromptTokens: 20, CompletionTokens: 2, TotalTokens: 22}},
		},
	}
	if err := store.AddFullMessage(context.Background(), "s", msg); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	entries, _, err := ReadUsageLedger(store.dir, time.Time{})
	if err != nil {
		t.Fatalf("ReadUsageLedger: %v", err)
	}
	if len(entries) != 2 || entries[0].Model != "light" || entries[1].Model != "heavy" {
		t.Fatalf("entries = %+v, want one per model", entries)
	}
}

func TestLedgerFiltersBySinceButKeepsFirst(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{old, recent} {
		if err := store.AddFullMessage(ctx, "s", timedUsageMessage(at, "m", 10, 1)); err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}
	}

	entries, first, err := ReadUsageLedger(store.dir, recent)
	if err != nil {
		t.Fatalf("ReadUsageLedger: %v", err)
	}
	if len(entries) != 1 || !entries[0].Time.Equal(recent) {
		t.Fatalf("entries = %+v, want only the recent one", entries)
	}
	if !first.Equal(old) {
		t.Errorf("first = %v, want %v", first, old)
	}
}

func TestLedgerSurvivesDeleteAndTruncate(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.AddFullMessage(ctx, "s", usageMessage("m", 10, 1)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}
	if err := store.TruncateHistory(ctx, "s", 0); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	if err := store.DeleteSession(ctx, "s"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	entries, _, err := ReadUsageLedger(store.dir, time.Time{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %d, err = %v; want the spend to outlive the session", len(entries), err)
	}
}

func TestLedgerSkipsMalformedLinesAndMissingFile(t *testing.T) {
	dir := t.TempDir()
	if entries, first, err := ReadUsageLedger(dir, time.Time{}); err != nil || len(entries) != 0 || !first.IsZero() {
		t.Fatalf("missing ledger = %v, %v, %v; want empty", entries, first, err)
	}

	body := `{"ts":"2026-11-20T10:00:00Z","session":"s","model":"m","requests":1,"input_tokens":5}` + "\n" +
		`{"ts":"2026-11-20T10:00:` + "\n" +
		"not json\n"
	if err := os.WriteFile(ledgerPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, _, err := ReadUsageLedger(dir, time.Time{})
	if err != nil || len(entries) != 1 || entries[0].InputTokens != 5 {
		t.Fatalf("entries = %+v, err = %v; want the one intact line", entries, err)
	}
}

func TestLedgerFileIsNotASession(t *testing.T) {
	store := newTestStore(t)
	if err := store.AddFullMessage(context.Background(), "s", usageMessage("m", 10, 1)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	if keys := store.ListSessions(); len(keys) != 1 || keys[0] != "s" {
		t.Fatalf("ListSessions = %v, want only s", keys)
	}
	entries, err := ReadSessionUsage(store.dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadSessionUsage = %d entries, err = %v; want 1", len(entries), err)
	}
}
