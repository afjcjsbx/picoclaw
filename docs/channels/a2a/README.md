---
title: "A2A Channel"
description: "Let other agents discover and call PicoClaw over the A2A protocol (v1.0, JSON-RPC), and let PicoClaw call remote A2A agents with the a2a_* tools."
---
# 🤝 A2A (Agent2Agent)

[A2A](https://a2a-protocol.org/latest/specification/) lets agents discover each other and exchange tasks. PicoClaw speaks **A2A 1.0, JSON-RPC binding**, in both directions:

- **Inbound** — the `a2a` channel serves an Agent Card and accepts tasks from other agents.
- **Outbound** — five tools (`a2a_discover`, `a2a_call`, `a2a_list`, `a2a_history`, `a2a_orchestrate`) call agents you list in `a2a_agents`.

> Only the **1.0.0** wire format is supported (PascalCase methods such as `SendMessage`, parts without `kind`, `TASK_STATE_*` states). Slash-style method names such as `message/send` get error `-32009`.

## 🚀 Inbound: expose PicoClaw

```json
{
  "channel_list": {
    "a2a": {
      "enabled": true,
      "type": "a2a",
      "settings": { "port": 9900 }
    }
  }
}
```

```bash
picoclaw gateway
curl http://127.0.0.1:9900/.well-known/agent-card.json
```

Without `tokens` the listener binds to **127.0.0.1 only** and refuses to start on any other host. To expose it on the network, set a host and at least one bearer token (tokens go to `security.yml`):

```json
"settings": { "host": "0.0.0.0", "port": 9900, "url": "https://agent.example.com" }
```

```yaml
# security.yml
channel_list:
  a2a:
    settings:
      tokens: ["a-long-random-token"]
```

Each token is a separate **principal**: it can only see, list, cancel and subscribe to its own tasks, and its conversations are isolated from other tokens.

### Settings

| Field | Default | Meaning |
|---|---|---|
| `host` / `port` | `127.0.0.1` / `9900` | Dedicated listener |
| `url` | request `Host` | Public base URL put in the card (no `/a2a`) |
| `name` / `description` | PicoClaw | Card text |
| `tokens` | none | Bearer tokens (see above) |
| `max_body_bytes` | 1 MiB | Request body cap |
| `rate_limit_per_minute` | 60 | Per-IP budget on `/a2a` |
| `max_tasks` | 256 | Tasks kept in RAM |
| `max_concurrent` | 4 | Tasks running at once; more get HTTP 503 |
| `task_timeout_seconds` | 300 | Deadline per task |
| `streaming.enabled` | false | Token deltas on streams (also needs `streaming.enabled` on the model) |

### Supported methods

`SendMessage`, `SendStreamingMessage` (SSE), `GetTask`, `ListTasks` (with `nextPageToken`), `CancelTask`, `SubscribeToTask`. Push-notification methods return `-32003`, `GetExtendedAgentCard` returns `-32007`.

- **Blocking by default**: `SendMessage` waits for the task; `configuration.returnImmediately` returns at once.
- **Idempotent**: a retry with the same `messageId` returns the existing task and does not run a second turn. Reusing a `messageId` with a different `contextId` is rejected with `-32602`.
- **Cancel is real**: `CancelTask` ends the task and sends `/stop` to the agent turn. It is best-effort for a command already blocked outside PicoClaw.
- **Streams**: the first frame is the task snapshot, then `statusUpdate` / `artifactUpdate` frames; the last artifact chunk has `lastChunk: true`; the stream ends on a terminal state. A `: keepalive` comment is sent every 20 s. A dropped client does **not** cancel the task; re-attach with `SubscribeToTask`.
- **One task per context**: a second message for a `contextId` that still has a running task is rejected (`-32010`). `contextId` maps to a PicoClaw session (`1-128` chars of `[A-Za-z0-9._-]`).

> **Sessions**: a `contextId` becomes the chat of a PicoClaw session (`a2a:<principal>:<contextId>`), so conversations are isolated as long as `session.dimensions` includes `chat` or `sender` (the default is `["chat"]`). With `dm_scope: "global"` every A2A context shares one session, like any other channel.

### Limits (by design)

- Text parts only (`text/plain` in and out). Other part types return `-32005`.
- Tasks live in RAM, expire 1 h after finishing, and are lost on restart; the conversation itself stays in the session store.
- `input-required` / `auth-required` are never produced.
- A failure notice produced by the agent loop arrives as the task's reply text (state `completed`); only timeouts, cancels, shutdown and delivery errors yield `failed`/`canceled`.

## 📤 Outbound: call other agents

```json
{
  "a2a_agents": {
    "researcher": {
      "url": "http://192.168.1.100:9900",
      "auth": { "type": "bearer" },
      "timeout": 120,
      "capabilities": ["web_search", "research"]
    }
  }
}
```

```yaml
# security.yml
a2a_agents:
  researcher:
    auth:
      token: "TOKEN"
```

The tools appear only when `a2a_agents` has at least one valid entry.

| Tool | Does |
|---|---|
| `a2a_discover` | Fetch the Agent Card (skills, modes) of one or all agents |
| `a2a_call` | Send a task and wait; pass `context_id` to continue a conversation |
| `a2a_list` | Configured agents and the last 32 conversations (no network) |
| `a2a_history` | Remote message history of a `task_id` |
| `a2a_orchestrate` | Up to 8 independent tasks, 4 in flight at a time |

The bearer token is only ever sent to the origin you configured: if a card advertises an endpoint on another host it is ignored.

## 🪶 Resource notes

- No background loops: one accept goroutine, one goroutine per live request, one timer per running task. Expiry is swept lazily.
- RAM is bounded: ≤ `max_tasks` entries, ≤ 64 KiB reply + 2 KiB request stored per task, ≤ 32 queued frames per stream (a slow reader is dropped, not buffered), ≤ 4096 tracked IPs, remote responses capped at 1 MiB.
- Token deltas are only produced when a streaming client is attached; blocking callers use the cheaper non-streaming path.
