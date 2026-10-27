# Messaging tools

Messaging tools let the agent talk to chat channels directly. They are most useful in the gateway (where a channel manager delivers messages) and in subagents, which report results to the user through the `message` tool instead of the parent conversation.

| Tool | What it does | Switch (default) |
|------|--------------|------------------|
| `message` | Send text and/or local media to a chat channel | `tools.message.enabled` (`true`) |
| `reaction` | Add a reaction to a message | always registered |

## `message`

Sends a message to the user on a chat channel. Supports text-only, media-only, or text with media attachments. Requires a running gateway (or another host that wires a send callback); in a bare CLI session without channel context the call fails with "No target channel/chat specified".

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.message.enabled` | bool | `true` | Register the `message` tool |
| `tools.message.media_enabled` | bool | `false` | Allow local media attachments through the `media` argument |

Environment variables: `PICOCLAW_TOOLS_MESSAGE_ENABLED`, `PICOCLAW_TOOLS_MESSAGE_MEDIA_ENABLED`.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `content` | string | yes | Message text. When media is present it becomes the caption/body. Empty content is accepted only when media is attached |
| `channel` | string | no | Target channel (for example `telegram`, `discord`). Defaults to the current conversation's channel |
| `chat_id` | string | no | Target chat/user ID. Defaults to the current conversation |
| `reply_to_message_id` | string | no | Reply target for channels that support threaded replies |
| `media` | array | no | Local media attachments. Only advertised when `media_enabled` is `true` |

Each `media` item has:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `path` | string | yes | Path to the local file; relative paths resolve from the workspace |
| `type` | string | no | Optional media type hint: `image`, `audio`, `video`, or `file` |
| `filename` | string | no | Optional display filename |

Media files go through the same read policy as other filesystem reads: protected credential files are refused, and the size limit is `agents.defaults.max_media_size` (20 MB by default). Delivery goes through the shared media store, so [media cleanup](media.md#media-cleanup) applies.

## `reaction`

Adds a lightweight reaction (for example a thumb-up) to a message, defaulting to the current inbound message. The tool has no configuration switch: it is always registered, and fails gracefully on channels that do not implement reactions.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `message_id` | string | no | Target message ID; defaults to the current inbound message |
| `channel` | string | no | Target channel. Defaults to the current conversation's channel |
| `chat_id` | string | no | Target chat/user ID. Defaults to the current conversation |

Reaction support is checked at call time against the channel implementation (Slack, Feishu, and OneBot implement it); on unsupported channels the tool returns an error and the agent continues normally. The gateway also uses the same capability to acknowledge inbound messages with a working reaction and remove it when the reply is sent.
