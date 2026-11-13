package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type resumeProvider struct {
	chat func(ctx context.Context, messages []providers.Message) (*providers.LLMResponse, error)
}

func (p *resumeProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return p.chat(ctx, messages)
}

func (p *resumeProvider) GetDefaultModel() string { return "mock-model" }

func newResumeTestLoop(t *testing.T, p *resumeProvider) (*AgentLoop, *bus.MessageBus, string) {
	t.Helper()
	workspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:              workspace,
				ModelName:              "test-model",
				MaxTokens:              4096,
				MaxToolIterations:      10,
				ResumeInterruptedTurns: true,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, p)
	t.Cleanup(al.Close)
	return al, msgBus, workspace
}

func markerPaths(t *testing.T, workspace string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(turnMarkersDir(workspace), "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// A turn that ends normally leaves no marker; one cut short by shutdown keeps
// it, and the next start resumes it: the model sees the restart notice and the
// answer reaches the original chat.
func TestRecoverInterruptedTurns_ResumesTurnCutShortByShutdown(t *testing.T) {
	var cancel context.CancelFunc
	var sawMarker bool
	var resumedMessages []providers.Message
	var workspace string
	provider := &resumeProvider{}
	provider.chat = func(ctx context.Context, messages []providers.Message) (*providers.LLMResponse, error) {
		if cancel != nil { // first run: the process "dies" mid-LLM-call
			sawMarker = len(markerPaths(t, workspace)) == 1
			cancel()
			return nil, ctx.Err()
		}
		resumedMessages = messages
		return &providers.LLMResponse{Content: "resumed ok"}, nil
	}
	al, msgBus, ws := newResumeTestLoop(t, provider)
	workspace = ws

	var runCtx context.Context
	runCtx, cancel = context.WithCancel(context.Background())
	_, _ = al.ProcessDirectWithChannel(runCtx, "do the long task", "", "telegram", "chat1")
	if !sawMarker {
		t.Fatal("no marker while the turn was running")
	}
	if got := len(markerPaths(t, workspace)); got != 1 {
		t.Fatalf("markers after shutdown = %d, want 1", got)
	}

	cancel = nil
	al.RecoverInterruptedTurns(context.Background())

	select {
	case out := <-msgBus.OutboundChan():
		if out.Content != "resumed ok" || out.ChatID != "chat1" {
			t.Fatalf("outbound = %q to %q, want %q to chat1", out.Content, out.ChatID, "resumed ok")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no response after recovery")
	}

	var gotTask, gotNotice bool
	for _, m := range resumedMessages {
		gotTask = gotTask || strings.Contains(m.Content, "do the long task")
		gotNotice = gotNotice || strings.Contains(m.Content, resumeNotice)
	}
	if !gotTask || !gotNotice {
		t.Fatalf("resumed prompt missing task=%v notice=%v", gotTask, gotNotice)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(markerPaths(t, workspace)) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(markerPaths(t, workspace)); got != 0 {
		t.Fatalf("markers after resumed turn finished = %d, want 0", got)
	}
}

func TestRecoverInterruptedTurns_DropsExhaustedMarker(t *testing.T) {
	provider := &resumeProvider{chat: func(context.Context, []providers.Message) (*providers.LLMResponse, error) {
		t.Error("provider must not be called for an exhausted marker")
		return &providers.LLMResponse{Content: "x"}, nil
	}}
	al, _, workspace := newResumeTestLoop(t, provider)

	path := turnMarkerPath(workspace, "agent:main:x")
	if err := writeTurnMarker(path, turnMarker{
		SessionKey: "agent:main:x", Channel: "telegram", ChatID: "c", StartedAt: time.Now(), Attempts: resumeMaxAttempts,
	}); err != nil {
		t.Fatal(err)
	}
	al.resumeTurn(context.Background(), path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("exhausted marker still present: %v", err)
	}
}

func TestCloseInterruptedToolCalls(t *testing.T) {
	al, _, _ := newResumeTestLoop(t, &resumeProvider{})
	agent := al.GetRegistry().GetDefaultAgent()
	ctx := context.Background()
	const key = "agent:main:close-test"

	add := func(m providers.Message) {
		t.Helper()
		if err := agent.Sessions.AddFullMessage(ctx, key, m); err != nil {
			t.Fatal(err)
		}
	}
	add(providers.Message{Role: "user", Content: "go"})
	add(providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "a", Name: "t"}, {ID: "b", Name: "t"}}})
	add(providers.Message{Role: "tool", Content: "done", ToolCallID: "a"})

	if !closeInterruptedToolCalls(ctx, agent, key) {
		t.Fatal("interrupted turn reported as finished")
	}
	history, _ := agent.Sessions.GetHistory(ctx, key)
	last := history[len(history)-1]
	if len(history) != 4 || last.Role != "tool" || last.ToolCallID != "b" || last.Content != interruptedToolResult {
		t.Fatalf("history not closed: %+v", history)
	}

	add(providers.Message{Role: "assistant", Content: "all done"})
	if closeInterruptedToolCalls(ctx, agent, key) {
		t.Fatal("finished turn reported as interrupted")
	}
}
