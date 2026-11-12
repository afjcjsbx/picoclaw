package agent

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/routing"
)

type issue3445ToolProvider struct {
	mu                sync.Mutex
	calls             int
	usagePromptTokens int
}

func (p *issue3445ToolProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()

	resp := &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:        fmt.Sprintf("light-call-%d", n),
			Name:      "missing_test_tool",
			Arguments: map[string]any{"attempt": n},
		}},
		FinishReason: "tool_calls",
	}
	if p.usagePromptTokens > 0 {
		resp.Usage = &providers.UsageInfo{PromptTokens: p.usagePromptTokens}
	}
	return resp, nil
}

func (p *issue3445ToolProvider) GetDefaultModel() string { return "light-model" }

func (p *issue3445ToolProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type issue3445FinalProvider struct {
	mu      sync.Mutex
	calls   int
	content string
	usage   *providers.UsageInfo
}

func (p *issue3445FinalProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.LLMResponse{Content: p.content, FinishReason: "stop", Usage: p.usage}, nil
}

func (p *issue3445FinalProvider) GetDefaultModel() string { return "primary-model" }

func (p *issue3445FinalProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func configureIssue3445Routing(
	agent *AgentInstance,
	light providers.LLMProvider,
	promptTokens, toolIterations int,
) {
	agent.Model = "primary-model"
	agent.Candidates = []providers.FallbackCandidate{{
		Provider: "test", Model: "primary-model", DisplayName: "primary-model",
	}}
	agent.LightProvider = light
	agent.LightCandidates = []providers.FallbackCandidate{{
		Provider: "test", Model: "light-model", DisplayName: "light-model",
	}}
	agent.Router = routing.New(routing.RouterConfig{
		LightModel:               "light-model",
		Threshold:                1,
		EscalationPromptTokens:   promptTokens,
		EscalationToolIterations: toolIterations,
	})
}

func TestRunTurn_EscalationKeepsUsagePerModel(t *testing.T) {
	primary := &issue3445FinalProvider{
		content: "primary answer",
		usage:   &providers.UsageInfo{PromptTokens: 500, CompletionTokens: 20, TotalTokens: 520, Requests: 1},
	}
	light := &issue3445ToolProvider{usagePromptTokens: 100}
	al, agent, cleanup := newTurnCoordTestLoop(t, primary)
	defer cleanup()

	configureIssue3445Routing(agent, light, 0, 3)
	agent.MaxIterations = 5

	ts := newTurnState(agent, makeTestProcessOpts("test-escalation-usage"), turnEventScope{
		turnID:  "turn-escalation-usage",
		context: newTurnContext(nil, nil, nil),
	})
	if _, err := al.runTurn(context.Background(), ts, NewPipeline(al)); err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}

	history, err := agent.Sessions.GetHistory(context.Background(), ts.sessionKey)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}
	var turnUsage *providers.UsageInfo
	for _, msg := range history {
		if msg.Role == "assistant" && msg.Content == "primary answer" {
			turnUsage = msg.Usage
		}
	}
	if turnUsage == nil {
		t.Fatal("final assistant message has no usage")
	}
	if turnUsage.Requests != 4 || turnUsage.PromptTokens != 800 {
		t.Fatalf("turn usage = %d calls, %d prompt tokens; want 4 calls and 800 prompt tokens",
			turnUsage.Requests, turnUsage.PromptTokens)
	}
	want := []providers.ModelUsage{
		{Model: "light-model", Usage: providers.UsageInfo{Requests: 3, PromptTokens: 300}},
		{
			Model: "primary-model",
			Usage: providers.UsageInfo{Requests: 1, PromptTokens: 500, CompletionTokens: 20, TotalTokens: 520},
		},
	}
	if !reflect.DeepEqual(turnUsage.ByModel, want) {
		t.Fatalf("ByModel = %+v, want %+v", turnUsage.ByModel, want)
	}
}

func TestRunTurn_EscalatesLightModelAfterToolStreak(t *testing.T) {
	primary := &issue3445FinalProvider{content: "primary answer"}
	light := &issue3445ToolProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, primary)
	defer cleanup()

	configureIssue3445Routing(agent, light, 0, 3)
	agent.MaxIterations = 5

	ts := newTurnState(agent, makeTestProcessOpts("test-escalation-tools"), turnEventScope{
		turnID:  "turn-escalation-tools",
		context: newTurnContext(nil, nil, nil),
	})
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}

	if got, want := light.callCount(), 3; got != want {
		t.Fatalf("light calls = %d, want %d", got, want)
	}
	if got, want := primary.callCount(), 1; got != want {
		t.Fatalf("primary calls = %d, want %d", got, want)
	}
	if got, want := result.finalContent, "primary answer"; got != want {
		t.Fatalf("final content = %q, want %q", got, want)
	}

	history, err := agent.Sessions.GetHistory(context.Background(), ts.sessionKey)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}
	sawLightToolCall := false
	for _, msg := range history {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		sawLightToolCall = true
		if msg.ModelName != "light-model" {
			t.Fatalf("light tool-call message model = %q, want light-model", msg.ModelName)
		}
	}
	if !sawLightToolCall {
		t.Fatal("expected persisted light-model tool calls")
	}
}

func TestRunTurn_EscalatesLightModelOnPromptTokens(t *testing.T) {
	primary := &issue3445FinalProvider{content: "primary answer"}
	light := &issue3445ToolProvider{usagePromptTokens: 50000}
	al, agent, cleanup := newTurnCoordTestLoop(t, primary)
	defer cleanup()

	configureIssue3445Routing(agent, light, 1000, 99)
	agent.MaxIterations = 5

	ts := newTurnState(agent, makeTestProcessOpts("test-escalation-tokens"), turnEventScope{
		turnID:  "turn-escalation-tokens",
		context: newTurnContext(nil, nil, nil),
	})
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}

	if got, want := light.callCount(), 1; got != want {
		t.Fatalf("light calls = %d, want %d", got, want)
	}
	if got, want := primary.callCount(), 1; got != want {
		t.Fatalf("primary calls = %d, want %d", got, want)
	}
	if got, want := result.finalContent, "primary answer"; got != want {
		t.Fatalf("final content = %q, want %q", got, want)
	}
}
