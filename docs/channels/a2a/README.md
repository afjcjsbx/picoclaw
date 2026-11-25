---
title: "A2A Channel"
description: "Let other agents discover and call PicoClaw over the A2A protocol (v1.0, JSON-RPC), and let PicoClaw call remote A2A agents with the a2a_* tools."
---
# 🤝 A2A (Agent2Agent)

[A2A](https://a2a-protocol.org/latest/specification/) lets agents discover each other and exchange tasks. PicoClaw speaks **A2A 1.0, JSON-RPC binding**, in both directions:

- **Inbound** — the `a2a` channel serves an Agent Card and accepts tasks from other agents.
- **Outbound** — eight tools (`a2a_discover`, `a2a_call`, `a2a_poll`, `a2a_stream`, `a2a_cancel`, `a2a_list`, `a2a_history`, `a2a_orchestrate`) call agents you list in `a2a_agents`.

> Only the **1.0.0** wire format is supported (PascalCase methods such as `SendMessage`, parts without `kind`, `TASK_STATE_*` states). Slash-style method names such as `message/send` get error `-32009`, and so does an `A2A-Version` header other than `1.0` (or `1.0.x`).

## 🚀 Inbound: expose PicoClaw

Open **Channels → A2A** in the Web UI to enable the listener, configure its
address and Agent Card, and manage bearer tokens. The page uses the same save
and gateway-restart flow as the other channels.

You can also configure it directly:

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

Each token is a separate **principal**: it can only see, list, cancel and subscribe to its own tasks, and its conversations are isolated from other tokens. Tokens do **not** isolate the agent itself: see [Shared workspace](#shared-workspace).

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
| `max_concurrent` | 4 | Tasks running at once, shared by all tokens (the agent is one resource); more get HTTP 503 |
| `task_timeout_seconds` | 300 | Deadline per task |
| `streaming.enabled` | false | Token deltas on streams (also needs `streaming.enabled` on the model) |
| `push_allow_private` | false | Allow push webhooks to loopback / private-network hosts (see [Push notifications](#push-notifications)) |

### Supported methods

`SendMessage`, `SendStreamingMessage` (SSE), `GetTask`, `ListTasks` (with `nextPageToken`), `CancelTask`, `SubscribeToTask`, and the four push-notification methods (`Create/Get/List/DeleteTaskPushNotificationConfig`). `GetExtendedAgentCard` returns `-32007`.

- **Blocking by default**: `SendMessage` waits for the task; `configuration.returnImmediately` returns at once.
- **Idempotent**: a retry with the same `messageId` returns the existing task and does not run a second turn. Reusing a `messageId` with a different `contextId` is rejected with `-32602`.
- **Cancel is real**: `CancelTask` ends the task and sends `/stop` to the agent turn. It is best-effort for a command already blocked outside PicoClaw.
- **Streams**: the first frame is the task snapshot, then `statusUpdate` / `artifactUpdate` frames; the last artifact chunk has `lastChunk: true`; the stream ends on a terminal state. A `: keepalive` comment is sent every 20 s. A dropped client does **not** cancel the task; re-attach with `SubscribeToTask`.
- **One task per context**: a second message for a `contextId` that still has a running task is rejected (`-32010`). `contextId` maps to a PicoClaw session (`1-128` chars of `[A-Za-z0-9._-]`).

> **Sessions**: a `contextId` becomes the chat of a PicoClaw session (`a2a:<principal>:<contextId>`), so conversations are isolated as long as `session.dimensions` includes `chat` or `sender` (the default is `["chat"]`). With `dm_scope: "global"` every A2A context shares one session, like any other channel.

### Push notifications

A client that cannot hold a connection open registers a webhook and gets a `POST` when the task ends. Register it inline (`configuration.taskPushNotificationConfig` in `SendMessage`, usually with `returnImmediately`) or later with `CreateTaskPushNotificationConfig` (flat params: `taskId`, optional `id`, `url`, `token`, `authentication: {scheme, credentials}`). The card advertises `capabilities.pushNotifications: true`.

- **What is sent**: one `POST` per webhook when the task reaches a terminal state, body `{"task": {...}}` (a `StreamResponse` with the final task, artifacts included, no history), `Content-Type: application/a2a+json`. `authentication` becomes `Authorization: <scheme> <credentials>`; `token` is sent as `X-A2A-Notification-Token`.
- **What is not**: `working` transitions, tool-progress notes and token deltas are not pushed (use `SubscribeToTask` for those), and `input-required` is never produced anyway.
- **Delivery**: at least once, 3 attempts (1 s, then 4 s apart) on network errors, `408`, `429` and `5xx`; other `4xx` are final. 10 s timeout per request, 4 deliveries in flight at once. Receivers must answer `2xx` and be idempotent.
- **Limits**: 3 webhooks per task; `url` ≤ 1024 chars, `token` ≤ 128, `credentials` ≤ 512. Configs live in RAM only and are dropped (with their secrets) when the task finishes, so `Get/List` return nothing for finished tasks and `Create` on one returns `-32004`. `token` and `credentials` are write-only: responses never echo them.
- **SSRF guard** (spec: agents SHOULD reject private targets): the URL must be `http(s)` without credentials, and the connection itself refuses loopback, private, link-local and CGNAT addresses, so a hostname that resolves there (DNS rebinding) is blocked too. Redirects and proxies are never followed. Set `push_allow_private: true` for agents on the same LAN.

### Shared workspace

Tokens separate tasks and sessions, not the agent. Every principal talks to the same agent, so they share its workspace, long-term memory (`memory/`, `USER.md`) and tools. If one client asks the agent to "remember" something, the agent may write it to `memory/MEMORY.md`, and any other client or token can then get it back by asking. Issue tokens only to callers you trust with that workspace; for untrusted callers run a separate PicoClaw instance (own workspace) per caller.

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
| `a2a_call` | Send a task and wait; pass `context_id` to continue a conversation. With `async: true` it returns a `task_id` at once and the answer arrives later as a new message |
| `a2a_poll` | One `GetTask` on a `task_id`: current state and answer so far, no waiting |
| `a2a_stream` | `SendStreamingMessage` (`message`) or `SubscribeToTask` (`task_id`) over SSE: waits without polling and reports the remote's progress notes. Needs a peer with `streaming=true` |
| `a2a_cancel` | `CancelTask` on a `task_id` |
| `a2a_list` | Configured agents and the last 32 conversations (no network) |
| `a2a_history` | Remote message history of a `task_id` |
| `a2a_orchestrate` | Up to 8 independent tasks, 4 in flight at a time |

**Async tasks.** `a2a_call async=true` submits with `returnImmediately` and starts one watcher per pending task (max 8): it subscribes with `SubscribeToTask` (the socket is idle until the remote has news) and falls back to `GetTask` polling (1 s → 5 s) for peers that cannot stream. When the task settles, the result is delivered to the conversation that started it, like a finished `spawn`. If the peer deadline (`timeout`, default 120 s) passes first the watcher reports that the task may still be running and does **not** cancel it: use `a2a_poll` / `a2a_cancel`. Blocking `a2a_call` benefits too: when a peer answers `SendMessage` before the task is done, it now waits over SSE and fetches the result once, instead of running a poll loop.

PicoClaw does not register push webhooks when it is the *client*: that would need an inbound, publicly reachable endpoint, while the SSE watcher only dials out and works behind NAT.

The bearer token is only ever sent to the origin you configured: if a card advertises an endpoint on another host it is ignored.

## 🪶 Resource notes

- No background loops: one accept goroutine, one goroutine per live request, one timer per running task. Expiry is swept lazily.
- RAM is bounded: ≤ `max_tasks` entries, ≤ 64 KiB reply + 2 KiB request stored per task, ≤ 32 queued frames per stream (a slow reader is dropped, not buffered), ≤ 4096 tracked IPs, remote responses capped at 1 MiB.
- Push notifications cost nothing until a task with a webhook finishes: no goroutine, timer or socket is held (`DisableKeepAlives`); a delivery is one short-lived goroutine per webhook, ≤ ~64 KiB body. Stored webhooks are ≤ 3 × ~2 KiB per running task and freed at completion.
- An async task watched by the client holds one goroutine and one idle socket (≈ 20 KiB: goroutine stack, read buffers, socket); a stream is folded into one task capped at 16 artifacts, 64 parts and 256 KiB of text per artifact, whatever the peer sends.
- Token deltas are only produced when a streaming client is attached; blocking callers use the cheaper non-streaming path.
