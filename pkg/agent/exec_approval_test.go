package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type approvalTestManager struct {
	recordingChannelManager
	sent chan bus.OutboundMessage
}

func (m *approvalTestManager) GetChannel(string) (channels.Channel, bool) {
	return &fakeChannel{}, true
}

func (m *approvalTestManager) SendMessage(_ context.Context, msg bus.OutboundMessage) error {
	m.sent <- msg
	return nil
}

func TestExecApprovalRequiresOriginalSenderAndExactToken(t *testing.T) {
	al, _, cleanup := newHookTestLoop(t, &toolHookProvider{})
	defer cleanup()
	al.cfg.Tools.Exec.EnableDenyPatterns = true
	al.cfg.Tools.Exec.AllowRemote = true
	al.running.Store(true)
	mgr := &approvalTestManager{sent: make(chan bus.OutboundMessage, 1)}
	al.channelManager = mgr
	origin := bus.InboundContext{Channel: "telegram", ChatID: "group", TopicID: "topic", SenderID: "alice"}
	command := "rm -rf old-build"
	req := &ToolApprovalRequest{
		Tool:      "exec",
		Arguments: map[string]any{"action": "run", "command": command},
		Context:   &TurnContext{Inbound: &origin},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan ApprovalDecision, 1)
	go func() { done <- al.hooks.ApproveTool(ctx, req) }()

	var prompt bus.OutboundMessage
	select {
	case prompt = <-mgr.sent:
	case <-ctx.Done():
		t.Fatal("approval prompt was not sent")
	}
	if !strings.Contains(prompt.Content, command) {
		t.Fatalf("prompt omits command: %q", prompt.Content)
	}
	token := regexp.MustCompile(`approve ([0-9a-f]{32})`).FindStringSubmatch(prompt.Content)
	if len(token) != 2 {
		t.Fatalf("prompt omits approval token: %q", prompt.Content)
	}
	other := origin
	other.SenderID = "bob"
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{Context: other, Content: "/approve " + token[1]}) {
		t.Fatal("approval reply was not intercepted")
	}
	select {
	case <-done:
		t.Fatal("another sender approved the command")
	default:
	}
	other = origin
	other.TopicID = "other-topic"
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{Context: other, Content: "/approve " + token[1]}) {
		t.Fatal("cross-topic reply was not intercepted")
	}
	select {
	case <-done:
		t.Fatal("another topic approved the command")
	default:
	}
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{Context: origin, Content: "approve " + token[1]}) {
		t.Fatal("approval reply was not intercepted")
	}
	select {
	case decision := <-done:
		if !decision.Approved || decision.approvedCommand != command {
			t.Fatalf("approval decision = %+v", decision)
		}
	case <-ctx.Done():
		t.Fatal("approval did not complete")
	}
	go func() { done <- al.hooks.ApproveTool(ctx, req) }()
	select {
	case prompt = <-mgr.sent:
	case <-ctx.Done():
		t.Fatal("denial prompt was not sent")
	}
	token = regexp.MustCompile(`deny ([0-9a-f]{32})`).FindStringSubmatch(prompt.Content)
	if len(token) != 2 {
		t.Fatalf("prompt omits denial token: %q", prompt.Content)
	}
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{Context: origin, Content: "deny " + token[1]}) {
		t.Fatal("denial reply was not intercepted")
	}
	select {
	case decision := <-done:
		if decision.Approved {
			t.Fatal("explicit denial approved the command")
		}
	case <-ctx.Done():
		t.Fatal("denial did not complete")
	}
}

