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
