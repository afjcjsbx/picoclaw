package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
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

type issue3445NativeSearchLightProvider struct {
	issue3445FailingLightProvider
}

func (p *issue3445NativeSearchLightProvider) SupportsNativeSearch() bool { return true }

type issue3445WebSearchTool struct{}

func (issue3445WebSearchTool) Name() string        { return "web_search" }
func (issue3445WebSearchTool) Description() string { return "client-side web search" }
func (issue3445WebSearchTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (issue3445WebSearchTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.SilentResult("ok")
}

type issue3445CapturePrimaryProvider struct {
	tools []providers.ToolDefinition
	opts  map[string]any
}

func (p *issue3445CapturePrimaryProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	toolDefs []providers.ToolDefinition,
	_ string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.tools = append([]providers.ToolDefinition(nil), toolDefs...)
	p.opts = shallowCloneLLMOptions(opts)
	return &providers.LLMResponse{Content: "primary answer", FinishReason: "stop"}, nil
}

func (p *issue3445CapturePrimaryProvider) GetDefaultModel() string { return "primary-model" }

func TestPipeline_CallLLM_LightFallbackRecomputesNativeSearchForPrimary(t *testing.T) {
	primary := &issue3445CapturePrimaryProvider{}
	light := &issue3445NativeSearchLightProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, primary)
	defer cleanup()

	agent.Tools.Register(issue3445WebSearchTool{})
	al.cfg.Tools.Web.Enabled = true
	al.cfg.Tools.Web.PreferNative = true
	configureIssue3445Routing(agent, light, 0, 99)

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-light-fallback-native"), turnEventScope{
		turnID:  "turn-light-fallback-native",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}
	if !exec.usedLight {
		t.Fatal("expected the turn to start on the light tier")
	}

	if _, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1); err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}

	if _, ok := primary.opts["native_search"]; ok {
		t.Fatalf("primary does not support native search, got opts %#v", primary.opts)
	}
	hasWebSearch := false
	for _, td := range primary.tools {
		hasWebSearch = hasWebSearch || td.Function.Name == "web_search"
	}
	if !hasWebSearch {
		t.Fatalf("primary should receive the client web_search tool, got %#v", primary.tools)
	}
}

type issue3445ErrLightProvider struct {
	issue3445FailingLightProvider
	err error
}

func (p *issue3445ErrLightProvider) Chat(
	ctx context.Context,
	msgs []providers.Message,
	defs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	_, _ = p.issue3445FailingLightProvider.Chat(ctx, msgs, defs, model, opts)
	return nil, p.err
}

func TestPipeline_CallLLM_NoPrimaryFallbackWhenCanceled(t *testing.T) {
	canceledTurnCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		turnCtx context.Context
		lightEr error
	}{
		{"provider call canceled", context.Background(), context.Canceled},
		{"turn context canceled", canceledTurnCtx, errors.New("light model unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := &issue3445FinalProvider{content: "primary answer"}
			light := &issue3445ErrLightProvider{err: tt.lightEr}
			al, agent, cleanup := newTurnCoordTestLoop(t, primary)
			defer cleanup()
			configureIssue3445Routing(agent, light, 0, 99)

			pipeline := NewPipeline(al)
			ts := newTurnState(agent, makeTestProcessOpts("test-light-canceled"), turnEventScope{
				turnID:  "turn-light-canceled",
				context: newTurnContext(nil, nil, nil),
			})
			exec, err := pipeline.SetupTurn(context.Background(), ts)
			if err != nil {
				t.Fatalf("SetupTurn failed: %v", err)
			}

			if _, err := pipeline.CallLLM(context.Background(), tt.turnCtx, ts, exec, 1); err == nil {
				t.Fatal("expected the cancellation error to surface")
			}
			if got := primary.callCount(); got != 0 {
				t.Fatalf("primary calls = %d, want 0", got)
			}
			if !exec.usedLight {
				t.Fatal("exec must stay on the light tier when the call was canceled")
			}
		})
	}
}
