package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type issue3445SteeringProvider struct {
	al  *AgentLoop
	mu  sync.Mutex
	got int
}

func (p *issue3445SteeringProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.got++
	n := p.got
	p.mu.Unlock()

	if err := p.al.Steer(providers.Message{
		Role:    "user",
		Content: fmt.Sprintf("keep going %d", n),
	}); err != nil {
		return nil, err
	}
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:        fmt.Sprintf("steering-call-%d", n),
			Name:      "missing_test_tool",
			Arguments: map[string]any{"attempt": n},
		}},
		FinishReason: "tool_calls",
	}, nil
}

func (p *issue3445SteeringProvider) GetDefaultModel() string { return "steering-model" }

func (p *issue3445SteeringProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.got
}

func TestSteeringIterationHardCap(t *testing.T) {
	tests := []struct {
		max  int
		want int
	}{
		{max: 2, want: 22},
		{max: 10, want: 30},
		{max: 50, want: 150},
	}
	for _, tt := range tests {
		if got := steeringIterationHardCap(tt.max); got != tt.want {
			t.Fatalf("steeringIterationHardCap(%d) = %d, want %d", tt.max, got, tt.want)
		}
	}
}

func TestRunTurn_SteeringCannotExceedHardCap(t *testing.T) {
	provider := &issue3445SteeringProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	provider.al = al
	agent.MaxIterations = 2

	ts := newTurnState(agent, makeTestProcessOpts("test-steering-hard-cap"), turnEventScope{
		turnID:  "turn-steering-hard-cap",
		context: newTurnContext(nil, nil, nil),
	})
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}

	wantCalls := steeringIterationHardCap(agent.MaxIterations)
	if got := provider.callCount(); got != wantCalls {
		t.Fatalf("LLM calls = %d, want hard cap %d", got, wantCalls)
	}
	if got := ts.currentIteration(); got != wantCalls {
		t.Fatalf("iterations = %d, want hard cap %d", got, wantCalls)
	}
	if got := result.finalContent; got != toolLimitResponse {
		t.Fatalf("final content = %q, want tool-limit response", got)
	}
}

func TestRunTurn_HardCapKeepsConsumedSteeringQueued(t *testing.T) {
	provider := &issue3445SteeringProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	provider.al = al
	agent.MaxIterations = 2

	ts := newTurnState(agent, makeTestProcessOpts("test-steering-requeue"), turnEventScope{
		turnID:  "turn-steering-requeue",
		context: newTurnContext(nil, nil, nil),
	})
	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}

	// Every LLM call queued one steering message and every iteration consumed
	// one, so the message pulled off the queue by the last iteration is the one
	// the cap left behind. It must be queued again, not dropped.
	wantCalls := steeringIterationHardCap(agent.MaxIterations)
	if got := al.pendingSteeringCountForScope(ts.sessionKey); got != 1 {
		t.Fatalf("queued steering after hard cap = %d, want 1 (calls=%d)", got, wantCalls)
	}
	queued := al.dequeueSteeringMessagesForScope(ts.sessionKey)
	if want := fmt.Sprintf("keep going %d", wantCalls); len(queued) != 1 || queued[0].Content != want {
		t.Fatalf("requeued steering = %#v, want content %q", queued, want)
	}
}

// issue3445GracefulProvider keeps steering and calling tools until the hard
// cap, requests a graceful interrupt on the last capped call, and answers the
// tool-less graceful iteration.
type issue3445GracefulProvider struct {
	al       *AgentLoop
	ts       *turnState
	capCalls int
	mu       sync.Mutex
	calls    int
	toolless int
}

func (p *issue3445GracefulProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	toolDefs []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	if len(toolDefs) == 0 {
		p.toolless++
	}
	p.mu.Unlock()

	if len(toolDefs) == 0 {
		return &providers.LLMResponse{Content: "wrapped up", FinishReason: "stop"}, nil
	}
	if n == p.capCalls {
		p.ts.requestGracefulInterrupt("wrap up")
	}
	if err := p.al.Steer(providers.Message{Role: "user", Content: "keep going"}); err != nil {
		return nil, err
	}
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:        fmt.Sprintf("graceful-call-%d", n),
			Name:      "missing_test_tool",
			Arguments: map[string]any{"attempt": n},
		}},
		FinishReason: "tool_calls",
	}, nil
}

func (p *issue3445GracefulProvider) GetDefaultModel() string { return "graceful-model" }

func TestRunTurn_GracefulInterruptAtHardCapStillGetsFinalIteration(t *testing.T) {
	provider := &issue3445GracefulProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	agent.MaxIterations = 2
	provider.al = al
	provider.capCalls = steeringIterationHardCap(agent.MaxIterations)

	ts := newTurnState(agent, makeTestProcessOpts("test-graceful-at-cap"), turnEventScope{
		turnID:  "turn-graceful-at-cap",
		context: newTurnContext(nil, nil, nil),
	})
	provider.ts = ts
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}

	if got, want := provider.toolless, 1; got != want {
		t.Fatalf("tool-less graceful calls = %d, want %d", got, want)
	}
	if got, want := result.finalContent, "wrapped up"; got != want {
		t.Fatalf("final content = %q, want %q", got, want)
	}
}
