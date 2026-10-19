package tools

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// concurrencyTrackingTool records how many executions overlap.
type concurrencyTrackingTool struct {
	mu      sync.Mutex
	active  int
	maxSeen int
	ran     int
}

func (t *concurrencyTrackingTool) Name() string { return "slow" }

func (t *concurrencyTrackingTool) Description() string { return "tracks concurrent executions" }

func (t *concurrencyTrackingTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *concurrencyTrackingTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	t.mu.Lock()
	t.active++
	t.ran++
	if t.active > t.maxSeen {
		t.maxSeen = t.active
	}
	t.mu.Unlock()

	time.Sleep(30 * time.Millisecond)

	t.mu.Lock()
	t.active--
	t.mu.Unlock()
	return NewToolResult("ok")
}

func (t *concurrencyTrackingTool) stats() (ran, maxSeen int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran, t.maxSeen
}

// manyToolCallsProvider returns the configured tool calls on the first round
// and a plain answer afterwards.
type manyToolCallsProvider struct {
	calls []providers.ToolCall
	round int
}

func (p *manyToolCallsProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	p.round++
	if p.round == 1 {
		return &providers.LLMResponse{ToolCalls: p.calls}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *manyToolCallsProvider) GetDefaultModel() string { return "test-model" }

// TestRunToolLoop_BoundsConcurrentToolExecution verifies that a single
// assistant response requesting many tool calls cannot fan out unbounded.
func TestRunToolLoop_BoundsConcurrentToolExecution(t *testing.T) {
	const calls = MaxParallelToolCalls * 3

	registry := NewToolRegistry()
	tracker := &concurrencyTrackingTool{}
	registry.Register(tracker)

	provider := &manyToolCallsProvider{}
	for i := 0; i < calls; i++ {
		provider.calls = append(provider.calls, providers.ToolCall{
			ID:        fmt.Sprintf("call-%d", i),
			Name:      "slow",
			Arguments: map[string]any{},
		})
	}

	result, err := RunToolLoop(
		context.Background(),
		ToolLoopConfig{
			Provider:      provider,
			Model:         "test-model",
			Tools:         registry,
			MaxIterations: 3,
		},
		[]providers.Message{{Role: "user", Content: "run"}},
		"cli",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("RunToolLoop error: %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("result content = %q, want the final answer", result.Content)
	}

	ran, maxSeen := tracker.stats()
	if ran != calls {
		t.Errorf("tool executions = %d, want %d", ran, calls)
	}
	if maxSeen > MaxParallelToolCalls {
		t.Errorf("max concurrent executions = %d, want <= %d", maxSeen, MaxParallelToolCalls)
	}
	if maxSeen < 2 {
		t.Errorf("max concurrent executions = %d, tool calls should still run in parallel", maxSeen)
	}
}
