package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type sessionModelFixture struct {
	t                       *testing.T
	al                      *AgentLoop
	baseAgent               *AgentInstance
	selfTool                *SelfTool
	localCalls, remoteCalls int
	localModel, remoteModel string
}

func newSessionModelFixture(t *testing.T) *sessionModelFixture {
	t.Helper()
	f := &sessionModelFixture{t: t}
	localServer := newChatCompletionTestServer(t, "local", "local reply", &f.localCalls, &f.localModel)
	t.Cleanup(localServer.Close)
	remoteServer := newChatCompletionTestServer(t, "remote", "remote reply", &f.remoteCalls, &f.remoteModel)
	t.Cleanup(remoteServer.Close)

	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Workspace: t.TempDir(), Provider: "openai", ModelName: "local",
			MaxTokens: 4096, MaxToolIterations: 10,
		}},
		Tools: config.ToolsConfig{Self: config.SelfToolConfig{Enable: true, AllowSet: true}},
		ModelList: []*config.ModelConfig{
			{
				ModelName: "local", Model: "openai/local-model", APIBase: localServer.URL,
				APIKeys: config.SimpleSecureStrings("local-key"),
			},
			{
				ModelName: "remote", Model: "openrouter/deepseek/deepseek-v3.2", APIBase: remoteServer.URL,
				APIKeys: config.SimpleSecureStrings("remote-key"),
			},
		},
	}
	provider, _, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider(): %v", err)
	}
	f.al = NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	t.Cleanup(f.al.Close)
	f.baseAgent = f.al.GetRegistry().GetDefaultAgent()
	toolValue, ok := f.baseAgent.Tools.Get("self")
	if !ok {
		t.Fatal("self tool was not registered")
	}
	if f.selfTool, ok = toolValue.(*SelfTool); !ok {
		t.Fatalf("self tool has type %T", toolValue)
	}
	return f
}

func (f *sessionModelFixture) callTurn(sessionKey string) string {
	f.t.Helper()
	response, err := f.al.runAgentLoop(context.Background(), f.baseAgent, processOptions{
		Dispatch:  DispatchRequest{SessionKey: sessionKey, UserMessage: "hello"},
		NoHistory: true,
	})
	if err != nil {
		f.t.Fatalf("runAgentLoop(%q): %v", sessionKey, err)
	}
	return response
}

// turnContext mimics the context a tool receives while a turn of the session runs.
func (f *sessionModelFixture) turnContext(sessionKey string) context.Context {
	ctx := withTurnState(context.Background(), newTurnState(
		f.baseAgent,
		processOptions{Dispatch: DispatchRequest{SessionKey: sessionKey}},
		turnEventScope{agentID: f.baseAgent.ID, sessionKey: sessionKey, turnID: "self-set-model"},
	))
	return WithAgentLoop(ctx, f.al)
}

func TestSelfToolModelPresetIsSessionScopedAndStartsNextTurn(t *testing.T) {
	f := newSessionModelFixture(t)
	if got := f.callTurn("session-a"); got != "local reply" {
		t.Fatalf("initial response = %q, want local reply", got)
	}

	setContext := f.turnContext("session-a")
	if result := runSelfTool(f.selfTool, setContext, "set", "model_preset", "missing", true); !result.IsError {
		t.Fatal("unknown model preset was accepted")
	}
	if result := runSelfTool(f.selfTool, setContext, "set", "model_preset", "remote", true); result.IsError {
		t.Fatalf("set model_preset: %s", result.ForLLM)
	}
	if got := runSelfTool(f.selfTool, setContext, "check", "model", nil, false).ForLLM; got != "local" {
		t.Fatalf("model during active turn = %q, want local", got)
	}
	if f.baseAgent.Model != "local" {
		t.Fatalf("global model = %q, want local", f.baseAgent.Model)
	}

	if got := f.callTurn("session-a"); got != "remote reply" {
		t.Fatalf("session-a response after preset change = %q, want remote reply", got)
	}
	if got := f.callTurn("session-b"); got != "local reply" {
		t.Fatalf("session-b response = %q, want local reply", got)
	}
	if f.remoteCalls != 1 || f.remoteModel != "deepseek-v3.2" {
		t.Fatalf("remote calls/model = %d/%q, want 1/deepseek-v3.2", f.remoteCalls, f.remoteModel)
	}
	if f.localCalls != 2 {
		t.Fatalf("local calls = %d, want 2", f.localCalls)
	}
}

