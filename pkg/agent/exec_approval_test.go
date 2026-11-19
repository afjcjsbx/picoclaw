package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type execApprovalTestManager struct {
	recordingChannelManager
	sent chan bus.OutboundMessage
}

func (m *execApprovalTestManager) GetChannel(string) (channels.Channel, bool) {
	return &fakeChannel{}, true
}

func (m *execApprovalTestManager) SendMessage(_ context.Context, msg bus.OutboundMessage) error {
	m.sent <- msg
	return nil
}

func newExecApprovalTestLoop(
	t *testing.T,
) (*AgentLoop, *execApprovalTestManager, bus.InboundContext, func()) {
	t.Helper()

	al, _, cleanup := newHookTestLoop(t, &toolHookProvider{})
	al.cfg.Tools.Exec.EnableDenyPatterns = true
	al.cfg.Tools.Exec.RiskThreshold = "medium"
	al.cfg.Tools.Exec.AllowRemote = true
	al.running.Store(true)

	mgr := &execApprovalTestManager{sent: make(chan bus.OutboundMessage, 8)}
	al.channelManager = mgr
	origin := bus.InboundContext{
		Channel:  "telegram",
		Account:  "bot",
		ChatID:   "chat",
		TopicID:  "topic",
		SenderID: "alice",
	}
	return al, mgr, origin, cleanup
}

func execApprovalRequest(origin bus.InboundContext, command string) *ToolApprovalRequest {
	return &ToolApprovalRequest{
		Tool:      "exec",
		Arguments: map[string]any{"action": "run", "command": command},
		Context:   &TurnContext{Inbound: &origin},
	}
}

func waitExecApprovalPrompt(t *testing.T, mgr *execApprovalTestManager) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-mgr.sent:
			if strings.Contains(msg.Content, "Approval required") {
				return msg.Content
			}
		case <-deadline:
			t.Fatal("approval prompt was not sent")
			return ""
		}
	}
}

func execApprovalToken(t *testing.T, prompt string) string {
	t.Helper()
	match := regexp.MustCompile(`approve ([0-9a-f]{32})`).FindStringSubmatch(prompt)
	if len(match) != 2 {
		t.Fatalf("prompt omits approval token: %q", prompt)
	}
	return match[1]
}

func noExecApprovalPrompt(t *testing.T, mgr *execApprovalTestManager) {
	t.Helper()
	select {
	case msg := <-mgr.sent:
		t.Fatalf("unexpected approval prompt: %q", msg.Content)
	default:
	}
}

func requireUnixRiskClassifier(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the exec risk classifier is Unix-only")
	}
}

func TestExecUnknownApprovalKnownCommandsBypassPrompt(t *testing.T) {
	t.Parallel()

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, req := range []*ToolApprovalRequest{
		execApprovalRequest(origin, "ls -la"),
		execApprovalRequest(origin, "echo ok"),
		{Tool: "exec", Arguments: map[string]any{"action": "poll"}, Context: &TurnContext{Inbound: &origin}},
		{Tool: "read_file", Arguments: map[string]any{"path": "x"}, Context: &TurnContext{Inbound: &origin}},
	} {
		if decision := al.hooks.ApproveTool(ctx, req); !decision.Approved {
			t.Fatalf("request %q was denied: %s", req.Tool, decision.Reason)
		}
	}
	noExecApprovalPrompt(t, mgr)
}

