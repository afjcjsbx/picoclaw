package tools

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// resolveGuardDecision applies the approval policy to a policy decision and
// returns the model-facing block reason, or "" when the command may run.
//
// ActionBlock is final. ActionReview is the only path that may consult the
// human approver, when one is wired. The legacy allow_unknown switch applies
// only to risk.unknown, never to parse errors or to a command blocked by
// another layer.
func (t *ExecTool) resolveGuardDecision(
	ctx context.Context, command string, decision shellguard.Decision,
) string {
	switch decision.Action {
	case shellguard.ActionAllow:
		return ""
	case shellguard.ActionBlock:
		if decision.Reason == "" {
			return "Command blocked by safety guard"
		}
		return decision.Reason
	case shellguard.ActionReview:
		return t.reviewCommand(ctx, command, decision)
	default:
		return "Command blocked by safety guard (unknown policy action)"
	}
}

// reviewCommand runs the approval chain for a review decision.
//
// The human approver is the final decision when configured: a denial, a
// timeout or an approver error always blocks. Without a human approver, only
// the legacy allow_unknown switch can authorize an opaque command; parse
// errors are blocked before this point.
func (t *ExecTool) reviewCommand(
	ctx context.Context, command string, decision shellguard.Decision,
) string {
	if t.approvalFunc != nil {
		return t.humanApproval(ctx, command, decision)
	}
	if decision.Rule == "risk.unknown" && t.allowUnknown {
		return ""
	}
	return unknownBlockedReason(decision.Reason)
}
