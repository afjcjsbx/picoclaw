package agent

import "context"

// ApprovalRequest describes an interactive tool authorization request.
type ApprovalRequest struct {
	SessionKey string
	TurnID     string
	ToolCallID string
	Tool       string
	Arguments  map[string]any
	Command    string
	Reason     string
}

// ApprovalBroker routes interactive approval requests to a channel-specific
// client, such as an ACP client.
type ApprovalBroker interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}

func (al *AgentLoop) RegisterApprovalBroker(channel string, broker ApprovalBroker) {
	if al == nil || broker == nil {
		return
	}
	al.approvalBrokerMu.Lock()
	defer al.approvalBrokerMu.Unlock()
	if al.approvalBrokers == nil {
		al.approvalBrokers = make(map[string]ApprovalBroker)
	}
	al.approvalBrokers[channel] = broker
}

func (al *AgentLoop) approvalBroker(channel string) ApprovalBroker {
	if al == nil {
		return nil
	}
	al.approvalBrokerMu.RLock()
	defer al.approvalBrokerMu.RUnlock()
	return al.approvalBrokers[channel]
}
