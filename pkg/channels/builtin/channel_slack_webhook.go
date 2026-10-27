//go:build custom_channels && channel_slack_webhook

package builtin

import (
	_ "github.com/sipeed/picoclaw/pkg/channels/slack_webhook"
)
