package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// cancelOnSummaryReadStore cancels the recovery context on the n-th summary
// read. The fallback reads the summary after writing the transcript archive, so
// this targets the cancellation window between the archive and the drop.
type cancelOnSummaryReadStore struct {
	session.SessionStore
	cancel       context.CancelFunc
	summaryReads int
	cancelOnRead int
}

func (s *cancelOnSummaryReadStore) GetSummary(ctx context.Context, key string) (string, error) {
	s.summaryReads++
	if s.summaryReads == s.cancelOnRead {
		s.cancel()
	}
	return s.SessionStore.GetSummary(ctx, key)
}

func TestLegacyCompact_Overflow_CancelDuringArchiveKeepsTranscript(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &cancelOnSummaryReadStore{
		SessionStore: agent.Sessions,
		cancel:       cancel,
		cancelOnRead: 2, // summarizeSession probes once, then the fallback probes.
	}
	agent.Sessions = store

	history := []providers.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "latest question"},
	}
	const sessionKey = "session-cancel-during-archive"
	if err := store.SessionStore.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	if _, ok := al.contextManager.(*legacyContextManager).forceCompression(ctx, sessionKey); ok {
		t.Fatal("expected the canceled fallback to keep the transcript intact")
	}

	kept, err := store.SessionStore.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(kept) != len(history) {
		t.Fatalf("history changed after cancellation: got %d messages, want %d", len(kept), len(history))
	}
}

type summaryFailingStore struct {
	session.SessionStore
	err error
}

func (s *summaryFailingStore) SetSummary(context.Context, string, string) error {
	return s.err
}

func TestLegacyCompact_Overflow_RefusesDropWhenSummaryWriteFails(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	store := &summaryFailingStore{
		SessionStore: agent.Sessions,
		err:          errors.New("summary write failed"),
	}
	agent.Sessions = store

	history := []providers.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "latest question"},
	}
	const sessionKey = "session-summary-write-failure"
	if err := store.SessionStore.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	if _, ok := al.contextManager.(*legacyContextManager).forceCompression(context.Background(), sessionKey); ok {
		t.Fatal("expected the fallback to be refused when the summary cannot be written")
	}

	kept, err := store.SessionStore.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(kept) != len(history) {
		t.Fatalf("history changed without the archive pointer: got %d messages, want %d", len(kept), len(history))
	}
}

type historyFailingStore struct {
	session.SessionStore
	err           error
	summaryWrites []string
}

func (s *historyFailingStore) SetSummary(ctx context.Context, key, summary string) error {
	s.summaryWrites = append(s.summaryWrites, summary)
	return s.SessionStore.SetSummary(ctx, key, summary)
}

func (s *historyFailingStore) SetHistory(context.Context, string, []providers.Message) error {
	return s.err
}

func TestLegacyCompact_Overflow_HistoryWriteFailureRestoresSummary(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	const sessionKey = "session-history-write-failure"
	const originalSummary = "OLD-SUMMARY"
	if err := agent.Sessions.SetSummary(context.Background(), sessionKey, originalSummary); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	store := &historyFailingStore{
		SessionStore: agent.Sessions,
		err:          errors.New("history write failed"),
	}
	agent.Sessions = store

	history := []providers.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "latest question"},
	}
	if err := store.SessionStore.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	if _, ok := al.contextManager.(*legacyContextManager).forceCompression(context.Background(), sessionKey); ok {
		t.Fatal("expected the fallback to be refused when the history cannot be written")
	}

	if len(store.summaryWrites) != 2 {
		t.Fatalf("summary writes = %d, want the emergency note and its rollback", len(store.summaryWrites))
	}
	if !strings.Contains(store.summaryWrites[0], "Emergency compression") {
		t.Fatalf("first summary write = %q, want the emergency note", store.summaryWrites[0])
	}
	summary, err := store.SessionStore.GetSummary(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if summary != originalSummary {
		t.Fatalf("summary = %q, want the rollback to %q", summary, originalSummary)
	}
	kept, err := store.SessionStore.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(kept) != len(history) {
		t.Fatalf("history changed without a successful write: got %d messages, want %d", len(kept), len(history))
	}
}
