//go:build !custom_channels

package builtin

import (
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/deltachat"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/dingtalk"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/discord"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/feishu"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/irc"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/line"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/maixcam"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/mqtt"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/onebot"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/pico"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/qq"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/slack"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/slack_webhook"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/teams_webhook"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/telegram"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/vk"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/wecom"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/weixin"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/whatsapp"
	_ "github.com/afjcjsbx/picoclaw/pkg/channels/whatsapp_native"
)
