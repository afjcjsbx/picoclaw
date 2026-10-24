package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func retryDefaults() config.AgentDefaults {
	return config.AgentDefaults{
		LLMRetryOnEmptyContent:  true,
		LLMRetryOnFinishReasons: []string{"length"},
	}
}

func TestSemanticRetryError_RetriableResponses(t *testing.T) {
	tests := []struct {
		name     string
		resp     *providers.LLMResponse
		contains string
	}{
		{
			name:     "empty content",
			resp:     &providers.LLMResponse{FinishReason: "stop"},
			contains: "empty content",
		},
		{
			name:     "whitespace only content",
			resp:     &providers.LLMResponse{Content: " \n\t ", FinishReason: "stop"},
			contains: "empty content",
		},
		{
			name:     "truncated with partial content",
			resp:     &providers.LLMResponse{Content: "partial", FinishReason: "length"},
			contains: "finish_reason",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := semanticRetryError(retryDefaults(), tc.resp)
			if err == nil {
				t.Fatal("expected a retriable error, got nil")
			}
			if !errors.Is(err, errSemanticRetry) {
				t.Fatalf("error %v does not wrap errSemanticRetry", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error %q does not mention %q", err, tc.contains)
			}
		})
	}
}

func TestSemanticRetryError_UsableResponses(t *testing.T) {
	tests := []struct {
		name string
		opts config.AgentDefaults
		resp *providers.LLMResponse
	}{
		{
			name: "normal answer",
			opts: retryDefaults(),
			resp: &providers.LLMResponse{Content: "done", FinishReason: "stop"},
		},
		{
			name: "empty content when retry disabled",
			opts: config.AgentDefaults{LLMRetryOnFinishReasons: []string{"length"}},
			resp: &providers.LLMResponse{FinishReason: "stop"},
		},
		{
			name: "tool call without text",
			opts: retryDefaults(),
			resp: &providers.LLMResponse{
				FinishReason: "tool_calls",
				ToolCalls:    []providers.ToolCall{{ID: "call-1", Name: "read_file"}},
			},
		},
		{
			name: "truncated when finish reasons are not configured",
			opts: config.AgentDefaults{LLMRetryOnEmptyContent: true},
			resp: &providers.LLMResponse{Content: "partial", FinishReason: "length"},
		},
		{
			name: "nil response",
			opts: retryDefaults(),
			resp: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := semanticRetryError(tc.opts, tc.resp); err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
		})
	}
}

func TestSemanticRetryError_CustomFinishReasons(t *testing.T) {
	opts := config.AgentDefaults{
		LLMRetryOnFinishReasons: []string{"length", "max_tokens"},
	}
	for reason, want := range map[string]bool{
		"length":         true,
		"max_tokens":     true,
		"stop":           false,
		"content_filter": false,
	} {
		err := semanticRetryError(opts, &providers.LLMResponse{Content: "ok", FinishReason: reason})
		if got := err != nil; got != want {
			t.Errorf("finish_reason=%q: retry=%v, want %v (err=%v)", reason, got, want, err)
		}
	}
}

func TestTransientLLMRetryReason_ClassifiesSemanticError(t *testing.T) {
	reason, ok := transientLLMRetryReason(fmt.Errorf("wrapped: %w", errSemanticRetry))
	if !ok {
		t.Fatal("expected semantic error to be retriable")
	}
	if reason != "semantic" {
		t.Fatalf("reason = %q, want %q", reason, "semantic")
	}
}

func TestDefaultConfig_RetriesEmptyAndTruncatedResponses(t *testing.T) {
	defaults := config.DefaultConfig().Agents.Defaults
	if !defaults.LLMRetryOnEmptyContent {
		t.Error("llm_retry_on_empty_content should default to true")
	}
	if len(defaults.LLMRetryOnFinishReasons) != 1 || defaults.LLMRetryOnFinishReasons[0] != "length" {
		t.Errorf("llm_retry_on_finish_reasons = %v, want [length]", defaults.LLMRetryOnFinishReasons)
	}
}

