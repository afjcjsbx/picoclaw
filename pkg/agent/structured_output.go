package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/agent/interfaces"
	"github.com/sipeed/picoclaw/pkg/channels"
)

func supportsStructuredTurnOutput(manager interfaces.ChannelManager, channelName string) bool {
	if strings.EqualFold(strings.TrimSpace(channelName), "pico") {
		return true
	}
	if manager == nil {
		return false
	}
	channel, ok := manager.GetChannel(channelName)
	if !ok {
		return false
	}
	capable, ok := channel.(channels.StructuredTurnOutputCapable)
	return ok && capable.SupportsStructuredTurnOutput()
}
