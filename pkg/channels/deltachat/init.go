package deltachat

import (
	"github.com/afjcjsbx/picoclaw/pkg/bus"
	"github.com/afjcjsbx/picoclaw/pkg/channels"
	"github.com/afjcjsbx/picoclaw/pkg/config"
)

func init() {
	channels.RegisterFactory(
		config.ChannelDeltaChat,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			bc := cfg.Channels[channelName]
			if bc == nil || !bc.Enabled {
				return nil, nil
			}
			decoded, err := bc.GetDecoded()
			if err != nil {
				return nil, err
			}
			c, ok := decoded.(*config.DeltaChatSettings)
			if !ok {
				return nil, channels.ErrSendFailed
			}
			ch, err := NewDeltaChatChannel(bc, c, b)
			if err != nil {
				return nil, err
			}
			if channelName != config.ChannelDeltaChat {
				ch.SetName(channelName)
			}
			return ch, nil
		},
	)
}
