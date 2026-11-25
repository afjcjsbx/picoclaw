package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func approvalBool(value bool) *bool { return &value }

func hardwareApprovalRequest(origin bus.InboundContext) *ToolApprovalRequest {
	return &ToolApprovalRequest{
		Tool: "i2c",
		Arguments: map[string]any{
			"action":  "write",
			"bus":     "1",
			"address": float64(0x76),
			"data":    []any{float64(0x01), float64(0x02)},
			"confirm": true,
		},
		Context: &TurnContext{Inbound: &origin},
	}
}

func enableHardwareWrites(al *AgentLoop) {
	al.cfg.Tools.I2C.AllowWrite = true
	al.cfg.Tools.I2C.Allow = []string{"1:0x76"}
	al.cfg.Tools.Hardware.RequireApproval = approvalBool(true)
}

func TestHardwareWriteApprovalApprove(t *testing.T) {
	t.Parallel()

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()
	enableHardwareWrites(al)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan ApprovalDecision, 1)
	go func() {
		done <- al.hooks.ApproveTool(ctx, hardwareApprovalRequest(origin))
	}()

	prompt := waitExecApprovalPrompt(t, mgr)
	for _, want := range []string{"hardware write", "i2c", "0x76", "0x01 0x02"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, missing %q", prompt, want)
		}
	}
	token := execApprovalToken(t, prompt)
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: origin,
		Content: "approve " + token,
	}) {
		t.Fatal("approval reply was not consumed")
	}

	select {
	case decision := <-done:
		if !decision.Approved {
			t.Fatalf("write denied: %s", decision.Reason)
		}
	case <-ctx.Done():
		t.Fatal("approval did not complete")
	}
}

func TestHardwareWriteApprovalDeny(t *testing.T) {
	t.Parallel()

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()
	enableHardwareWrites(al)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan ApprovalDecision, 1)
	go func() {
		done <- al.hooks.ApproveTool(ctx, hardwareApprovalRequest(origin))
	}()

	prompt := waitExecApprovalPrompt(t, mgr)
	token := execApprovalToken(t, prompt)
	if !al.tryHandleExecApproval(ctx, bus.InboundMessage{
		Context: origin,
		Content: "deny " + token,
	}) {
		t.Fatal("denial reply was not consumed")
	}

	select {
	case decision := <-done:
		if decision.Approved {
			t.Fatal("write was approved after deny")
		}
		if !strings.Contains(decision.Reason, "denied") {
			t.Fatalf("denial reason = %q", decision.Reason)
		}
	case <-ctx.Done():
		t.Fatal("approval did not complete")
	}
}

func TestHardwareWriteApprovalBypassesReadOnlyAndDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*AgentLoop)
	}{
		{name: "read-only", mutate: func(al *AgentLoop) { al.cfg.Tools.I2C.AllowWrite = false }},
		{name: "approval disabled", mutate: func(al *AgentLoop) {
			al.cfg.Tools.Hardware.RequireApproval = approvalBool(false)
		}},
		{name: "dry-run", mutate: func(al *AgentLoop) { al.cfg.Tools.Hardware.DryRun = true }},
		{name: "target not allowed", mutate: func(al *AgentLoop) {
			al.cfg.Tools.I2C.Allow = []string{"1:0x77"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
			defer cleanup()
			enableHardwareWrites(al)
			tt.mutate(al)

			decision := al.hooks.ApproveTool(context.Background(), hardwareApprovalRequest(origin))
			if !decision.Approved {
				t.Fatalf("request was denied: %s", decision.Reason)
			}
			noExecApprovalPrompt(t, mgr)
		})
	}
}

func TestHardwareApprovalIgnoresNonHardwareToolsWithoutConfig(t *testing.T) {
	t.Parallel()

	hook := &hardwareWriteApprovalHook{agent: &AgentLoop{}}
	decision, err := hook.ApproveTool(context.Background(), &ToolApprovalRequest{
		Tool:      "read_file",
		Arguments: map[string]any{"path": "x"},
	})
	if err != nil {
		t.Fatalf("ApproveTool() error = %v", err)
	}
	if !decision.Approved {
		t.Fatalf("non-hardware tool denied: %s", decision.Reason)
	}
}

func TestHardwareApprovalIgnoresReads(t *testing.T) {
	t.Parallel()

	al, mgr, origin, cleanup := newExecApprovalTestLoop(t)
	defer cleanup()
	enableHardwareWrites(al)

	req := hardwareApprovalRequest(origin)
	req.Arguments["action"] = "read"
	delete(req.Arguments, "confirm")
	decision := al.hooks.ApproveTool(context.Background(), req)
	if !decision.Approved {
		t.Fatalf("read was denied: %s", decision.Reason)
	}
	noExecApprovalPrompt(t, mgr)
}

func TestHardwareApprovalFailsClosedWithoutInteractiveUser(t *testing.T) {
	t.Parallel()

	t.Run("agent stopped", func(t *testing.T) {
		t.Parallel()
		al, _, origin, cleanup := newExecApprovalTestLoop(t)
		defer cleanup()
		enableHardwareWrites(al)
		al.running.Store(false)

		decision := al.hooks.ApproveTool(context.Background(), hardwareApprovalRequest(origin))
		if decision.Approved {
			t.Fatal("write was approved while the agent was stopped")
		}
		if !strings.Contains(decision.Reason, "no interactive user") {
			t.Fatalf("reason = %q", decision.Reason)
		}
	})

	t.Run("automated sender", func(t *testing.T) {
		t.Parallel()
		al, _, origin, cleanup := newExecApprovalTestLoop(t)
		defer cleanup()
		enableHardwareWrites(al)
		origin.SenderID = "cron"

		decision := al.hooks.ApproveTool(context.Background(), hardwareApprovalRequest(origin))
		if decision.Approved {
			t.Fatal("write was approved for an automated sender")
		}
		if !strings.Contains(decision.Reason, "no interactive user") {
			t.Fatalf("reason = %q", decision.Reason)
		}
	})
}
