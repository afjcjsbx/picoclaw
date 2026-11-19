// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/tools"
	integrationtools "github.com/sipeed/picoclaw/pkg/tools/integration"
)

func TestPublishTurnFailureNoticeBypassesMessageToolSuppression(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}
	messageTool := integrationtools.NewMessageTool()
	messageTool.SetSendCallback(func(
		context.Context,
		string, string, string, string,
		[]bus.MediaPart,
	) error {
		return nil
	})
	defaultAgent.Tools.Register(messageTool)

	result := messageTool.Execute(
		tools.WithToolSessionContext(context.Background(), "main", "session-1", nil),
		map[string]any{
			"content": "ack",
			"channel": "telegram",
			"chat_id": "-100123",
		},
	)
	if result == nil || result.IsError {
		t.Fatalf("message tool execute failed: %+v", result)
	}

	al.publishTurnFailureNotice(context.Background(), "telegram", "-100123", "session-1", "turn failed")

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content != "turn failed" {
			t.Fatalf("outbound content = %q, want turn failed", outbound.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("expected failure notice despite the message tool having sent earlier")
	}
}

func TestPublishTurnFailureNoticeSurvivesCanceledContext(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	al.publishTurnFailureNotice(ctx, "telegram", "-100123", "session-1", "turn failed")

	select {
	case outbound := <-msgBus.OutboundChan():
		if outbound.Content != "turn failed" {
			t.Fatalf("outbound content = %q, want turn failed", outbound.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("expected failure notice to survive a canceled parent context")
	}
}

func TestPublishTurnFailureNoticeSkipsInternalChannels(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	al.publishTurnFailureNotice(context.Background(), "cli", "chat-1", "session-1", "turn failed")

	select {
	case outbound := <-msgBus.OutboundChan():
		t.Fatalf("unexpected outbound for internal channel: %q", outbound.Content)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMaybePublishErrorUsesVisibleTurnFailureNotice(t *testing.T) {
	al, _, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()

	if !al.maybePublishError(
		context.Background(),
		"telegram",
		"-100123",
		"session-1",
		errors.New("provider exploded"),
	) {
		t.Fatal("maybePublishError() = false, want true")
	}

	select {
	case outbound := <-msgBus.OutboundChan():
		if !strings.Contains(outbound.Content, "Error processing message: provider exploded") {
			t.Fatalf("outbound content = %q, want processing error", outbound.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("expected processing error notice")
	}
}
