package loop

import (
	"testing"

	agentcore "github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	agenttools "github.com/sipeed/picoclaw/pkg/tools"
)

func TestCronTurnExcludesCronTool(t *testing.T) {
	registry := agenttools.NewToolRegistry()
	registry.Register(&allowlistTestTool{name: "cron"})
	registry.Register(&allowlistTestTool{name: "read_file"})
	agent := &agentcore.AgentInstance{ID: "test", Tools: registry}

	for _, tc := range []struct {
		name       string
		sessionKey string
		senderID   string
		wantCron   bool
	}{
		{"scheduled turn", "agent:cron-job-123", "cron", false},
		{"scheduled subturn", "agent:subturn-123", "cron", false},
		{"scheduled session without sender", "agent:cron-job-456", "", false},
		{"ordinary turn", "agent:default:chat", "direct", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTurnState(agent, processOptions{Dispatch: DispatchRequest{
				SessionKey:     tc.sessionKey,
				InboundContext: &bus.InboundContext{SenderID: tc.senderID},
			}}, turnEventScope{})

			_, hasCron := ts.agent.Tools.Get("cron")
			if hasCron != tc.wantCron {
				t.Fatalf("cron tool available = %v, want %v", hasCron, tc.wantCron)
			}
			if _, ok := ts.agent.Tools.Get("read_file"); !ok {
				t.Fatal("unrelated tool was removed")
			}
			for _, def := range ts.agent.Tools.ToProviderDefs() {
				if def.Function.Name == "cron" && !tc.wantCron {
					t.Fatal("cron was offered to the model")
				}
			}
			if _, ok := agent.Tools.Get("cron"); !ok {
				t.Fatal("shared agent registry was modified")
			}
		})
	}
}
