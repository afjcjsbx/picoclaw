package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newCustomCommandTestLoop(t *testing.T, workspace string) (*AgentLoop, *recordingProvider) {
	t.Helper()
	t.Setenv("PICOCLAW_HOME", t.TempDir())

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxToolIterations = 10

	provider := &recordingProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	return al, provider
}

func writeAgentCustomCommand(t *testing.T, workspace, name, content string) {
	t.Helper()
	path := filepath.Join(workspace, "commands", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir commands: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write custom command: %v", err)
	}
}

func TestProcessMessage_CustomCommandExpandsPrompt(t *testing.T) {
	workspace := t.TempDir()
	writeAgentCustomCommand(t, workspace, "review.md", `---
description: Review code
---

Review $1 and $2 for bugs.`)

	al, provider := newCustomCommandTestLoop(t, workspace)

	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  `/review src/auth.ts "error handling"`,
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if response != "Mock response" {
		t.Fatalf("processMessage() response = %q, want mock response", response)
	}

	if len(provider.lastMessages) == 0 {
		t.Fatal("provider did not receive any messages")
	}
	lastMessage := provider.lastMessages[len(provider.lastMessages)-1]
	want := "Review src/auth.ts and error handling for bugs."
	if lastMessage.Role != "user" || lastMessage.Content != want {
		t.Fatalf("last provider message = %+v, want expanded prompt %q", lastMessage, want)
	}
}

func TestHandleCommand_CustomCommandFallsThroughToAgent(t *testing.T) {
	workspace := t.TempDir()
	writeAgentCustomCommand(t, workspace, "explain.md", "Explain this code clearly.")

	al, _ := newCustomCommandTestLoop(t, workspace)
	agent := al.GetRegistry().GetDefaultAgent()
	opts := processOptions{}

	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/explain src/cache.ts",
	}, agent, &opts)
	if handled {
		t.Fatalf("custom command was handled as a direct reply: %q", reply)
	}
	want := "Explain this code clearly.\n\nsrc/cache.ts"
	if opts.UserMessage != want {
		t.Fatalf("opts.UserMessage = %q, want %q", opts.UserMessage, want)
	}
	if opts.Dispatch.UserMessage != want {
		t.Fatalf("opts.Dispatch.UserMessage = %q, want %q", opts.Dispatch.UserMessage, want)
	}
}

func TestHandleCommand_BuiltinCommandWinsOverCustomFile(t *testing.T) {
	workspace := t.TempDir()
	writeAgentCustomCommand(t, workspace, "help.md", "This must never run.")

	al, _ := newCustomCommandTestLoop(t, workspace)
	agent := al.GetRegistry().GetDefaultAgent()
	opts := processOptions{}

	reply, handled := al.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/help",
	}, agent, &opts)
	if !handled {
		t.Fatal("built-in /help was not handled")
	}
	if !strings.Contains(reply, "/help - Show this help message") {
		t.Fatalf("reply = %q, want built-in help output", reply)
	}
	if opts.UserMessage != "" {
		t.Fatalf("opts.UserMessage = %q, want unchanged", opts.UserMessage)
	}
}

func TestHelpListsCustomCommands(t *testing.T) {
	workspace := t.TempDir()
	writeAgentCustomCommand(t, workspace, "review.md", `---
description: Review code for correctness
---

Review $ARGUMENTS.`)

	al, _ := newCustomCommandTestLoop(t, workspace)
	response, err := al.processMessage(context.Background(), testInboundMessage(bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		ChatID:   "chat-1",
		Content:  "/help",
	}))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if !strings.Contains(response, "/review [arguments] - Review code for correctness") {
		t.Fatalf("help output missing custom command:\n%s", response)
	}
}

func TestExpandCustomCommandContent(t *testing.T) {
	workspace := t.TempDir()
	writeAgentCustomCommand(t, workspace, "review.md", "Review $ARGUMENTS.")

	al, _ := newCustomCommandTestLoop(t, workspace)
	agent := al.GetRegistry().GetDefaultAgent()

	got := al.expandCustomCommandContent("/review src/auth.ts", agent.ID)
	if got != "Review src/auth.ts." {
		t.Fatalf("expandCustomCommandContent() = %q, want expanded prompt", got)
	}

	if got := al.expandCustomCommandContent("/help", agent.ID); got != "/help" {
		t.Fatalf("built-in command was expanded: %q", got)
	}
	if got := al.expandCustomCommandContent("/missing", agent.ID); got != "/missing" {
		t.Fatalf("unknown command was changed: %q", got)
	}
}
