---
lang: zh
description: "Discord 是一个专为社区设计的免费语音、视频和文本聊天应用。PicoClaw 通过 Discord Bot API 连接到 Discord 服务器，支持接收和发送消息。"
---

> 返回 [README](../../project/README.zh.md)

# Discord

Discord 是一个专为社区设计的免费语音、视频和文本聊天应用。PicoClaw 通过 Discord Bot API 连接到 Discord 服务器，支持接收和发送消息。

## 配置

```json
{
  "channel_list": {
    "discord": {
      "enabled": true,
      "type": "discord",
      "allow_from": ["YOUR_USER_ID"],
      "commands_enabled": true,
      "group_trigger": {
        "mention_only": false
      },
      "settings": {
        "token": "YOUR_BOT_TOKEN",
        "allow_dm": true,
        "allow_attachments": true,
        "channel_ids": ["YOUR_ENABLED_CHANNEL_ID"]
      }
    }
  }
}
```

| 字段                       | 类型   | 必填 | 描述                                                                                                       |
| -------------------------- | ------ | ---- | ---------------------------------------------------------------------------------------------------------- |
| enabled                    | bool   | 是   | 是否启用 Discord 频道                                                                                      |
| settings.token             | string | 是   | Discord 机器人 Token                                                                                       |
| allow_from                 | array  | 否   | 用户 ID 白名单，空表示允许所有用户                                                                         |
| commands_enabled           | bool   | 否   | 是否启用此频道的斜杠命令。设为 `false` 会清空 Discord 命令菜单，并由 Agent 拒绝所有斜杠命令，默认为 `true` |
| group_trigger              | object | 否   | 群组触发设置（示例: { "mention_only": false }）                                                            |
| settings.allow_dm          | bool   | 否   | 是否接受私信和 DM 中的斜杠命令，默认为 `true`。环境变量：`PICOCLAW_CHANNELS_DISCORD_ALLOW_DM`              |
| settings.allow_attachments | bool   | 否   | 是否下载并处理入站附件，默认为 `true`。环境变量：`PICOCLAW_CHANNELS_DISCORD_ALLOW_ATTACHMENTS`             |
| settings.channel_ids       | array  | 否   | PicoClaw 允许回复的 Discord 服务器频道 ID 白名单；留空表示所有服务器频道                                   |

将 `allow_dm` 设置为 `false` 后，机器人会忽略私信和 DM 中的交互，同时继续处理服务器频道中的提及。

将 `allow_attachments` 设置为 `false` 后，机器人会忽略用户发送的文件。附件不会被下载或转发给 Agent，但消息文本仍会处理；仅包含附件的消息将被忽略。

将 `commands_enabled` 设置为 `false` 后，启动时会清空 Discord 斜杠命令菜单，并由 Agent 拒绝所有斜杠命令。此设置作用于整个 Discord 集成，而不是单个服务器或文本频道。

## 限制可回复的频道

将 `settings.channel_ids` 用作允许 PicoClaw 回复的 Discord 服务器频道白名单。`group_trigger` 仍然决定消息如何触发回复：

```json
{
  "group_trigger": {
    "mention_only": false
  },
  "settings": {
    "channel_ids": ["123456789012345678", "234567890123456789"]
  }
}
```

在此配置下，PicoClaw 仅在列出的两个频道中回复，并会自动回复，无需提及。

- 未列出的服务器频道会被忽略，即使提及机器人。
- 若 `mention_only: true`，列出的频道仅在被明确提及时回复。
- 配置的触发前缀在列出的频道中仍然生效。
- `channel_ids` 不影响私信；是否接受私信由 `settings.allow_dm` 控制。
- `channel_ids` 为空或省略时允许所有服务器频道；`group_trigger` 仍然生效。

请填写 Discord 频道 ID，而不是频道名称。在 Discord 中启用开发者模式，然后右键频道并选择**复制频道 ID**。也可以通过 `PICOCLAW_CHANNELS_DISCORD_CHANNEL_IDS` 设置逗号分隔的 ID。

## 设置流程

1. 前往 [Discord 开发者门户](https://discord.com/developers/applications) 创建一个新的应用
2. 启用 Intents:
   - Message Content Intent
   - Server Members Intent
3. 获取 Bot Token
4. 将 Bot Token 填入配置文件中
5. 邀请机器人加入服务器并授予必要权限(例如发送消息、读取消息历史等)
