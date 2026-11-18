package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type systemMessageTestProvider struct {
	response string
}

func (p *systemMessageTestProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: p.response}, nil
}

func (p *systemMessageTestProvider) GetDefaultModel() string {
	return "test-model"
}

func newSystemMessageTestLoop(t *testing.T) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	return NewAgentLoop(cfg, msgBus, &systemMessageTestProvider{response: "ok"}), msgBus
}

func TestAsyncCallbackPublishesOriginSessionKey(t *testing.T) {
	al, msgBus := newSystemMessageTestLoop(t)
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	originKey := session.BuildOpaqueSessionKey("agent:main:direct:pico-user")

	batch := &toolBatch{
		al: al,
		ts: &turnState{
			agent:      agent,
			channel:    "pico",
			chatID:     "pico:session-1",
			sessionKey: originKey,
		},
	}
	batch.asyncCallback("exec")(context.Background(), &tools.ToolResult{ForLLM: "background done"})

	select {
	case got := <-msgBus.InboundChan():
		if got.SessionKey != originKey {
			t.Fatalf("inbound session_key = %q, want %q", got.SessionKey, originKey)
		}
		if got.AgentID != agent.ID {
			t.Fatalf("inbound agent_id = %q, want %q", got.AgentID, agent.ID)
		}
		if got.Content != "background done" {
			t.Fatalf("inbound content = %q, want %q", got.Content, "background done")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async inbound message")
	}
}

func TestProcessSystemMessageContinuesOriginSession(t *testing.T) {
	al, msgBus := newSystemMessageTestLoop(t)
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	ctx := context.Background()
	originKey := session.BuildOpaqueSessionKey("agent:main:direct:pico-user")
	if err := agent.Sessions.AddMessage(ctx, originKey, "user", "hello"); err != nil {
		t.Fatalf("seed origin session: %v", err)
	}

	if _, err := al.processSystemMessage(ctx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "system",
			ChatID:   "pico:pico:session-1",
			ChatType: "direct",
			SenderID: "async:exec",
		},
		Channel:    "system",
		ChatID:     "pico:pico:session-1",
		SenderID:   "async:exec",
		SessionKey: originKey,
		Content:    "background done",
	}); err != nil {
		t.Fatalf("processSystemMessage() error = %v", err)
	}

	originHistory, err := agent.Sessions.GetHistory(ctx, originKey)
	if err != nil {
		t.Fatalf("GetHistory(origin) error = %v", err)
	}
	if len(originHistory) != 3 {
		t.Fatalf("origin history has %d messages, want 3: %+v", len(originHistory), originHistory)
	}
	if originHistory[0].Content != "hello" {
		t.Fatalf("origin history lost its first message: %+v", originHistory)
	}
	if !strings.Contains(originHistory[1].Content, "[System: async:exec] background done") {
		t.Fatalf("origin history missing follow-up: %+v", originHistory)
	}

	mainHistory, err := agent.Sessions.GetHistory(ctx, session.BuildMainSessionKey(agent.ID))
	if err != nil {
		t.Fatalf("GetHistory(main) error = %v", err)
	}
	if len(mainHistory) != 0 {
		t.Fatalf("main session should stay empty, got %+v", mainHistory)
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Context.Channel != "pico" || outbound.Context.ChatID != "pico:session-1" {
			t.Fatalf("outbound context = %+v, want origin pico chat", outbound.Context)
		}
		if outbound.SessionKey != originKey {
			t.Fatalf("outbound session_key = %q, want %q", outbound.SessionKey, originKey)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for system follow-up response")
	}
}

