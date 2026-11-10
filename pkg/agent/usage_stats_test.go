package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

func TestSessionUsageSummaryAggregatesPersistedMessages(t *testing.T) {
	store := session.NewSessionManager("")
	agent := &AgentInstance{Sessions: store}
	ctx := context.Background()
	sessionKey := "agent:main:test"

	messages := []providers.Message{
		{
			Role:      "user",
			Content:   "hello",
			ModelName: "model-a",
		},
		{
			Role:      "assistant",
			Content:   "hi",
			ModelName: "model-a",
			Usage: &providers.UsageInfo{
				PromptTokens:     100,
				CompletionTokens: 20,
				TotalTokens:      120,
				CacheReadTokens:  40,
				CostUSD:          0.001,
				Requests:         2,
			},
		},
	}
	for _, message := range messages {
		if err := store.AddFullMessage(ctx, sessionKey, message); err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}
	}

	summary, err := sessionUsageSummary(ctx, agent, sessionKey)
	if err != nil {
		t.Fatalf("sessionUsageSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("summary = nil")
	}
	if summary.Requests != 2 || summary.Turns != 1 {
		t.Fatalf("requests/turns = %d/%d, want 2/1", summary.Requests, summary.Turns)
	}
	if summary.InputTokens != 100 || summary.OutputTokens != 20 || summary.TotalTokens != 120 {
		t.Fatalf("tokens = %+v", summary.Totals)
	}
	if summary.CacheReadTokens != 40 || summary.CostUSD != 0.001 {
		t.Fatalf("cache/cost = %d/%v", summary.CacheReadTokens, summary.CostUSD)
	}
	if len(summary.Models) != 1 || summary.Models[0].Model != "model-a" {
		t.Fatalf("models = %+v", summary.Models)
	}
}

func TestSessionUsageSummaryWithoutAgent(t *testing.T) {
	summary, err := sessionUsageSummary(context.Background(), nil, "session")
	if err != nil {
		t.Fatalf("sessionUsageSummary: %v", err)
	}
	if summary != nil {
		t.Fatalf("summary = %+v, want nil", summary)
	}
}

func TestHandleCommandUsageUsesSessionUsage(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &plainProvider{})
	agent := al.GetRegistry().GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}

	const sessionKey = "agent:main:cli:direct:tester"
	if err := agent.Sessions.AddFullMessage(context.Background(), sessionKey, providers.Message{
		Role:      "assistant",
		Content:   "previous reply",
		ModelName: "test-model",
		Usage: &providers.UsageInfo{
			PromptTokens:     250,
			CompletionTokens: 50,
			TotalTokens:      300,
			CacheReadTokens:  100,
			CostUSD:          0.001,
			Requests:         2,
		},
	}); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	opts := processOptions{
		SessionKey: sessionKey,
		Channel:    "cli",
		ChatID:     "direct",
		SenderID:   "tester",
		Dispatch: DispatchRequest{
			SessionKey:  sessionKey,
			UserMessage: "/usage",
		},
	}
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Content:    "/usage",
		Channel:    "cli",
		ChatID:     "direct",
		SenderID:   "tester",
		SessionKey: sessionKey,
	}, agent, &opts)
	if !handled {
		t.Fatal("/usage was not handled")
	}
	for _, want := range []string{
		"Usage for this session",
		"LLM calls: 2 · turns: 1",
		"Input: 250 tokens (fresh 150 · cache read 100)",
		"Cost: $0.001",
		"Model: `test-model`",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/usage reply missing %q:\n%s", want, reply)
		}
	}
}
