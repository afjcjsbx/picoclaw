package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type askUserTestProvider struct {
	question      string
	options       []string
	expectedReply string
	calls         int
	lastMessages  []providers.Message
}

func (p *askUserTestProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	_ = ctx
	_ = toolDefs
	_ = model
	_ = opts

	p.calls++
	p.lastMessages = append([]providers.Message(nil), messages...)

	if p.calls == 1 {
		return &providers.LLMResponse{
			Content: "I need one detail before continuing.",
			ToolCalls: []providers.ToolCall{{
				ID:        "call_ask_user",
				Type:      "function",
				Name:      tools.AskUserToolName,
				Arguments: map[string]any{"question": p.question, "options": p.options},
			}},
		}, nil
	}

	for _, message := range messages {
		if message.Role == "tool" && message.ToolCallID == "call_ask_user" && message.Content == p.expectedReply {
			return &providers.LLMResponse{Content: "Thanks, continuing with that input."}, nil
		}
	}

	return nil, fmt.Errorf("provider did not receive expected ask_user reply %q", p.expectedReply)
}

func (p *askUserTestProvider) GetDefaultModel() string {
	return "ask-user-test-model"
}

func TestFindPendingAskUserCall(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "start"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_ask_user",
				Type: "function",
				Function: &providers.FunctionCall{
					Name:      tools.AskUserToolName,
					Arguments: `{"question":"Need a choice","options":["one","two"]}`,
				},
			}},
		},
	}

	pending := findPendingAskUserCall(history)
	if pending == nil {
		t.Fatal("expected pending ask_user call")
	}
	if pending.ToolCallID != "call_ask_user" {
		t.Fatalf("ToolCallID = %q", pending.ToolCallID)
	}
	if pending.Question != "Need a choice" {
		t.Fatalf("Question = %q", pending.Question)
	}
	if len(pending.Options) != 2 || pending.Options[0] != "one" || pending.Options[1] != "two" {
		t.Fatalf("Options = %#v", pending.Options)
	}
}

func TestProcessMessage_AskUserPausesAndResumesWithFreeText(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	provider := &askUserTestProvider{
		question:      "What should I focus on?",
		options:       []string{"bugs", "docs"},
		expectedReply: "write release notes",
	}
	al.GetRegistry().GetDefaultAgent().Provider = provider
	al.RegisterTool(tools.NewAskUserTool())

	firstMsg := testInboundMessage(bus.InboundMessage{
		Channel:  "test",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Content:  "help me",
	})

	response, err := al.processMessage(context.Background(), firstMsg)
	if err != nil {
		t.Fatalf("first processMessage() error = %v", err)
	}
	if response != "" {
		t.Fatalf("first response = %q, want empty while waiting for user input", response)
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		want := "What should I focus on?\n\n1. bugs\n2. docs"
		if outbound.Content != want {
			t.Fatalf("outbound content = %q, want %q", outbound.Content, want)
		}
	case <-time.After(responseTimeout):
		t.Fatal("expected ask_user outbound question")
	}

	route, _, err := al.resolveMessageRoute(firstMsg)
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	allocation := al.allocateRouteSession(route, firstMsg)
	sessionKey := resolveScopeKey(allocation.SessionKey, firstMsg.SessionKey)

	history := al.GetRegistry().GetDefaultAgent().Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Fatalf("history length after ask_user = %d, want 2", len(history))
	}
	if history[1].Role != "assistant" || len(history[1].ToolCalls) != 1 {
		t.Fatalf("history[1] = %+v, want assistant tool call", history[1])
	}

	secondMsg := testInboundMessage(bus.InboundMessage{
		Channel:  "test",
		ChatID:   "chat-1",
		SenderID: "user-1",
		Content:  "write release notes",
	})

	response, err = al.processMessage(context.Background(), secondMsg)
	if err != nil {
		t.Fatalf("second processMessage() error = %v", err)
	}
	if response != "Thanks, continuing with that input." {
		t.Fatalf("second response = %q", response)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}

	history = al.GetRegistry().GetDefaultAgent().Sessions.GetHistory(sessionKey)
	if len(history) != 4 {
		t.Fatalf("history length after resume = %d, want 4", len(history))
	}
	if history[2].Role != "tool" || history[2].ToolCallID != "call_ask_user" || history[2].Content != "write release notes" {
		t.Fatalf("history[2] = %+v, want ask_user tool result", history[2])
	}
	if history[3].Role != "assistant" || history[3].Content != "Thanks, continuing with that input." {
		t.Fatalf("history[3] = %+v, want final assistant reply", history[3])
	}
}

func TestProcessMessage_AskUserResumesWithNumberedOption(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	provider := &askUserTestProvider{
		question:      "Which output style do you want?",
		options:       []string{"short", "detailed"},
		expectedReply: "detailed",
	}
	al.GetRegistry().GetDefaultAgent().Provider = provider
	al.RegisterTool(tools.NewAskUserTool())

	firstMsg := testInboundMessage(bus.InboundMessage{
		Channel:  "test",
		ChatID:   "chat-2",
		SenderID: "user-1",
		Content:  "summarize this",
	})
	if _, err := al.processMessage(context.Background(), firstMsg); err != nil {
		t.Fatalf("first processMessage() error = %v", err)
	}

	secondMsg := testInboundMessage(bus.InboundMessage{
		Channel:  "test",
		ChatID:   "chat-2",
		SenderID: "user-1",
		Content:  "2",
	})
	response, err := al.processMessage(context.Background(), secondMsg)
	if err != nil {
		t.Fatalf("second processMessage() error = %v", err)
	}
	if response != "Thanks, continuing with that input." {
		t.Fatalf("response = %q", response)
	}
}
