package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

func usageAssistantMessage(model string, prompt, completion int) providers.Message {
	return providers.Message{
		Role:      "assistant",
		Content:   "reply",
		ModelName: model,
		Usage: &providers.UsageInfo{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			Requests:         1,
		},
	}
}

func TestSessionUsageSummaryFallsBackToHistory(t *testing.T) {
	agent := &AgentInstance{Sessions: session.NewSessionManager("")}
	ctx := context.Background()
	const sessionKey = "agent:main:test"
	if err := agent.Sessions.AddFullMessage(ctx, sessionKey, usageAssistantMessage("model-a", 100, 20)); err != nil {
		t.Fatalf("AddFullMessage: %v", err)
	}

	summary, err := sessionUsageSummary(ctx, agent, sessionKey)
	if err != nil {
		t.Fatalf("sessionUsageSummary: %v", err)
	}
	if summary == nil || summary.Turns != 1 || summary.InputTokens != 100 || summary.OutputTokens != 20 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestSessionUsageSummaryKeepsCountersAfterCompaction(t *testing.T) {
	store, err := memory.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	agent := &AgentInstance{Sessions: session.NewJSONLBackend(store)}
	ctx := context.Background()
	const sessionKey = "agent:main:test"
	for range 2 {
		if addErr := agent.Sessions.AddFullMessage(
			ctx,
			sessionKey,
			usageAssistantMessage("m", 100, 10),
		); addErr != nil {
			t.Fatalf("AddFullMessage: %v", addErr)
		}
	}
	if truncateErr := agent.Sessions.TruncateHistory(ctx, sessionKey, 1); truncateErr != nil {
		t.Fatalf("TruncateHistory: %v", truncateErr)
	}
	if saveErr := agent.Sessions.Save(ctx, sessionKey); saveErr != nil {
		t.Fatalf("Save: %v", saveErr)
	}

	summary, err := sessionUsageSummary(ctx, agent, sessionKey)
	if err != nil {
		t.Fatalf("sessionUsageSummary: %v", err)
	}
	if summary == nil || summary.Turns != 2 || summary.InputTokens != 200 {
		t.Fatalf("summary = %+v, want both turns after compaction", summary)
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

func TestHandleCommandUsageReportsSessionUsage(t *testing.T) {
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
		"Cost: $0.0010",
		"Model: `test-model`",
	} {
		if !strings.Contains(reply, want) {
			t.Fatalf("/usage reply missing %q:\n%s", want, reply)
		}
	}
}