func TestExecApprovalExpiresAndUnattendedCommandsFailClosed(t *testing.T) {
	al, _, cleanup := newHookTestLoop(t, &toolHookProvider{})
	defer cleanup()
	al.cfg.Tools.Exec.EnableDenyPatterns = true
	al.cfg.Tools.Exec.AllowRemote = true
	origin := bus.InboundContext{Channel: "telegram", ChatID: "chat", SenderID: "alice"}
	req := &ToolApprovalRequest{
		Tool:      "exec",
		Arguments: map[string]any{"action": "run", "command": "rm -rf old-build"},
		Context:   &TurnContext{Inbound: &origin},
	}
	if decision := al.hooks.ApproveTool(context.Background(), req); decision.Approved {
		t.Fatal("unattended command was approved")
	}
	al.running.Store(true)
	mgr := &approvalTestManager{sent: make(chan bus.OutboundMessage, 1)}
	al.channelManager = mgr
	al.cfg.Tools.Exec.AllowRemote = false
	if decision := al.hooks.ApproveTool(context.Background(), req); decision.Approved {
		t.Fatal("remote exec restriction was bypassed")
	}
	select {
	case <-mgr.sent:
		t.Fatal("unexecutable remote command prompted for approval")
	default:
	}
	al.cfg.Tools.Exec.AllowRemote = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan ApprovalDecision, 1)
	go func() { done <- al.hooks.ApproveTool(ctx, req) }()
	var prompt bus.OutboundMessage
	select {
	case prompt = <-mgr.sent:
	case <-time.After(time.Second):
		t.Fatal("expiring approval prompt was not sent")
	}
	decision := <-done
	if decision.Approved {
		t.Fatal("expired request was approved")
	}
	token := regexp.MustCompile(`approve ([0-9a-f]{32})`).FindStringSubmatch(prompt.Content)
	if len(token) != 2 {
		t.Fatalf("prompt omits token: %q", prompt.Content)
	}
	if !al.tryHandleExecApproval(
		context.Background(),
		bus.InboundMessage{Context: origin, Content: "/approve " + token[1]},
	) {
		t.Fatal("late approval reply was not intercepted")
	}
}

type execApprovalProvider struct{ calls int }

func (p *execApprovalProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: "exec-1", Name: "exec", Arguments: map[string]any{"action": "run", "command": "rm -rf old-build"},
		}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *execApprovalProvider) GetDefaultModel() string { return "exec-approval-test" }

func TestExecApprovalRunsOnlyAfterChannelConfirmation(t *testing.T) {
	al, agent, cleanup := newHookTestLoop(t, &execApprovalProvider{})
	defer cleanup()
	al.cfg.Tools.Exec.EnableDenyPatterns = true
	al.cfg.Tools.Exec.AllowRemote = true
	execTool, err := tools.NewExecToolWithConfig(agent.Workspace, false, al.cfg)
	if err != nil {
		t.Fatal(err)
	}
	al.RegisterTool(execTool)
	target := filepath.Join(agent.Workspace, "old-build")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := &approvalTestManager{sent: make(chan bus.OutboundMessage, 1)}
	al.channelManager = mgr
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	msgBus := al.bus.(*bus.MessageBus)
	origin := bus.InboundContext{Channel: "telegram", ChatID: "chat", SenderID: "alice"}
	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{Context: origin, Content: "delete old build"}); err != nil {
		t.Fatal(err)
	}
	var prompt bus.OutboundMessage
	select {
	case prompt = <-mgr.sent:
	case <-ctx.Done():
		t.Fatal("approval prompt was not sent")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("command ran before approval: %v", err)
	}
	token := regexp.MustCompile(`approve ([0-9a-f]{32})`).FindStringSubmatch(prompt.Content)
	if len(token) != 2 {
		t.Fatalf("prompt omits token: %q", prompt.Content)
	}
	if err := msgBus.PublishInbound(
		ctx,
		bus.InboundMessage{Context: origin, Content: "approve " + token[1]},
	); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case outbound := <-msgBus.OutboundChan():
			if outbound.Content == "done" {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("approved command did not run: %v", err)
				}
				cancel()
				<-runDone
				return
			}
		case <-ctx.Done():
			t.Fatal("approved turn did not finish")
		}
	}
}
