package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestCurrentContextUsage_LegacyReflectsManualCompaction(t *testing.T) {
	al := newSummarizeThresholdAgentLoop(t, 8000)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	const sessionKey = "legacy-context-usage"
	history := alternatingHistory(10, strings.Repeat("word ", 200))
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	before, beforeCount := al.currentContextUsage(agent, sessionKey)
	if before == nil {
		t.Fatal("expected usage before compaction")
	}
	if beforeCount != len(history) {
		t.Fatalf("message count before = %d, want %d", beforeCount, len(history))
	}

	if err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonManual,
		Budget:     agent.ContextWindow,
	}); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	after, afterCount := al.currentContextUsage(agent, sessionKey)
	if after == nil {
		t.Fatal("expected usage after compaction")
	}
	if afterCount >= beforeCount {
		t.Fatalf("message count after compaction = %d, want fewer than %d", afterCount, beforeCount)
	}
	if after.HistoryTokens >= before.HistoryTokens {
		t.Fatalf("history tokens after compaction = %d, want fewer than %d", after.HistoryTokens, before.HistoryTokens)
	}
	if after.UsedTokens >= before.UsedTokens {
		t.Fatalf("used tokens after compaction = %d, want fewer than %d", after.UsedTokens, before.UsedTokens)
	}
}

// When the ContextManager cannot assemble a prompt, usage must still fall back
// to the session store instead of disappearing from /context and outbound
// messages.
func TestCurrentContextUsage_FallsBackToSessionStore(t *testing.T) {
	al := newSummarizeThresholdAgentLoop(t, 8000)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	const sessionKey = "context-usage-fallback"
	history := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	al.contextManager = &failingAssembleContextManager{}

	usage, count := al.currentContextUsage(agent, sessionKey)
	if usage == nil {
		t.Fatal("expected usage from the session store fallback")
	}
	if count != len(history) {
		t.Fatalf("message count = %d, want %d", count, len(history))
	}
	if usage.HistoryTokens == 0 {
		t.Fatal("expected non-zero history tokens from the fallback")
	}
}

type failingAssembleContextManager struct{}

func (m *failingAssembleContextManager) Assemble(
	context.Context,
	*AssembleRequest,
) (*AssembleResponse, error) {
	return nil, errors.New("assemble failed")
}

func (m *failingAssembleContextManager) Compact(context.Context, *CompactRequest) error {
	return nil
}

func (m *failingAssembleContextManager) Ingest(context.Context, *IngestRequest) error {
	return nil
}

func (m *failingAssembleContextManager) Clear(context.Context, string) error {
	return nil
}
