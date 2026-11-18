package agent

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/usage"
)

// sessionUsageCounter is implemented by session stores that keep cumulative
// usage per session.
type sessionUsageCounter interface {
	SessionUsage(ctx context.Context, sessionKey string) (usage.Summary, error)
}

// sessionUsageSummary returns the usage recorded for sessionKey. Stores that
// keep cumulative counters answer directly; other stores are summed from their
// persisted messages.
func sessionUsageSummary(
	ctx context.Context,
	agent *AgentInstance,
	sessionKey string,
) (*usage.Summary, error) {
	if agent == nil || agent.Sessions == nil || sessionKey == "" {
		return nil, nil
	}

	var summary usage.Summary
	if counters, ok := agent.Sessions.(sessionUsageCounter); ok {
		counted, err := counters.SessionUsage(ctx, sessionKey)
		if err != nil {
			return nil, err
		}
		summary = counted
	} else {
		history, err := agent.Sessions.GetHistory(ctx, sessionKey)
		if err != nil {
			return nil, err
		}
		summary = usage.FromMessages(history)
	}
	summary.SortModels()
	return &summary, nil
}
