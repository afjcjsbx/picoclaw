package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestSelfToolModelPresetIsSessionScopedAndStartsNextTurn(t *testing.T) {
	workspace := t.TempDir()
	localCalls, remoteCalls := 0, 0
	localModel, remoteModel := "", ""
	localServer := newChatCompletionTestServer(t, "local", "local reply", &localCalls, &localModel)
	defer localServer.Close()
	remoteServer := newChatCompletionTestServer(
		t,
		"remote",
		"remote reply",
		&remoteCalls,
		&remoteModel,
	)
	defer remoteServer.Close()

	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Workspace: workspace, Provider: "openai", ModelName: "local",
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
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	defer al.Close()
	baseAgent := al.GetRegistry().GetDefaultAgent()
	toolValue, ok := baseAgent.Tools.Get("self")
	if !ok {
		t.Fatal("self tool was not registered")
	}
	selfTool, ok := toolValue.(*SelfTool)
	if !ok {
		t.Fatalf("self tool has type %T", toolValue)
	}

	callTurn := func(sessionKey string) string {
		t.Helper()
		response, err := al.runAgentLoop(context.Background(), baseAgent, processOptions{
			Dispatch:  DispatchRequest{SessionKey: sessionKey, UserMessage: "hello"},
			NoHistory: true,
		})
		if err != nil {
			t.Fatalf("runAgentLoop(%q): %v", sessionKey, err)
		}
		return response
	}
	if got := callTurn("session-a"); got != "local reply" {
		t.Fatalf("initial response = %q, want local reply", got)
	}

	setContext := withTurnState(context.Background(), newTurnState(
		baseAgent,
		processOptions{Dispatch: DispatchRequest{SessionKey: "session-a"}},
		turnEventScope{agentID: baseAgent.ID, sessionKey: "session-a", turnID: "self-set-model"},
	))
	setContext = WithAgentLoop(setContext, al)
	if result := runSelfTool(selfTool, setContext, "set", "model_preset", "missing", true); !result.IsError {
		t.Fatal("unknown model preset was accepted")
	}
	if result := runSelfTool(selfTool, setContext, "set", "model_preset", "remote", true); result.IsError {
		t.Fatalf("set model_preset: %s", result.ForLLM)
	}
	if got := runSelfTool(selfTool, setContext, "check", "model", nil, false).ForLLM; got != "local" {
		t.Fatalf("model during active turn = %q, want local", got)
	}
	if baseAgent.Model != "local" {
		t.Fatalf("global model = %q, want local", baseAgent.Model)
	}

	if got := callTurn("session-a"); got != "remote reply" {
		t.Fatalf("session-a response after preset change = %q, want remote reply", got)
	}
	if got := callTurn("session-b"); got != "local reply" {
		t.Fatalf("session-b response = %q, want local reply", got)
	}
	if remoteCalls != 1 || remoteModel != "deepseek-v3.2" {
		t.Fatalf("remote calls/model = %d/%q, want 1/deepseek-v3.2", remoteCalls, remoteModel)
	}
	if localCalls != 2 {
		t.Fatalf("local calls = %d, want 2", localCalls)
	}
}
