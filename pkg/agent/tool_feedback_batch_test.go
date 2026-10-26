package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// feedbackScriptProvider returns the scripted responses in order, then a final answer.
type feedbackScriptProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	calls     int
}

func (p *feedbackScriptProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls <= len(p.responses) {
		return p.responses[p.calls-1], nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *feedbackScriptProvider) GetDefaultModel() string {
	return "feedback-script-provider"
}

func echoCall(id, text string) providers.ToolCall {
	return providers.ToolCall{ID: id, Name: "echo_text", Arguments: map[string]any{"text": text}}
}

type fakeEditableChannel struct{ fakeChannel }

func (f *fakeEditableChannel) EditMessage(ctx context.Context, chatID, messageID, content string) error {
	return nil
}

// runToolFeedbackTurn runs one turn on channelName with tool feedback enabled
// and returns the tool feedback it published. When registered is not nil, a
// channel manager holding those channels is attached to the loop.
func runToolFeedbackTurn(
	t *testing.T,
	responses []*providers.LLMResponse,
	channelName string,
	registered map[string]channels.Channel,
	setup func(al *AgentLoop),
) []string {
	t.Helper()

	al, agent, cleanup := newHookTestLoop(t, &feedbackScriptProvider{responses: responses})
	t.Cleanup(cleanup)
	al.cfg.Agents.Defaults.ToolFeedback.Enabled = true
	al.RegisterTool(&echoTextTool{})
	if setup != nil {
		setup(al)
	}

	msgBus, ok := al.bus.(*bus.MessageBus)
	if !ok {
		t.Fatalf("expected concrete MessageBus, got %T", al.bus)
	}
	if registered != nil {
		cm, err := channels.NewManager(&config.Config{}, msgBus, nil)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		for name, ch := range registered {
			cm.RegisterChannel(name, ch)
		}
		al.SetChannelManager(cm)
	}

	if _, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:      "session-1",
		Channel:         channelName,
		ChatID:          "user-1",
		UserMessage:     "run tool",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	}); err != nil {
		t.Fatalf("runAgentLoop failed: %v", err)
	}

	var feedback []string
	for {
		select {
		case msg := <-msgBus.OutboundChan():
			if msg.Context.Raw[metadataKeyMessageKind] == messageKindToolFeedback {
				feedback = append(feedback, msg.Content)
			}
		default:
			return feedback
		}
	}
}

func threeEchoCalls() []*providers.LLMResponse {
	return []*providers.LLMResponse{{
		Content:   "Echo three things.",
		ToolCalls: []providers.ToolCall{echoCall("c1", "a"), echoCall("c2", "b"), echoCall("c3", "c")},
	}}
}

func TestToolFeedback_MergesCallsOnChannelWithoutEditing(t *testing.T) {
	feedback := runToolFeedbackTurn(t, threeEchoCalls(), "weixin",
		map[string]channels.Channel{"weixin": &fakeChannel{}}, nil)

	if len(feedback) != 1 {
		t.Fatalf("published %d tool feedback messages, want 1: %q", len(feedback), feedback)
	}
	if want := "\U0001f527 `echo_text` ×3\nEcho three things.\n"; !strings.HasPrefix(feedback[0], want) {
		t.Fatalf("tool feedback = %q, want prefix %q", feedback[0], want)
	}
	if got := strings.Count(feedback[0], "```json"); got != 3 {
		t.Fatalf("tool feedback has %d argument blocks, want 3: %q", got, feedback[0])
	}
	if !strings.Contains(feedback[0], `"text": "c"`) {
		t.Fatalf("tool feedback = %q, want the arguments of the last call", feedback[0])
	}
}

func TestToolFeedback_KeepsOneMessagePerCallOnEditableChannel(t *testing.T) {
	feedback := runToolFeedbackTurn(t, threeEchoCalls(), "telegram",
		map[string]channels.Channel{"telegram": &fakeEditableChannel{}}, nil)

	if len(feedback) != 3 {
		t.Fatalf("published %d tool feedback messages, want 3: %q", len(feedback), feedback)
	}
	for _, msg := range feedback {
		if want := "\U0001f527 `echo_text`\nEcho three things.\n"; !strings.HasPrefix(msg, want) {
			t.Fatalf("tool feedback = %q, want prefix %q", msg, want)
		}
	}
}