func TestExecUnknownApprovalApproveAndDeny(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := execApprovalRequest(origin, "docker ps")
	done := make(chan ApprovalDecision, 1)
	go func() { done <- al.hooks.ApproveTool(ctx, req) }()

	prompt := waitExecApprovalPrompt(t, mgr)
	if !strings.Contains(prompt, "unknown exec command") || !strings.Contains(prompt, `"docker ps"`) {
		t.Fatalf("prompt does not describe the command: %q", prompt)
	}
	if !strings.Contains(prompt, "not in the risk table") {
		t.Fatalf("prompt does not explain the unknown reason: %q", prompt)
	}
	token := execApprovalToken(t, prompt)
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: origin,
		Content: "@picoclaw approve " + token,
	}) {
		t.Fatal("approval reply was not intercepted")
	}
	select {
	case decision := <-done:
		if !decision.Approved {
			t.Fatalf("approved command was denied: %s", decision.Reason)
		}
	case <-ctx.Done():
		t.Fatal("approval did not complete")
	}

	done = make(chan ApprovalDecision, 1)
	go func() { done <- al.hooks.ApproveTool(ctx, req) }()
	prompt = waitExecApprovalPrompt(t, mgr)
	token = execApprovalToken(t, prompt)
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: origin,
		Content: "deny " + token,
	}) {
		t.Fatal("denial reply was not intercepted")
	}
	select {
	case decision := <-done:
		if decision.Approved {
			t.Fatal("explicitly denied command was approved")
		}
	case <-ctx.Done():
		t.Fatal("denial did not complete")
	}
}

func TestExecUnknownApprovalPolicyModes(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := execApprovalRequest(origin, "docker ps")

	al.cfg.Tools.Exec.UnknownCommandPolicy = config.ExecUnknownCommandAllow
	if approval := al.hooks.ApproveTool(ctx, req); !approval.Approved {
		t.Fatalf("allow policy denied the command: %s", approval.Reason)
	}
	noExecApprovalPrompt(t, mgr)

	al.cfg.Tools.Exec.UnknownCommandPolicy = config.ExecUnknownCommandBlock
	decision := al.hooks.ApproveTool(ctx, req)
	if decision.Approved {
		t.Fatal("block policy approved an unknown command")
	}
	if !strings.Contains(decision.Reason, "unknown_command_policy") {
		t.Fatalf("block policy reason = %q", decision.Reason)
	}
	noExecApprovalPrompt(t, mgr)

	al.cfg.Tools.Exec.UnknownCommandPolicy = config.ExecUnknownCommandAsk
	al.cfg.Tools.Exec.EnableDenyPatterns = false
	if approval := al.hooks.ApproveTool(ctx, req); !approval.Approved {
		t.Fatalf("disabled classifier denied the command: %s", approval.Reason)
	}
	noExecApprovalPrompt(t, mgr)

	al.cfg.Tools.Exec.EnableDenyPatterns = true
	long := strings.Repeat("a", maxExecApprovalCommandRunes+1)
	decision = al.hooks.ApproveTool(ctx, execApprovalRequest(origin, long))
	if decision.Approved {
		t.Fatal("overlong unknown command was approved")
	}
	if !strings.Contains(decision.Reason, "too long") {
		t.Fatalf("overlong reason = %q", decision.Reason)
	}
	noExecApprovalPrompt(t, mgr)
}

func TestExecUnknownApprovalSkipsPromptForDeniedCommands(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()
	al.cfg.Tools.Exec.CustomDenyPatterns = []string{`docker\s+rm`}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, command := range []string{"docker rm web", "docker ps; shutdown now"} {
		// The exec guard blocks these with a precise error, so the hook must
		// not ask the user to approve a command that cannot run.
		if decision := al.hooks.ApproveTool(ctx, execApprovalRequest(origin, command)); !decision.Approved {
			t.Fatalf("%q was denied by the hook: %s", command, decision.Reason)
		}
		noExecApprovalPrompt(t, mgr)
	}
}

