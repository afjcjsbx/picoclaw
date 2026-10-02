package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestIsTrustedDirectSender(t *testing.T) {
	cfg := &config.Config{Channels: config.ChannelsConfig{
		"telegram": {AllowFrom: config.FlexibleStringSlice{"123"}},
	}}
	trusted := bus.InboundMessage{
		Channel:  "telegram",
		SenderID: "telegram:123",
		Context:  bus.InboundContext{ChatType: "direct"},
		Sender:   bus.SenderInfo{PlatformID: "123"},
	}
	if !isTrustedDirectSender(cfg, trusted) {
		t.Fatal("allowlisted direct sender should be trusted")
	}
	trusted.Context.ChatType = "group"
	if isTrustedDirectSender(cfg, trusted) {
		t.Fatal("group sender should remain untrusted")
	}
	trusted.Context.ChatType = "direct"
	trusted.Sender.PlatformID = "other"
	if isTrustedDirectSender(cfg, trusted) {
		t.Fatal("unknown direct sender should remain untrusted")
	}

	for _, msg := range []bus.InboundMessage{
		{Context: bus.InboundContext{Channel: "cli", ChatType: "direct", SenderID: "cron"}, SenderID: "cron"},
		{Context: bus.InboundContext{Channel: "telegram", ChatType: "direct", SenderID: "cron"}, SenderID: "cron"},
		{Context: bus.InboundContext{Channel: "telegram", ChatType: "direct", SenderID: "heartbeat"}, SenderID: "heartbeat"},
	} {
		if !isTrustedDirectSender(&config.Config{}, msg) {
			t.Errorf("internal message should not be wrapped: %+v", msg)
		}
	}
}
