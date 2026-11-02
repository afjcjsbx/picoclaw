package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type askUserProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *askUserProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++

	if p.calls == 1 {
		return &providers.LLMResponse{
			Content: "I need a clarification.",
			ToolCalls: []providers.ToolCall{{
				ID:   "call-ask-user",
				Name: "ask_user",
				Arguments: map[string]any{
					"questions": []any{map[string]any{
						"question": "Which format?",
						"choices":  []any{"JSON", "YAML"},
					}},
				},
			}},
			FinishReason: "tool_calls",
		}, nil
	}

	return &providers.LLMResponse{
		Content:      "Selected YAML.",
		FinishReason: "stop",
	}, nil
}

func (p *askUserProvider) GetDefaultModel() string { return "ask-user-test-model" }

func TestAskUserToolSuspendsTurnAndResumesWithAnswer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.AskUser.TimeoutSeconds = 10
	cfg.Agents.Defaults.MaxToolIterations = 5

	msgBus := bus.NewMessageBus()
	provider := &askUserProvider{}
	loop := NewAgentLoop(cfg, msgBus, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = loop.Run(ctx) }()
	defer loop.Stop()

	inboundContext := bus.InboundContext{
		Channel:  "test",
		ChatID:   "chat-ask-user",
		ChatType: "direct",
		SenderID: "user",
	}
	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{
		Context: inboundContext,
		Content: "Please ask me a question",
	}); err != nil {
		t.Fatal(err)
	}

	prompt := waitForOutboundContent(t, msgBus, 5*time.Second, func(content string) bool {
		return strings.Contains(content, "Which format?")
	})
	if !strings.Contains(prompt, "1. JSON"+askUserRecommendedSuffix()) {
		t.Fatalf("prompt missing recommended marker:\n%s", prompt)
	}

	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{
		Context: inboundContext,
		Content: "2",
	}); err != nil {
		t.Fatal(err)
	}

	final := waitForOutboundContent(t, msgBus, 5*time.Second, func(content string) bool {
		return content == "Selected YAML."
	})
	if final != "Selected YAML." {
		t.Fatalf("final = %q", final)
	}

	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls < 2 {
		t.Fatalf("provider calls = %d, want at least 2", calls)
	}
}

func waitForOutboundContent(
	t *testing.T,
	msgBus *bus.MessageBus,
	timeout time.Duration,
	match func(string) bool,
) string {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case outbound := <-msgBus.OutboundChan():
			if match(outbound.Content) {
				return outbound.Content
			}
		case <-timer.C:
			t.Fatal("timed out waiting for outbound content")
			return ""
		}
	}
}

func askUserRecommendedSuffix() string {
	return " (Recommended)"
}
