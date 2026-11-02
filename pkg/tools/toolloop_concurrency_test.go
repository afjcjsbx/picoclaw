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

func (t *concurrencyTrackingTool) ParallelSafety(map[string]any) ParallelSafety {
	return ParallelSafety{Safe: true}
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
	const calls = maxParallelToolCalls * 3

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
	if maxSeen > maxParallelToolCalls {
		t.Errorf("max concurrent executions = %d, want <= %d", maxSeen, maxParallelToolCalls)
	}
	if maxSeen < 2 {
		t.Errorf("max concurrent executions = %d, tool calls should still run in parallel", maxSeen)
	}
}

// plainTrackingTool hides the embedded ParallelSafety method.
type plainTrackingTool struct{ inner *concurrencyTrackingTool }

func (t plainTrackingTool) Name() string               { return "plain" }
func (t plainTrackingTool) Description() string        { return t.inner.Description() }
func (t plainTrackingTool) Parameters() map[string]any { return t.inner.Parameters() }
func (t plainTrackingTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.inner.Execute(ctx, args)
}

func runToolLoopWithCalls(
	t *testing.T,
	registry *ToolRegistry,
	maxParallel int,
	calls []providers.ToolCall,
) []providers.Message {
	t.Helper()
	var final []providers.Message
	provider := &manyToolCallsProvider{calls: calls}
	_, err := RunToolLoop(
		context.Background(),
		ToolLoopConfig{
			Provider:      &capturingProvider{manyToolCallsProvider: provider, out: &final},
			Model:         "test-model",
			Tools:         registry,
			MaxIterations: 3,
			MaxParallel:   maxParallel,
		},
		[]providers.Message{{Role: "user", Content: "run"}},
		"cli",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("RunToolLoop error: %v", err)
	}
	return final
}

// capturingProvider records the messages of the last LLM call.
type capturingProvider struct {
	*manyToolCallsProvider
	out *[]providers.Message
}

func (p *capturingProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	*p.out = append([]providers.Message(nil), messages...)
	return p.manyToolCallsProvider.Chat(ctx, messages, tools, model, options)
}

// TestRunToolLoop_UndeclaredToolsRunSequentially verifies that tools without a
// ParallelAware declaration are barriers even though RunToolLoop is parallel.
func TestRunToolLoop_UndeclaredToolsRunSequentially(t *testing.T) {
	registry := NewToolRegistry()
	tracker := &concurrencyTrackingTool{}
	registry.Register(plainTrackingTool{inner: tracker})

	calls := make([]providers.ToolCall, 4)
	for i := range calls {
		calls[i] = providers.ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "plain", Arguments: map[string]any{}}
	}
	runToolLoopWithCalls(t, registry, 0, calls)

	if ran, maxSeen := tracker.stats(); ran != 4 || maxSeen != 1 {
		t.Errorf("ran=%d maxSeen=%d, want 4 executions that never overlap", ran, maxSeen)
	}
}

// TestRunToolLoop_ResultsKeepCallOrder verifies the tool messages follow the
// model's call order even though the first call finishes last.
func TestRunToolLoop_ResultsKeepCallOrder(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&delayByArgTool{})

	messages := runToolLoopWithCalls(t, registry, 0, []providers.ToolCall{
		{ID: "slow", Name: "delay", Arguments: map[string]any{"ms": float64(60)}},
		{ID: "fast", Name: "delay", Arguments: map[string]any{"ms": float64(1)}},
	})

	var order []string
	for _, m := range messages {
		if m.Role == "tool" {
			order = append(order, m.ToolCallID)
		}
	}
	if len(order) != 2 || order[0] != "slow" || order[1] != "fast" {
		t.Errorf("tool message order = %v, want [slow fast]", order)
	}
}

type delayByArgTool struct{}

func (delayByArgTool) Name() string        { return "delay" }
func (delayByArgTool) Description() string { return "sleeps for ms" }
func (delayByArgTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"ms": map[string]any{"type": "number"}},
	}
}

func (delayByArgTool) Execute(_ context.Context, args map[string]any) *ToolResult {
	ms, _ := args["ms"].(float64)
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return NewToolResult("done")
}

func (delayByArgTool) ParallelSafety(map[string]any) ParallelSafety {
	return ParallelSafety{Safe: true}
}
