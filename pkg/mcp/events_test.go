package mcp

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

func TestPublishServerEventKeepsConfiguredServerName(t *testing.T) {
	eventBus := runtimeevents.NewBus()
	defer func() {
		if err := eventBus.Close(); err != nil {
			t.Errorf("event bus close failed: %v", err)
		}
	}()

	_, eventsCh, err := eventBus.Channel().OfKind(
		runtimeevents.KindMCPServerConnecting,
	).SubscribeChan(t.Context(), runtimeevents.SubscribeOptions{Name: "mcp-name", Buffer: 1})
	if err != nil {
		t.Fatalf("SubscribeChan failed: %v", err)
	}

	mgr := NewManager(WithRuntimeEvents(eventBus))
	mgr.publishServerEvent(
		runtimeevents.KindMCPServerConnecting,
		"debug-server",
		config.MCPServerConfig{
			Enabled: true,
			Type:    "stdio",
			Command: "npx",
			Env:     map[string]string{"LOG_LEVEL": "debug"},
		},
		0,
		nil,
	)

	event := receiveMCPRuntimeEvent(t, eventsCh)
	if event.Source.Name != "debug-server" {
		t.Fatalf("event source name = %q, want %q", event.Source.Name, "debug-server")
	}
	if event.Attrs["server"] != "debug-server" {
		t.Fatalf("event attrs server = %v, want %q", event.Attrs["server"], "debug-server")
	}
	payload, ok := event.Payload.(ServerEventPayload)
	if !ok {
		t.Fatalf("event payload = %T, want ServerEventPayload", event.Payload)
	}
	if payload.Server != "debug-server" {
		t.Fatalf("event payload server = %q, want %q", payload.Server, "debug-server")
	}
}