func TestToolFeedback_KeepsOneMessagePerCallWhenChannelIsUnknown(t *testing.T) {
	feedback := runToolFeedbackTurn(t, threeEchoCalls(), "weixin",
		map[string]channels.Channel{"other": &fakeChannel{}}, nil)

	if len(feedback) != 3 {
		t.Fatalf("published %d tool feedback messages, want 3: %q", len(feedback), feedback)
	}
}

func TestToolFeedback_DropsRepeatedExplanationOnChannelWithoutEditing(t *testing.T) {
	responses := []*providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{echoCall("c1", "a")}},
		{ToolCalls: []providers.ToolCall{echoCall("c2", "b")}},
	}
	feedback := runToolFeedbackTurn(t, responses, "weixin",
		map[string]channels.Channel{"weixin": &fakeChannel{}}, nil)

	if len(feedback) != 2 {
		t.Fatalf("published %d tool feedback messages, want 2: %q", len(feedback), feedback)
	}
	if !strings.Contains(feedback[0], utils.ToolFeedbackContinuationHint+": run tool") {
		t.Fatalf("first tool feedback = %q, want the explanation", feedback[0])
	}
	if want := "\U0001f527 `echo_text`\n```json\n{\n  \"text\": \"b\"\n}\n```"; feedback[1] != want {
		t.Fatalf("second tool feedback = %q, want %q", feedback[1], want)
	}
}

func TestToolFeedback_KeepsChangedExplanationOnChannelWithoutEditing(t *testing.T) {
	responses := []*providers.LLMResponse{
		{Content: "First step.", ToolCalls: []providers.ToolCall{echoCall("c1", "a")}},
		{Content: "Second step.", ToolCalls: []providers.ToolCall{echoCall("c2", "b")}},
	}
	feedback := runToolFeedbackTurn(t, responses, "weixin",
		map[string]channels.Channel{"weixin": &fakeChannel{}}, nil)

	if len(feedback) != 2 {
		t.Fatalf("published %d tool feedback messages, want 2: %q", len(feedback), feedback)
	}
	if !strings.Contains(feedback[0], "First step.") || !strings.Contains(feedback[1], "Second step.") {
		t.Fatalf("tool feedback = %q, want both explanations", feedback)
	}
}

func TestToolFeedback_RespondHookCallIsMergedWithFollowingCalls(t *testing.T) {
	responses := []*providers.LLMResponse{{
		Content: "Two steps.",
		ToolCalls: []providers.ToolCall{
			echoCall("c1", "a"),
			{ID: "c2", Name: "echo_text_rewritten", Arguments: map[string]any{"text": "b"}},
		},
	}}
	feedback := runToolFeedbackTurn(t, responses, "weixin",
		map[string]channels.Channel{"weixin": &fakeChannel{}},
		func(al *AgentLoop) {
			al.RegisterTool(&echoTextRewrittenTool{})
			if err := al.MountHook(NamedHook("respond-hook", &respondHook{
				respondTools: map[string]bool{"echo_text": true},
			})); err != nil {
				t.Fatalf("MountHook failed: %v", err)
			}
		})

	if len(feedback) != 1 {
		t.Fatalf("published %d tool feedback messages, want 1: %q", len(feedback), feedback)
	}
	if want := "\U0001f527 `echo_text` · `echo_text_rewritten`\nTwo steps.\n"; !strings.HasPrefix(feedback[0], want) {
		t.Fatalf("tool feedback = %q, want prefix %q", feedback[0], want)
	}
}

func TestToolFeedback_MergedMessageUsesRewrittenToolName(t *testing.T) {
	responses := []*providers.LLMResponse{{ToolCalls: []providers.ToolCall{echoCall("c1", "a")}}}
	feedback := runToolFeedbackTurn(t, responses, "weixin",
		map[string]channels.Channel{"weixin": &fakeChannel{}},
		func(al *AgentLoop) {
			al.RegisterTool(&echoTextRewrittenTool{})
			if err := al.MountHook(NamedHook("tool-rename", &toolRenameHook{})); err != nil {
				t.Fatalf("MountHook failed: %v", err)
			}
		})

	if len(feedback) != 1 || !strings.HasPrefix(feedback[0], "\U0001f527 `echo_text_rewritten`\n") {
		t.Fatalf("tool feedback = %q, want one message for echo_text_rewritten", feedback)
	}
}
