---
description: "Slack is a leading enterprise instant messaging platform. PicoClaw uses Slack's Socket Mode for real-time bidirectional communication."
---
> Back to [README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md)

# Slack

Slack is a leading enterprise instant messaging platform. PicoClaw uses Slack's Socket Mode for real-time bidirectional communication, with no need to configure a public webhook endpoint.

## Configuration

```json
{
  "channel_list": {
    "slack": {
      "enabled": true,
      "type": "slack",
      "bot_token": "xoxb-...",
      "app_token": "xapp-...",
      "allow_from": [],
      "placeholder": { "enabled": true, "text": ["Thinking..."] },
      "group_trigger": { "mention_only": false },
      "reasoning_channel_id": ""
    }
  }
}
```

| Field      | Type   | Required | Description                                                              |
| ---------- | ------ | -------- | ------------------------------------------------------------------------ |
| enabled    | bool   | Yes      | Whether to enable the Slack channel                                      |
| bot_token  | string | Yes      | Bot User OAuth Token for the Slack bot (starts with xoxb-)               |
| app_token  | string | Yes      | Socket Mode App Level Token for the Slack app (starts with xapp-)        |
| allow_from | array  | No       | User ID whitelist; empty means all users are allowed                     |
| placeholder | object | No      | Placeholder message shown while the agent is working                     |
| group_trigger | object | No    | Group trigger settings (e.g. `{ "mention_only": true }`)                 |
| reasoning_channel_id | string | No | Optional channel ID to route reasoning/thinking output to a separate channel |

## Setup

1. Go to [Slack API](https://api.slack.com/) and create a new Slack app
2. Enable Socket Mode and obtain the App Level Token
3. Add Bot Token Scopes (e.g. `chat:write`, `im:history`, etc.)
4. Install the app to your workspace and obtain the Bot User OAuth Token
5. Fill in the Bot Token and App Token in the configuration file