func TestProcessSystemMessageUsesOriginAgent(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmp
	cfg.Agents.Defaults.ModelName = "test-model"
	root := config.AgentConfig{
		ID:        "root",
		Default:   true,
		Workspace: tmp + "/root",
		Model:     &config.AgentModelConfig{Primary: "test-model"},
	}
	worker := config.AgentConfig{
		ID:        "worker",
		Workspace: tmp + "/worker",
		Model:     &config.AgentModelConfig{Primary: "test-model"},
	}
	cfg.Agents.List = []config.AgentConfig{root, worker}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &systemMessageTestProvider{response: "ok"})
	workerAgent, ok := al.GetRegistry().GetAgent("worker")
	if !ok {
		t.Fatal("expected worker agent")
	}
	rootAgent := al.GetRegistry().GetDefaultAgent()
	if rootAgent == nil {
		t.Fatal("expected default agent")
	}

	ctx := context.Background()
	originKey := session.BuildOpaqueSessionKey("agent:worker:direct:pico-user")
	ensureSessionMetadata(workerAgent.Sessions, originKey, &session.SessionScope{
		Version: session.ScopeVersionV1,
		AgentID: "worker",
		Channel: "pico",
	}, nil)
	if err := workerAgent.Sessions.AddMessage(ctx, originKey, "user", "hello"); err != nil {
		t.Fatalf("seed worker session: %v", err)
	}

	if _, err := al.processSystemMessage(ctx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "system",
			ChatID:   "pico:pico:session-1",
			ChatType: "direct",
			SenderID: "async:exec",
		},
		Channel:    "system",
		ChatID:     "pico:pico:session-1",
		SenderID:   "async:exec",
		SessionKey: originKey,
		AgentID:    "worker",
		Content:    "background done",
	}); err != nil {
		t.Fatalf("processSystemMessage() error = %v", err)
	}

	workerHistory, err := workerAgent.Sessions.GetHistory(ctx, originKey)
	if err != nil {
		t.Fatalf("GetHistory(worker) error = %v", err)
	}
	if len(workerHistory) != 3 {
		t.Fatalf("worker history has %d messages, want 3: %+v", len(workerHistory), workerHistory)
	}
	if !strings.Contains(workerHistory[1].Content, "[System: async:exec] background done") {
		t.Fatalf("worker history missing follow-up: %+v", workerHistory)
	}

	rootMain, err := rootAgent.Sessions.GetHistory(ctx, session.BuildMainSessionKey(rootAgent.ID))
	if err != nil {
		t.Fatalf("GetHistory(root main) error = %v", err)
	}
	if len(rootMain) != 0 {
		t.Fatalf("root main session should stay empty, got %+v", rootMain)
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.AgentID != "worker" {
			t.Fatalf("outbound agent_id = %q, want worker", outbound.AgentID)
		}
		if outbound.SessionKey != originKey {
			t.Fatalf("outbound session_key = %q, want %q", outbound.SessionKey, originKey)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for system follow-up response")
	}
}

func TestProcessSystemMessageFallsBackToMainSession(t *testing.T) {
	for _, sessionKey := range []string{"", "not-a-session-key"} {
		t.Run("session_key="+sessionKey, func(t *testing.T) {
			al, _ := newSystemMessageTestLoop(t)
			agent := al.GetRegistry().GetDefaultAgent()
			if agent == nil {
				t.Fatal("expected default agent")
			}
			ctx := context.Background()

			if _, err := al.processSystemMessage(ctx, bus.InboundMessage{
				Context: bus.InboundContext{
					Channel:  "system",
					ChatID:   "telegram:chat-1",
					ChatType: "direct",
					SenderID: "async:exec",
				},
				Channel:    "system",
				ChatID:     "telegram:chat-1",
				SenderID:   "async:exec",
				SessionKey: sessionKey,
				Content:    "background done",
			}); err != nil {
				t.Fatalf("processSystemMessage() error = %v", err)
			}

			mainHistory, err := agent.Sessions.GetHistory(ctx, session.BuildMainSessionKey(agent.ID))
			if err != nil {
				t.Fatalf("GetHistory(main) error = %v", err)
			}
			if len(mainHistory) != 2 {
				t.Fatalf("main history has %d messages, want 2: %+v", len(mainHistory), mainHistory)
			}
			if !strings.Contains(mainHistory[0].Content, "[System: async:exec] background done") {
				t.Fatalf("main history missing follow-up: %+v", mainHistory)
			}
		})
	}
}
