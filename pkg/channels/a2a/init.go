package a2a

import (
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func init() {
	channels.RegisterSafeFactory(
		config.ChannelA2A,
		func(bc *config.Channel, cfg *config.A2ASettings, b *bus.MessageBus) (channels.Channel, error) {
			return New(bc, cfg, b)
		},
	)
}
