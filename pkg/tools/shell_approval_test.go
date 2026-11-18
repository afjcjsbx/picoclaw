package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

type fakeHumanApprover struct {
	approved bool
	err      error
	calls    int
	request  ApprovalRequest
}

func (a *fakeHumanApprover) Approve(_ context.Context, req ApprovalRequest) (bool, error) {
	a.calls++
	a.request = req
	return a.approved, a.err
}

func TestHumanApprovalIsRequiredForReviewDecision(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	human := &fakeHumanApprover{approved: true}
	tool.SetApprovalFunc(human.Approve)

	if got := tool.guardCommand("python3 script.py", t.TempDir()); got != "" {
		t.Fatalf("guardCommand() = %q, want human-approved", got)
	}
	if human.calls != 1 {
		t.Fatalf("human calls = %d, want 1", human.calls)
	}
	if human.request.Command != "python3 script.py" || human.request.Rule != "risk.unknown" {
		t.Fatalf("approval request = %+v", human.request)
	}
}

func TestHumanApprovalCanDenyReviewDecision(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	human := &fakeHumanApprover{approved: false}
	tool.SetApprovalFunc(human.Approve)

	got := tool.guardCommand("python3 script.py", t.TempDir())
	if !strings.Contains(got, "manual approval") {
		t.Fatalf("guardCommand() = %q, want manual denial", got)
	}
	if human.calls != 1 {
		t.Fatalf("human calls = %d, want 1", human.calls)
	}
}

func TestHumanApprovalErrorBlocks(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	tool, err := NewExecToolWithConfig(t.TempDir(), false, cfg)
	if err != nil {
		t.Fatalf("NewExecToolWithConfig: %v", err)
	}
	human := &fakeHumanApprover{err: errors.New("approval channel unavailable")}
	tool.SetApprovalFunc(human.Approve)

	got := tool.guardCommand("python3 script.py", t.TempDir())
	if !strings.Contains(got, "approval request failed") {
		t.Fatalf("guardCommand() = %q, want fail-closed approval error", got)
	}
}
