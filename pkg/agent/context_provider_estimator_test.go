// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type reasoningReplayTestProvider struct {
	replay bool
}

func (p reasoningReplayTestProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{}, nil
}

func (p reasoningReplayTestProvider) GetDefaultModel() string { return "test-model" }

func (p reasoningReplayTestProvider) ReplaysReasoningContent() bool { return p.replay }

func TestEstimateMessageTokensForProviderSkipsStrippedReasoning(t *testing.T) {
	msg := providers.Message{
		Role:             "assistant",
		Content:          "ok",
		ReasoningContent: strings.Repeat("reasoning ", 10_000),
	}

	replayed := estimateMessageTokensForProvider(reasoningReplayTestProvider{replay: true}, msg)
	stripped := estimateMessageTokensForProvider(reasoningReplayTestProvider{replay: false}, msg)

	if replayed <= stripped {
		t.Fatalf("replayed tokens = %d, stripped = %d; reasoning should add tokens only when replayed", replayed, stripped)
	}
	if want := EstimateMessageTokens(providers.Message{Role: "assistant", Content: "ok"}); stripped != want {
		t.Fatalf("stripped tokens = %d, want content-only %d", stripped, want)
	}
}

func TestEstimatePromptTokensUsesProviderReasoningCapability(t *testing.T) {
	msg := providers.Message{
		Role:             "assistant",
		Content:          "ok",
		ReasoningContent: strings.Repeat("reasoning ", 10_000),
	}
	const threshold = 1_000

	if got := estimatePromptTokens(reasoningReplayTestProvider{replay: false}, []providers.Message{msg}, nil); got >= threshold {
		t.Fatalf("stripped-reasoning prompt = %d tokens, want below threshold %d", got, threshold)
	}
	if got := estimatePromptTokens(reasoningReplayTestProvider{replay: true}, []providers.Message{msg}, nil); got < threshold {
		t.Fatalf("replayed-reasoning prompt = %d tokens, want at least threshold %d", got, threshold)
	}
}

type recordingContextManager struct {
	compactCalls int
	lastRequest  *CompactRequest
}

func (m *recordingContextManager) Assemble(context.Context, *AssembleRequest) (*AssembleResponse, error) {
	return &AssembleResponse{}, nil
}

func (m *recordingContextManager) Compact(_ context.Context, req *CompactRequest) error {
	m.compactCalls++
	m.lastRequest = req
	return ErrNothingToCompact
}

func (m *recordingContextManager) Ingest(context.Context, *IngestRequest) error { return nil }

func (m *recordingContextManager) Clear(context.Context, string) error { return nil }

func TestCompactBeforeLLMUsesActiveTurnBoundaryAndProviderEstimate(t *testing.T) {
	agent := &AgentInstance{
		Provider:              reasoningReplayTestProvider{replay: false},
		ContextWindow:         1_000,
		MaxTokens:             0,
		SummarizeTokenPercent: 50,
		Tools:                 tools.NewToolRegistry(),
	}
	ts := &turnState{
		agent: agent,
		opts:  processOptions{EnableSummary: true},
	}
	ts.recordPersistedMessage(providers.Message{Role: "user", Content: "task"})
	ts.recordPersistedMessage(providers.Message{Role: "assistant", Content: "working"})

	exec := &turnExecution{
		messages: []providers.Message{{
			Role:             "assistant",
			Content:          "ok",
			ReasoningContent: strings.Repeat("reasoning ", 10_000),
		}},
		activeProvider: agent.Provider,
	}
	cm := &recordingContextManager{}
	pipeline := &Pipeline{ContextManager: cm}

	// The provider strips reasoning, so the prompt is below the threshold and
	// compaction must not run at all.
	pipeline.CompactBeforeLLM(context.Background(), ts, exec)
	if cm.compactCalls != 0 {
		t.Fatalf("compact calls = %d, want 0 when reasoning is stripped", cm.compactCalls)
	}

	// A provider that replays reasoning reaches the threshold. The request must
	// carry the active-turn boundary.
	agent.Provider = reasoningReplayTestProvider{replay: true}
	exec.activeProvider = agent.Provider
	pipeline.CompactBeforeLLM(context.Background(), ts, exec)
	if cm.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", cm.compactCalls)
	}
	if cm.lastRequest == nil {
		t.Fatal("expected a compaction request")
	}
	if cm.lastRequest.ActiveTurnMessages != 2 {
		t.Fatalf("ActiveTurnMessages = %d, want 2", cm.lastRequest.ActiveTurnMessages)
	}
}
