package agent

import (
	"context"
	"encoding/json"
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

type requesterBindingProvider struct {
	mu    sync.Mutex
	calls int
	docs  []string
}

func (p *requesterBindingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	for _, message := range messages {
		if message.Role == "tool" {
			p.docs = append(p.docs, message.Content)
		}
	}
	if p.calls == 1 {
		return &providers.LLMResponse{
			Content: "asking",
			ToolCalls: []providers.ToolCall{{
				ID:   "call-1",
				Name: "ask_user",
				Arguments: map[string]any{
					"questions": []any{map[string]any{
						"question": "Who answers?",
						"choices":  []any{"A", "B"},
					}},
				},
			}},
			FinishReason: "tool_calls",
		}, nil
	}
	return &providers.LLMResponse{Content: "done", FinishReason: "stop"}, nil
}

func (p *requesterBindingProvider) GetDefaultModel() string { return "requester-binding" }

func (p *requesterBindingProvider) snapshot() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.docs...)
}

func TestAskUserOnlyAcceptsReplyFromRequester(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.AskUser.TimeoutSeconds = 5
	cfg.Agents.Defaults.MaxToolIterations = 5

	msgBus := bus.NewMessageBus()
	provider := &requesterBindingProvider{}
	loop := NewAgentLoop(cfg, msgBus, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = loop.Run(ctx) }()
	defer loop.Stop()

	message := func(senderID, content string) bus.InboundMessage {
		return bus.InboundMessage{
			Context: bus.InboundContext{
				Channel:  "test",
				ChatID:   "chat-requester",
				ChatType: "group",
				SenderID: senderID,
			},
			Sender:  bus.SenderInfo{Platform: "test", PlatformID: senderID},
			Content: content,
		}
	}

	if err := msgBus.PublishInbound(ctx, message("user-1", "please ask")); err != nil {
		t.Fatal(err)
	}
	waitForOutboundContent(t, msgBus, 5*time.Second, func(content string) bool {
		return strings.Contains(content, "Who answers?")
	})

	// Another group participant must not answer the pending question.
	if err := msgBus.PublishInbound(ctx, message("user-2", "2")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if calls, _ := provider.snapshot(); calls != 1 {
		t.Fatalf("provider calls = %d before the requester answered", calls)
	}

	// Only the requester can complete the interaction.
	if err := msgBus.PublishInbound(ctx, message("user-1", "1")); err != nil {
		t.Fatal(err)
	}
	waitForOutboundContent(t, msgBus, 5*time.Second, func(content string) bool {
		return content == "done"
	})

	_, docs := provider.snapshot()
	if len(docs) == 0 {
		t.Fatal("no tool result recorded")
	}
	var doc struct {
		Responses []struct {
			UserResponse any `json:"user_response"`
		} `json:"responses"`
	}
	if err := json.Unmarshal([]byte(docs[len(docs)-1]), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Responses) != 1 || doc.Responses[0].UserResponse != "A" {
		t.Fatalf("responses = %+v, want A", doc.Responses)
	}
}

type duplicateAskProvider struct {
	mu    sync.Mutex
	calls int
	docs  []string
}

func (p *duplicateAskProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	for _, message := range messages {
		if message.Role == "tool" {
			p.docs = append(p.docs, message.Content)
		}
	}
	if p.calls == 1 {
		return &providers.LLMResponse{
			Content: "asking twice",
			ToolCalls: []providers.ToolCall{
				{ID: "first", Name: "ask_user", Arguments: map[string]any{
					"questions": []any{map[string]any{"question": "First?"}},
				}},
				{ID: "second", Name: "ask_user", Arguments: map[string]any{
					"questions": []any{map[string]any{"question": "Second?"}},
				}},
			},
			FinishReason: "tool_calls",
		}, nil
	}
	return &providers.LLMResponse{Content: "duplicate done", FinishReason: "stop"}, nil
}

func (p *duplicateAskProvider) GetDefaultModel() string { return "duplicate-ask" }

func (p *duplicateAskProvider) snapshot() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([]string(nil), p.docs...)
}

func TestAskUserRejectsSecondCallInSameResponse(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Tools.AskUser.TimeoutSeconds = 5
	cfg.Agents.Defaults.MaxToolIterations = 5

	msgBus := bus.NewMessageBus()
	provider := &duplicateAskProvider{}
	loop := NewAgentLoop(cfg, msgBus, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = loop.Run(ctx) }()
	defer loop.Stop()

	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "test",
			ChatID:   "chat-duplicate",
			ChatType: "direct",
			SenderID: "user",
		},
		Content: "go",
	}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	waitForOutboundContent(t, msgBus, 5*time.Second, func(content string) bool {
		return strings.Contains(content, "First?")
	})
	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "test",
			ChatID:   "chat-duplicate",
			ChatType: "direct",
			SenderID: "user",
		},
		Content: "1",
	}); err != nil {
		t.Fatal(err)
	}
	waitForOutboundContent(t, msgBus, 5*time.Second, func(content string) bool {
		return content == "duplicate done"
	})
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("turn took %v, the duplicate call waited for its timeout", elapsed)
	}

	_, docs := provider.snapshot()
	if len(docs) != 2 {
		t.Fatalf("tool results = %d, want 2", len(docs))
	}
	foundRejection := false
	for _, doc := range docs {
		if strings.Contains(doc, "accepts only one call per response") {
			foundRejection = true
		}
		if strings.Contains(doc, "Second?") {
			t.Fatalf("the second question was delivered: %s", doc)
		}
	}
	if !foundRejection {
		t.Fatalf("duplicate call was not rejected: %v", docs)
	}
}
