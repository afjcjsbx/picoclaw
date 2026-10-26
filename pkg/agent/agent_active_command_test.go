package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestAgentLoop_TryHandleActiveTurnCommandRejectsMutatingCommand(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &plainProvider{})

	msg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "test",
			ChatID:   "chat1",
			ChatType: "direct",
			SenderID: "user1",
		},
		Content: "/clear",
	}
	sessionKey := "agent:main:test:chat1"

	if !al.tryHandleActiveTurnCommand(context.Background(), msg, sessionKey, "main") {
		t.Fatal("expected mutating command to be handled during an active turn")
	}

	select {
	case out := <-msgBus.OutboundChan():
		if !strings.Contains(out.Content, "Command /clear is unavailable while a task is running") {
			t.Fatalf("unexpected rejection reply: %q", out.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for command rejection reply")
	}

	if got := al.pendingSteeringCountForScope(sessionKey); got != 0 {
		t.Fatalf("mutating command was queued as steering, queue depth = %d", got)
	}
}

func TestAgentLoop_TryHandleActiveTurnCommandRunsReadOnlyCommands(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &plainProvider{})

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "start", text: "/start", want: "Hello! I am PicoClaw"},
		{name: "help", text: "/help", want: "/help - Show this help message"},
		{name: "show channel", text: "/show channel", want: "Current Channel: test"},
		{name: "list model", text: "/list model", want: "Current Model: test-model"},
		{name: "list channels", text: "/list channels", want: "No channels enabled"},
		{name: "check channel", text: "/check channel test", want: "channel manager not initialized"},
		{name: "context", text: "/context", want: "Context usage"},
		{name: "subagents", text: "/subagents", want: "Nothing is running in this chat right now."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := bus.NormalizeInboundMessage(bus.InboundMessage{
				Context: bus.InboundContext{
					Channel:  "test",
					ChatID:   "chat1",
					ChatType: "direct",
					SenderID: "user1",
				},
				Content: tt.text,
			})
			if !al.tryHandleActiveTurnCommand(context.Background(), msg, "agent:main:test:chat1", "") {
				t.Fatalf("expected %s to be handled as a read-only command", tt.text)
			}

			select {
			case out := <-msgBus.OutboundChan():
				if !strings.Contains(out.Content, tt.want) {
					t.Fatalf("%s reply = %q, want substring %q", tt.text, out.Content, tt.want)
				}
			case <-time.After(time.Second):
				t.Fatalf("timeout waiting for %s reply", tt.text)
			}
		})
	}
}

func TestAgentLoop_TryHandleActiveTurnCommandLeavesUnknownCommandForSteering(t *testing.T) {
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

	if al.tryHandleActiveTurnCommand(
		context.Background(),
		bus.InboundMessage{Content: "/unknown"},
		"agent:main:test:chat1",
		"main",
	) {
		t.Fatal("unknown slash-prefixed text should remain eligible for steering")
	}
}
