package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

type issue3445FailingLightProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *issue3445FailingLightProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return nil, errors.New("light model unavailable")
}

func (p *issue3445FailingLightProvider) GetDefaultModel() string { return "light-model" }

func (p *issue3445FailingLightProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestRunTurn_FallsBackToPrimaryWhenLightModelFails(t *testing.T) {
	primary := &issue3445FinalProvider{content: "primary answer"}
	light := &issue3445FailingLightProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, primary)
	defer cleanup()

	configureIssue3445Routing(agent, light, 0, 99)
	agent.MaxIterations = 3

	ts := newTurnState(agent, makeTestProcessOpts("test-light-fallback"), turnEventScope{
		turnID:  "turn-light-fallback",
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
