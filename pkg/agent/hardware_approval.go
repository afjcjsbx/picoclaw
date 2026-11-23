package agent

import (
	"context"
	"time"

	hardwaretools "github.com/sipeed/picoclaw/pkg/tools/hardware"
)

// hardwareWriteApprovalHook asks the originating user before a hardware write
// when the operator enabled writes and kept approval enabled. It reads the
// current config on every call, so a hot reload applies without remounting.
type hardwareWriteApprovalHook struct {
	agent *AgentLoop
}

func (h *hardwareWriteApprovalHook) ApprovalTimeout() time.Duration {
	if h == nil || h.agent == nil {
		return 0
	}
	cfg := h.agent.GetConfig()
	if cfg == nil {
		return 0
	}
	return time.Duration(cfg.Tools.Hardware.GetApprovalTimeoutSeconds()) * time.Second
}

func (h *hardwareWriteApprovalHook) ApproveTool(
	ctx context.Context,
	req *ToolApprovalRequest,
) (ApprovalDecision, error) {
	if h == nil || h.agent == nil || req == nil {
		return ApprovalDecision{Approved: true}, nil
	}
	write, ok := hardwaretools.DescribeWrite(req.Tool, req.Arguments)
	if !ok {
		return ApprovalDecision{Approved: true}, nil
	}

	cfg := h.agent.GetConfig()
	if cfg == nil {
		return ApprovalDecision{Reason: "configuration is unavailable"}, nil
	}

	var allowWrite bool
	var allow []string
	switch req.Tool {
	case "i2c":
		allowWrite = cfg.Tools.I2C.AllowWrite
		allow = cfg.Tools.I2C.Allow
	case "spi":
		allowWrite = cfg.Tools.SPI.AllowWrite
		allow = cfg.Tools.SPI.Allow
	case "serial":
		allowWrite = cfg.Tools.Serial.AllowWrite
		allow = cfg.Tools.Serial.Allow
	default:
		return ApprovalDecision{Approved: true}, nil
	}

	// The tool itself will return a precise write-disabled or allowlist error;
	// prompting first would only ask the user to approve a call that cannot run.
	if !allowWrite || len(allow) == 0 || cfg.Tools.Hardware.DryRun {
		return ApprovalDecision{Approved: true}, nil
	}
	if !cfg.Tools.Hardware.GetRequireApproval() {
		return ApprovalDecision{Approved: true}, nil
	}
	if !hardwaretools.TargetAllowed(req.Tool, write.Target, allow) {
		return ApprovalDecision{Approved: true}, nil
	}

	return h.agent.askToolApproval(
		ctx,
		req,
		"Approval required for a hardware write.",
		write.Summary,
		write.Summary,
		"",
		"write",
	)
}
