package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/session"
)

func TestComputeContextUsageWithHistoryTokens(t *testing.T) {
	agent := &AgentInstance{
		ContextWindow:         1000,
		MaxTokens:             100,
		SummarizeTokenPercent: 50,
		Sessions:              session.NewSessionManager(""),
	}

	usage := computeContextUsageWithHistoryTokens(agent, "", 123)
	if usage == nil {
		t.Fatal("computeContextUsageWithHistoryTokens() = nil")
	}
	if usage.HistoryTokens != 123 {
		t.Fatalf("HistoryTokens = %d, want 123", usage.HistoryTokens)
	}
	if usage.UsedTokens != 123 {
		t.Fatalf("UsedTokens = %d, want 123", usage.UsedTokens)
	}
	if usage.CompressAtTokens != 900 {
		t.Fatalf("CompressAtTokens = %d, want 900", usage.CompressAtTokens)
	}
	if usage.SummarizeAtTokens != 500 {
		t.Fatalf("SummarizeAtTokens = %d, want 500", usage.SummarizeAtTokens)
	}
}