func TestModelCommandReplacesSessionModelPreset(t *testing.T) {
	f := newSessionModelFixture(t)
	for _, session := range []string{"session-a", "session-b"} {
		ctx := f.turnContext(session)
		if result := runSelfTool(f.selfTool, ctx, "set", "model_preset", "remote", true); result.IsError {
			t.Fatalf("set model_preset for %s: %s", session, result.ForLLM)
		}
	}

	opts := &processOptions{Dispatch: DispatchRequest{SessionKey: "session-a"}}
	rt := f.al.buildCommandsRuntime(context.Background(), f.baseAgent, opts)
	if model, _ := rt.GetModelInfo(); model != "remote" {
		t.Fatalf("/model shows %q for a session using the remote preset, want remote", model)
	}
	if _, err := rt.SwitchModel("local"); err != nil {
		t.Fatalf("SwitchModel(local): %v", err)
	}

	if got := f.callTurn("session-a"); got != "local reply" {
		t.Fatalf("session-a response after /model local = %q, want local reply", got)
	}
	if got := f.callTurn("session-b"); got != "remote reply" {
		t.Fatalf("session-b response = %q, want remote reply (its preset is untouched)", got)
	}
}

// loopingToolCallProvider asks for a tool on every call, so only the turn's
// iteration limit ends the turn.
type loopingToolCallProvider struct{ calls int }

func (p *loopingToolCallProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID: fmt.Sprintf("call_%d", p.calls), Name: "search", Arguments: map[string]any{"q": p.calls},
		}},
		FinishReason: "tool_calls",
	}, nil
}

func (p *loopingToolCallProvider) GetDefaultModel() string { return "looping-model" }

func TestSessionIterationLimitAppliesToThatSessionOnly(t *testing.T) {
	provider := &loopingToolCallProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	al.setSessionIterationLimit(agent.ID, "limited", 2)

	runTurn := func(sessionKey string) int {
		t.Helper()
		provider.calls = 0
		_, err := al.runAgentLoop(context.Background(), agent, processOptions{
			Dispatch:  DispatchRequest{SessionKey: sessionKey, UserMessage: "go"},
			NoHistory: true,
		})
		if err != nil {
			t.Fatalf("runAgentLoop(%q): %v", sessionKey, err)
		}
		return provider.calls
	}
	limited, unlimited := runTurn("limited"), runTurn("other")
	if limited != 2 {
		t.Errorf("limited session made %d LLM calls, want 2", limited)
	}
	if unlimited != agent.MaxIterations {
		t.Errorf("other session made %d LLM calls, want the agent limit %d", unlimited, agent.MaxIterations)
	}
}

func TestClearCommandDropsSessionRuntimeState(t *testing.T) {
	f := newSessionModelFixture(t)
	for _, session := range []string{"session-a", "session-b"} {
		ctx := f.turnContext(session)
		for key, value := range map[string]any{"model_preset": "remote", "max_iterations": float64(3), "note": "keep"} {
			if result := runSelfTool(f.selfTool, ctx, "set", key, value, true); result.IsError {
				t.Fatalf("set %s for %s: %s", key, session, result.ForLLM)
			}
		}
	}

	opts := &processOptions{Dispatch: DispatchRequest{SessionKey: "session-a"}}
	rt := f.al.buildCommandsRuntime(context.Background(), f.baseAgent, opts)
	if err := rt.ClearHistory(); err != nil {
		t.Fatalf("ClearHistory(): %v", err)
	}

	id := f.baseAgent.ID
	if f.al.sessionModel(sessionModelKey{agentID: id, sessionKey: "session-a"}) != nil {
		t.Error("session-a kept its model preset after /clear")
	}
	if got := f.al.sessionIterationLimit(id, "session-a"); got != 0 {
		t.Errorf("session-a kept max_iterations %d after /clear", got)
	}
	if result := runSelfTool(f.selfTool, f.turnContext("session-a"), "check", "note", nil, false); !result.IsError {
		t.Error("session-a kept its scratchpad after /clear")
	}

	if f.al.sessionModel(sessionModelKey{agentID: id, sessionKey: "session-b"}) == nil {
		t.Error("/clear in session-a removed the model preset of session-b")
	}
	if got := f.al.sessionIterationLimit(id, "session-b"); got != 3 {
		t.Errorf("session-b max_iterations = %d, want 3", got)
	}
	if result := runSelfTool(f.selfTool, f.turnContext("session-b"), "check", "note", nil, false); result.IsError {
		t.Errorf("/clear in session-a removed the scratchpad of session-b: %s", result.ForLLM)
	}
}
