package dingtalk

import (
	"github.com/afjcjsbx/picoclaw/pkg/bus"
	"github.com/afjcjsbx/picoclaw/pkg/channels"
	"github.com/afjcjsbx/picoclaw/pkg/config"
)

func init() {
	channels.RegisterFactory(
		config.ChannelDingTalk,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			bc := cfg.Channels[channelName]
			decoded, err := bc.GetDecoded()
			if err != nil {
				return nil, err
			}
			c, ok := decoded.(*config.DingTalkSettings)
			if !ok {
				return nil, channels.ErrSendFailed
			}
			ch, err := NewDingTalkChannel(bc, c, b)
			if err != nil {
				return nil, err
			}
			if channelName != config.ChannelDingTalk {
				ch.SetName(channelName)
			}
			return ch, nil
		},
	)
}
