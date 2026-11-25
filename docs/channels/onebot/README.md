---
description: "OneBot is an open protocol standard for QQ bots, providing a unified interface for various QQ bot implementations (e.g. go-cqhttp, Mirai)."
---
> Back to [README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md)

# OneBot

OneBot is an open protocol standard for QQ bots, providing a unified interface for various QQ bot implementations (e.g. go-cqhttp, Mirai). It uses WebSocket for communication.

## Configuration

```json
{
  "channel_list": {
    "onebot": {
      "enabled": true,
      "type": "onebot",
      "ws_url": "ws://localhost:8080",
      "access_token": "",
      "allow_from": [],
      "placeholder": { "enabled": true, "text": ["Thinking..."] },
      "group_trigger": { "mention_only": false },
      "reasoning_channel_id": "",
      "settings": {
        "reconnect_interval": 5
      }
    }
  }
}
```

| Field        | Type   | Required | Description                                                      |
| ------------ | ------ | -------- | ---------------------------------------------------------------- |
| enabled      | bool   | Yes      | Whether to enable the OneBot channel                             |
| ws_url       | string | Yes      | WebSocket URL of the OneBot server                               |
| access_token | string | No       | Access token for connecting to the OneBot server                 |
| allow_from   | array  | No       | User ID whitelist; empty means all users are allowed             |
| placeholder  | object | No       | Placeholder message shown while the agent is working             |
| group_trigger | object | No      | Group trigger settings (e.g. `{ "mention_only": true }`)         |
| reasoning_channel_id | string | No | Optional channel ID to route reasoning/thinking output to a separate chat |
| reconnect_interval | int | No | Seconds to wait before reconnecting after a connection loss. Default `5`. Env: `PICOCLAW_CHANNELS_ONEBOT_RECONNECT_INTERVAL` |

## Setup

1. Deploy a OneBot-compatible implementation (e.g. napcat)
2. Configure the OneBot implementation to enable the WebSocket service and set an access token (if needed)
3. Fill in the WebSocket URL and access token in the configuration file