// sequenceLLMProvider replays canned responses and counts calls.
type sequenceLLMProvider struct {
	responses []providers.LLMResponse
	mu        sync.Mutex
	calls     int
}

func (p *sequenceLLMProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := min(p.calls, len(p.responses)-1)
	p.calls++
	resp := p.responses[index]
	return &resp, nil
}

func (p *sequenceLLMProvider) GetDefaultModel() string { return "sequence-model" }

func (p *sequenceLLMProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newSemanticRetryPipeline(
	t *testing.T,
	provider providers.LLMProvider,
	retries int,
) (*Pipeline, *turnState, func()) {
	t.Helper()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:               t.TempDir(),
				ModelName:               "test-model",
				MaxTokens:               4096,
				MaxToolIterations:       10,
				MaxLLMRetries:           retries,
				LLMRetryBackoffSecs:     1,
				LLMRetryOnEmptyContent:  true,
				LLMRetryOnFinishReasons: []string{"length"},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		al.Close()
		t.Fatal("expected default agent")
	}

	ts := newTurnState(agent, makeTestProcessOpts("test-session"), turnEventScope{
		turnID:  "turn-1",
		context: newTurnContext(nil, nil, nil),
	})
	return NewPipeline(al), ts, al.Close
}

func runSemanticRetryTurn(t *testing.T, provider providers.LLMProvider, retries int) (*turnExecution, error) {
	t.Helper()

	pipeline, ts, cleanup := newSemanticRetryPipeline(t, provider, retries)
	defer cleanup()

	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}
	_, err = pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
	return exec, err
}

func TestPipeline_CallLLM_RetriesEmptyContent(t *testing.T) {
	provider := &sequenceLLMProvider{responses: []providers.LLMResponse{
		{FinishReason: "stop"},
		{Content: "recovered answer", FinishReason: "stop"},
	}}

	exec, err := runSemanticRetryTurn(t, provider, 1)
	if err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	if got := provider.callCount(); got != 2 {
		t.Fatalf("callCount = %d, want 2", got)
	}
	if exec.finalContent != "recovered answer" {
		t.Fatalf("finalContent = %q, want recovered answer", exec.finalContent)
	}
}

func TestPipeline_CallLLM_RetriesTruncatedFinishReason(t *testing.T) {
	provider := &sequenceLLMProvider{responses: []providers.LLMResponse{
		{Content: "partial", FinishReason: "length"},
		{Content: "complete answer", FinishReason: "stop"},
	}}

	exec, err := runSemanticRetryTurn(t, provider, 1)
	if err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	if got := provider.callCount(); got != 2 {
		t.Fatalf("callCount = %d, want 2", got)
	}
	if exec.finalContent != "complete answer" {
		t.Fatalf("finalContent = %q, want complete answer", exec.finalContent)
	}
}

func TestPipeline_CallLLM_KeepsEmptyResponseAfterRetryBudget(t *testing.T) {
	provider := &sequenceLLMProvider{responses: []providers.LLMResponse{
		{FinishReason: "stop"},
	}}

	exec, err := runSemanticRetryTurn(t, provider, 2)
	if err != nil {
		t.Fatalf("exhausted retries should not fail the turn, got %v", err)
	}
	if got := provider.callCount(); got != 3 {
		t.Fatalf("callCount = %d, want 3 (initial attempt + 2 retries)", got)
	}
	if exec.finalContent != "" {
		t.Fatalf("finalContent = %q, want empty", exec.finalContent)
	}
}

func TestPipeline_CallLLM_DoesNotRetryToolCallResponse(t *testing.T) {
	provider := &sequenceLLMProvider{responses: []providers.LLMResponse{
		{
			FinishReason: "tool_calls",
			ToolCalls: []providers.ToolCall{{
				ID:   "call-1",
				Name: "read_file",
			}},
		},
		{Content: "should not be reached", FinishReason: "stop"},
	}}

	_, err := runSemanticRetryTurn(t, provider, 2)
	if err != nil {
		t.Fatalf("tool call response should be accepted, got %v", err)
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("callCount = %d, want 1", got)
	}
}
