package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestHandleCommand_CommandsCanBeDisabledPerChannel(t *testing.T) {
	al, _ := newCustomCommandTestLoop(t, t.TempDir())
	cfg := al.GetConfig()
	cfg.Channels.Get(config.ChannelTelegram).CommandsEnabled = boolPointer(false)
	agent := al.GetRegistry().GetDefaultAgent()

	for _, text := range []string{"/clear", "/new", "/use", "/switch model", "/unknown"} {
		t.Run(text, func(t *testing.T) {
			reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
				Channel:  config.ChannelTelegram,
				ChatID:   "chat-1",
				Content:  text,
				SenderID: "user-1",
			}, agent, &processOptions{})
			if !handled || reply != commandsDisabledReply {
				t.Fatalf("handleCommand(%s) = %q, %v; want handled %q", text, reply, handled, commandsDisabledReply)
			}
		})
	}

	const sessionKey = "agent:main:discord:chat-1"
	if err := agent.Sessions.SetHistory(
		context.Background(),
		sessionKey,
		[]providers.Message{{Role: "user", Content: "keep me"}},
	); err != nil {
		t.Fatalf("SetHistory() error = %v", err)
	}
	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  config.ChannelDiscord,
		ChatID:   "chat-1",
		Content:  "/clear",
		SenderID: "user-1",
	}, agent, &processOptions{SessionKey: sessionKey})
	if !handled || reply != "Chat history cleared!" {
		t.Fatalf("/clear on discord = %q, %v; want allowed clear confirmation", reply, handled)
	}
}

func TestCommandDefinitions_AreEmptyWhenCommandsAreDisabled(t *testing.T) {
	al, _ := newCustomCommandTestLoop(t, t.TempDir())
	cfg := al.GetConfig()
	cfg.Channels.Get(config.ChannelTelegram).CommandsEnabled = boolPointer(false)
	agent := al.GetRegistry().GetDefaultAgent()

	if got := al.commandDefinitions(agent, config.ChannelTelegram); len(got) != 0 {
		t.Fatalf("disabled channel definitions = %+v, want empty", got)
	}
	if got := al.commandDefinitions(agent, config.ChannelDiscord); len(got) == 0 {
		t.Fatal("enabled channel has no command definitions")
	}
}

func TestCommandsDisabled_DoNotExpandCustomCommandsOrReachProvider(t *testing.T) {
	workspace := t.TempDir()
	writeAgentCustomCommand(t, workspace, "review.md", "Review $ARGUMENTS.")
	al, provider := newCustomCommandTestLoop(t, workspace)
	al.GetConfig().Channels.Get(config.ChannelTelegram).CommandsEnabled = boolPointer(false)
	agent := al.GetRegistry().GetDefaultAgent()
	opts := &processOptions{}

	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  config.ChannelTelegram,
		ChatID:   "chat-1",
		Content:  "/review src/auth.ts",
		SenderID: "user-1",
	}, agent, opts)
	if !handled || reply != commandsDisabledReply {
		t.Fatalf("disabled custom command = %q, %v", reply, handled)
	}
	if opts.UserMessage != "" || opts.Dispatch.UserMessage != "" {
		t.Fatalf("disabled custom command was expanded: %+v", opts)
	}
	if len(provider.lastMessages) != 0 {
		t.Fatalf("disabled custom command reached provider: %+v", provider.lastMessages)
	}
}

func TestActiveTurnCommands_RespectCommandsEnabled(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Channels.Get(config.ChannelTelegram).CommandsEnabled = boolPointer(false)
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &plainProvider{})
	t.Cleanup(al.Close)

	for _, text := range []string{"/help", "/review src/auth.ts", "/unknown"} {
		t.Run(text, func(t *testing.T) {
			msg := bus.NormalizeInboundMessage(bus.InboundMessage{
				Context: bus.InboundContext{
					Channel:  config.ChannelTelegram,
					ChatID:   "chat-1",
					ChatType: "direct",
					SenderID: "user-1",
				},
				Content: text,
			})
			if !al.tryHandleActiveTurnCommand(context.Background(), msg, "agent:main:telegram:chat-1", "main") {
				t.Fatalf("disabled %s was not consumed during an active turn", text)
			}
			assertActiveTurnReply(t, msgBus, commandsDisabledReply)
		})
	}

	msg := bus.NormalizeInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  config.ChannelTelegram,
			ChatID:   "chat-1",
			ChatType: "direct",
			SenderID: "user-1",
		},
		Content: "/stop",
	})
	if !al.tryHandleStopCommand(context.Background(), msg, "agent:main:telegram:chat-1") {
		t.Fatal("disabled /stop was not consumed during an active turn")
	}
	assertActiveTurnReply(t, msgBus, commandsDisabledReply)
	if got := al.pendingSteeringCountForScope("agent:main:telegram:chat-1"); got != 0 {
		t.Fatalf("disabled commands were queued as steering, depth = %d", got)
	}
}

func assertActiveTurnReply(t *testing.T, msgBus *bus.MessageBus, want string) {
	t.Helper()
	select {
	case out := <-msgBus.OutboundChan():
		if out.Content != want {
			t.Fatalf("active-turn reply = %q, want %q", out.Content, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for active-turn reply %q", want)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