func TestExecUnknownApprovalShortReply(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply := func(from bus.InboundContext, content string) bool {
		return al.tryHandleExecApproval(ctx, bus.InboundMessage{Context: from, Content: content})
	}
	ask := func(command string) chan ApprovalDecision {
		done := make(chan ApprovalDecision, 1)
		go func() { done <- al.hooks.ApproveTool(ctx, execApprovalRequest(origin, command)) }()
		return done
	}

	// Nothing pending: the word is ordinary chat and must reach the LLM.
	if reply(origin, "approve") {
		t.Fatal("short reply was consumed without a pending request")
	}

	done := ask("docker ps")
	waitExecApprovalPrompt(t, mgr)

	// Another user cannot answer for the requester.
	other := origin
	other.SenderID = "bob"
	if reply(other, "approve") {
		t.Fatal("short reply from another sender was consumed")
	}
	select {
	case <-done:
		t.Fatal("another sender approved the command")
	default:
	}

	if !reply(origin, "/approve") {
		t.Fatal("short reply was not intercepted")
	}
	if decision := <-done; !decision.Approved {
		t.Fatalf("short approve denied the command: %s", decision.Reason)
	}

	done = ask("docker ps")
	waitExecApprovalPrompt(t, mgr)
	if !reply(origin, "deny") {
		t.Fatal("short denial was not intercepted")
	}
	if decision := <-done; decision.Approved {
		t.Fatal("short deny approved the command")
	}

	// Two live requests: the short form is ambiguous and must not pick one.
	first, second := ask("docker ps"), ask("docker images")
	token := execApprovalToken(t, waitExecApprovalPrompt(t, mgr))
	waitExecApprovalPrompt(t, mgr)
	if !reply(origin, "approve") {
		t.Fatal("ambiguous short reply was not intercepted")
	}
	select {
	case <-first:
		t.Fatal("ambiguous short reply answered a request")
	case <-second:
		t.Fatal("ambiguous short reply answered a request")
	case <-time.After(100 * time.Millisecond):
	}
	if !reply(origin, "deny "+token) {
		t.Fatal("tokenized reply was not intercepted")
	}
}

func TestExecUnknownApprovalUsesExecTimeout(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()
	// The generic hook deadline is far shorter than the exec approval wait.
	al.hooks.ConfigureTimeouts(0, 0, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	done := make(chan ApprovalDecision, 1)
	go func() { done <- al.hooks.ApproveTool(ctx, execApprovalRequest(origin, "docker ps")) }()

	prompt := waitExecApprovalPrompt(t, mgr)
	if !strings.Contains(prompt, "expires in 5m0s") {
		t.Fatalf("prompt does not show the default 300s wait: %q", prompt)
	}
	select {
	case decision := <-done:
		t.Fatalf("request ended before the exec approval timeout: %+v", decision)
	case <-time.After(200 * time.Millisecond):
	}

	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{Context: origin, Content: "approve"}) {
		t.Fatal("reply was not intercepted")
	}
	if decision := <-done; !decision.Approved {
		t.Fatalf("approved command was denied: %s", decision.Reason)
	}

	al.cfg.Tools.Exec.ApprovalTimeoutSeconds = 1
	go func() { done <- al.hooks.ApproveTool(ctx, execApprovalRequest(origin, "docker ps")) }()
	if prompt := waitExecApprovalPrompt(t, mgr); !strings.Contains(prompt, "expires in 1s") {
		t.Fatalf("prompt ignores approval_timeout_seconds: %q", prompt)
	}
	if decision := <-done; decision.Approved {
		t.Fatal("unanswered request was approved")
	}
}

func TestExecUnknownApprovalFailsClosedWithoutUser(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := execApprovalRequest(origin, "docker ps")

	al.running.Store(false)
	if decision := al.hooks.ApproveTool(ctx, req); decision.Approved {
		t.Fatal("unattended unknown command was approved")
	}

	al.running.Store(true)
	al.channelManager = nil
	if decision := al.hooks.ApproveTool(ctx, req); decision.Approved {
		t.Fatal("unknown command without a channel was approved")
	}

	al.channelManager = mgr
	origin.SenderID = "cron"
	if decision := al.hooks.ApproveTool(ctx, execApprovalRequest(origin, "docker ps")); decision.Approved {
		t.Fatal("automated sender was allowed to approve an unknown command")
	}
	noExecApprovalPrompt(t, mgr)
}

