package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestCronSessionKeyFromPicoChat(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  bus.InboundMessage
		want string
	}{
		{
			"webui cron entry",
			bus.InboundMessage{Context: bus.InboundContext{Channel: "pico", ChatID: "pico:agent_cron-job1-run1"}},
			"agent:cron-job1-run1",
		},
		{
			"explicit key wins",
			bus.InboundMessage{
				Context:    bus.InboundContext{Channel: "pico", ChatID: "pico:agent_cron-job1-run1"},
				SessionKey: "agent:cron-job1-other",
			},
			"agent:cron-job1-other",
		},
		{
			"ordinary pico chat",
			bus.InboundMessage{Context: bus.InboundContext{Channel: "pico", ChatID: "pico:shared-chat"}},
			"",
		},
		{
			"other channel with lookalike chat",
			bus.InboundMessage{Context: bus.InboundContext{Channel: "telegram", ChatID: "pico:agent_cron-job1-run1"}},
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveSessionKey(testInboundMessage(tc.msg)); got != tc.want {
				t.Fatalf("effectiveSessionKey() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A WebUI follow-up typed into a "[cron]" entry must land in the cron run's own
// session, otherwise reopening the entry hides it (it would sit in a separate
// opaque session with the same pico ID).
func TestProcessMessage_PicoCronEntryContinuesCronSession(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &simpleMockProvider{response: "ok"})
	defer al.Close()

	const cronKey = "agent:cron-job1-run1"
	ctx := context.Background()

	if _, err := al.ProcessDirectWithChannel(ctx, "cron prompt", cronKey, "pico", "pico:shared-chat"); err != nil {
		t.Fatalf("cron run error = %v", err)
	}
	if _, err := al.processMessage(ctx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "pico",
			ChatID:   "pico:agent_cron-job1-run1",
			ChatType: "direct",
			SenderID: "pico-user",
		},
		Content: "follow-up from web ui",
	}); err != nil {
		t.Fatalf("follow-up error = %v", err)
	}

	history, err := al.GetRegistry().GetDefaultAgent().Sessions.GetHistory(ctx, cronKey)
	if err != nil {
		t.Fatalf("GetHistory() error = %v", err)
	}
	var sawPrompt, sawFollowUp bool
	for _, m := range history {
		sawPrompt = sawPrompt || m.Content == "cron prompt"
		sawFollowUp = sawFollowUp || m.Content == "follow-up from web ui"
	}
	if !sawPrompt || !sawFollowUp {
		t.Fatalf("cron session history = %+v, want cron prompt and follow-up", history)
	}
}
