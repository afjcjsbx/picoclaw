package tools

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// ApprovalRequest is the information a human approver needs to decide whether
// an opaque command may run. It deliberately carries the policy rule and risk
// so an approval hook can present a precise prompt instead of the raw tool
// arguments alone.
type ApprovalRequest struct {
	Command string
	Rule    string
	Reason  string
	Risk    shellguard.RiskLevel
}

// ApprovalFunc asks a human (or an external approval system) for a decision.
// Returning an error is a rejection; it is never interpreted as consent.
type ApprovalFunc func(ctx context.Context, req ApprovalRequest) (bool, error)

// SetApprovalFunc wires the per-command human approval hook. The agent layer
// sets this when a ToolApprover is configured; an ExecTool without one fails
// closed on review decisions unless allow_unknown is enabled.
func (t *ExecTool) SetApprovalFunc(fn ApprovalFunc) {
	if t == nil {
		return
	}
	t.approvalFunc = fn
}

// humanApproval asks the configured human approver. A missing approver and an
// error both reject the command.
func (t *ExecTool) humanApproval(
	ctx context.Context, command string, decision shellguard.Decision,
) string {
	if t.approvalFunc == nil {
		return "Command blocked: manual approval is not configured"
	}
	approved, err := t.approvalFunc(ctx, ApprovalRequest{
		Command: command,
		Rule:    decision.Rule,
		Reason:  decision.Reason,
		Risk:    decision.Risk,
	})
	if err != nil {
		return fmt.Sprintf("Command blocked: approval request failed: %v", err)
	}
	if !approved {
		return "Command blocked by manual approval"
	}
	return ""
}
