package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/usage"
)

// sessionUsageSummary aggregates provider-reported usage from the persisted
// messages of sessionKey. Terminal assistant messages carry the aggregate of
// every LLM call in their turn, while tool-call assistant messages carry none,
// so summing all messages that have usage does not double count iterations.
func sessionUsageSummary(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
) (*usage.Summary, error) {
	if agent == nil || agent.Sessions == nil || sessionKey == "" {
		return nil, nil
	}
	history, err := agent.Sessions.GetHistory(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	summary := usage.FromMessages(history)
	return &summary, nil
}