func TestExecUnknownApprovalRemoteGateSkipsPrompt(t *testing.T) {
	t.Parallel()

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	al.cfg.Tools.Exec.AllowRemote = false
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if decision := al.hooks.ApproveTool(ctx, execApprovalRequest(origin, "docker ps")); !decision.Approved {
		t.Fatalf("remote command was denied by the approval hook: %s", decision.Reason)
	}
	noExecApprovalPrompt(t, mgr)
}

func TestExecUnknownApprovalRequiresOriginalSenderAndTopic(t *testing.T) {
	t.Parallel()
	requireUnixRiskClassifier(t)

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan ApprovalDecision, 1)
	go func() { done <- al.hooks.ApproveTool(ctx, execApprovalRequest(origin, "docker ps")) }()

	prompt := waitExecApprovalPrompt(t, mgr)
	token := execApprovalToken(t, prompt)

	other := origin
	other.SenderID = "bob"
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: other,
		Content: "approve " + token,
	}) {
		t.Fatal("reply from another sender was not intercepted")
	}
	select {
	case <-done:
		t.Fatal("another sender approved the command")
	default:
	}

	other = origin
	other.TopicID = "another-topic"
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: other,
		Content: "approve " + token,
	}) {
		t.Fatal("reply from another topic was not intercepted")
	}
	select {
	case <-done:
		t.Fatal("another topic approved the command")
	default:
	}

	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: origin,
		Content: "approve " + token,
	}) {
		t.Fatal("reply from the requester was not intercepted")
	}
	select {
	case decision := <-done:
		if !decision.Approved {
			t.Fatalf("requester approval was denied: %s", decision.Reason)
		}
	case <-ctx.Done():
		t.Fatal("approval did not complete")
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
			ID:        "exec-1",
			Name:      "exec",
			Arguments: map[string]any{"action": "run", "command": "./marker.sh"},
		}}}, nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *execApprovalProvider) GetDefaultModel() string { return "exec-approval-test" }

func TestExecUnknownApprovalRunsOnlyAfterConfirmation(t *testing.T) {
	requireUnixRiskClassifier(t)

	al, agent, cleanup := newHookTestLoop(t, &execApprovalProvider{})
	defer cleanup()

	al.cfg.Tools.Exec.EnableDenyPatterns = true
	al.cfg.Tools.Exec.RiskThreshold = "medium"
	al.cfg.Tools.Exec.AllowRemote = true
	mgr := &execApprovalTestManager{sent: make(chan bus.OutboundMessage, 4)}
	al.channelManager = mgr

	execTool, err := tools.NewExecToolWithConfig(agent.Workspace, true, al.cfg)
	if err != nil {
		t.Fatal(err)
	}
	al.RegisterTool(execTool)

	target := filepath.Join(agent.Workspace, "approved.txt")
	script := filepath.Join(agent.Workspace, "marker.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf approved > approved.txt\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()

	origin := bus.InboundContext{Channel: "telegram", ChatID: "chat", SenderID: "alice"}
	msgBus := al.bus.(*bus.MessageBus)
	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{
		Context: origin,
		Content: "run the marker script",
	}); err != nil {
		t.Fatal(err)
	}

	prompt := waitExecApprovalPrompt(t, mgr)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("command ran before approval: %v", err)
	}
	token := execApprovalToken(t, prompt)
	if err := msgBus.PublishInbound(ctx, bus.InboundMessage{
		Context: origin,
		Content: "approve " + token,
	}); err != nil {
		t.Fatal(err)
	}

	for {
		select {
		case outbound := <-msgBus.OutboundChan():
			if outbound.Content != "done" {
				continue
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("approved command did not run: %v", err)
			}
			cancel()
			<-runDone
			return
		case <-ctx.Done():
			t.Fatal("approved turn did not finish")
		}
	}
}
