---
description: "PicoClaw provides QQ support via the official Bot API from the QQ Open Platform."
---
> Back to [README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md)

# QQ

PicoClaw provides QQ support via the official Bot API from the QQ Open Platform.

## Configuration

```json
{
  "channel_list": {
    "qq": {
      "enabled": true,
      "type": "qq",
      "app_id": "YOUR_APP_ID",
      "app_secret": "YOUR_APP_SECRET",
      "allow_from": [],
      "placeholder": { "enabled": true, "text": ["Thinking..."] },
      "group_trigger": { "mention_only": false },
      "reasoning_channel_id": "",
      "settings": {
        "max_message_length": 0,
        "max_base64_file_size_mib": 0,
        "send_markdown": false
      }
    }
  }
}
```

| Field      | Type   | Required | Description                                              |
| ---------- | ------ | -------- | -------------------------------------------------------- |
| enabled    | bool   | Yes      | Whether to enable the QQ channel                         |
| app_id     | string | Yes      | App ID of the QQ bot application                         |
| app_secret | string | Yes      | App Secret of the QQ bot application                     |
| allow_from | array  | No       | Allowlist of user IDs; empty means all users are allowed |
| placeholder | object | No      | Placeholder message shown while the agent is working     |
| group_trigger | object | No    | Group trigger settings (e.g. `{ "mention_only": true }`) |
| reasoning_channel_id | string | No | Optional channel ID to route reasoning output to a separate chat |
| max_message_length | int | No | Maximum characters per outbound message before splitting. `0` uses the platform default. Env: `PICOCLAW_CHANNELS_QQ_MAX_MESSAGE_LENGTH` |
| max_base64_file_size_mib | int | No | Maximum file size in MiB to send inline as base64. Files above this limit are skipped. `0` uses the platform default. Env: `PICOCLAW_CHANNELS_QQ_MAX_BASE64_FILE_SIZE_MIB` |
| send_markdown | bool | No | When `true`, send messages using QQ's Markdown message format. Env: `PICOCLAW_CHANNELS_QQ_SEND_MARKDOWN` |

## Setup

### Quick Setup (Recommended)

The QQ Open Platform provides a one-click creation entry:

1. Open [QQ Bot Quick Create](https://q.qq.com/qqbot/openclaw/index.html) and log in by scanning the QR code
2. The system automatically creates a bot — copy the **App ID** and **App Secret**
3. Fill in the credentials in the PicoClaw configuration file
4. Run `picoclaw gateway` to start the service
5. Open QQ and start chatting with the bot

> The App Secret is only shown once — save it immediately. Viewing it again will force a reset.
>
> Bots created via the quick entry are for the creator's personal use only and do not support group chats. For group chat support, configure sandbox mode on the [QQ Open Platform](https://q.qq.com/).

### Manual Setup

1. Log in to the [QQ Open Platform](https://q.qq.com/) with your QQ account and register as a developer
2. Create a QQ bot and customize its avatar and name
3. Obtain the **App ID** and **App Secret** from the bot settings
4. Fill in the credentials in the PicoClaw configuration file
5. Run `picoclaw gateway` to start the service
6. Search for your bot in QQ and start chatting

> During development, it is recommended to enable sandbox mode and add test users and groups to the sandbox for debugging.
