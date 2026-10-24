package mail

import (
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func init() {
	channels.RegisterFactory(
		config.ChannelMail,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			bc := cfg.Channels[channelName]
			if bc == nil || !bc.Enabled {
				return nil, nil
			}
			decoded, err := bc.GetDecoded()
			if err != nil {
				return nil, err
			}
			settings, ok := decoded.(*config.MailSettings)
			if !ok {
				return nil, channels.ErrSendFailed
			}
			ch, err := NewMailChannel(bc, settings, b, cfg.WorkspacePath())
			if err != nil {
				return nil, err
			}
			if channelName != config.ChannelMail {
				ch.SetName(channelName)
			}
			return ch, nil
		},
	)
}
