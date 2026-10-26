# Gateway HTTP API

PicoClaw exposes two HTTP servers:

- The **Gateway** (default `127.0.0.1:18790`, configured via `gateway.host`/`gateway.port`): health endpoints, webhook handlers for HTTP-based channels, and the Pico WebSocket endpoint.
- The **Launcher** (default `127.0.0.1:18800`, see the [Web UI & Launcher Guide](../guides/webui.md)): the dashboard's REST API and the authenticated Pico proxy.

There are no generic REST chat endpoints — chat traffic uses the Pico protocol described below.

## Gateway Endpoints

| Method | Path | Purpose | Auth |
| --- | --- | --- | --- |
| `GET` | `/health` | Liveness check; JSON `{status:"ok", uptime, pid}` | None |
| `GET` | `/ready` | Readiness check; returns 503 until the Gateway reports ready | None |
| `POST` | `/reload` | Schedule an asynchronous config reload; returns `202 {"status":"reload scheduled",...}` (503 when reload is unavailable, 405 for other methods) | Bearer token |
| `GET` | `/pico/ws` | Pico channel WebSocket (only when the `pico` channel is enabled) | Pico token |
| `GET`/`HEAD` | `/pico/media/{id}` | Attachment download (`content-disposition: inline` for images) | Pico token |
| `POST` | `/webhook/line` | LINE channel webhook; path configurable via `LINE.webhook_path` | LINE signature |

Notes:

- **`/reload` auth**: the Gateway generates a token at startup and stores it in the PID file (`.picoclaw.pid`). Send it as `Authorization: Bearer <token>`. An empty token disables the check.
- **Channel handlers**: HTTP-based channels register their own routes on the same server (for example the LINE webhook above). A channel that is not enabled does not expose its routes.
- **Docker**: the `gateway` compose profile exposes only webhooks and health endpoints; use the `launcher` profile (or `PICOCLAW_GATEWAY_HOST=0.0.0.0`) for host access. See the [Docker & Quick Start Guide](../guides/docker.md).

## Pico WebSocket Protocol

`/pico/ws` is the JSON protocol used by the Web UI chat, the `pico` channel clients, and channel proxies. When the `pico` channel is enabled:

- **Authentication**: `Authorization: Bearer <pico-token>`, or `Sec-WebSocket-Protocol: token.<token>` (echoed back on upgrade), or `?token=` only when `allow_token_query` is enabled.
- **Session**: pass `?session_id=<id>` on the URL to continue an existing session; otherwise the server generates one. The channel treats the conversation as `pico:<sessionID>`.
- **Limits**: the endpoint returns 503 when the channel is not running or the connection limit (`max_connections`, default 100) is exceeded.

Every frame is a JSON envelope:

```json
{
  "type": "message.send",
  "id": "optional-frame-id",
  "session_id": "optional-session-id",
  "timestamp": 0,
  "payload": {}
}
```

| Direction | `type` values |
| --- | --- |
| Client → server | `message.send`, `media.send`, `ping` |
| Server → client | `message.create`, `message.update`, `message.delete`, `media.create`, `typing.start`, `typing.stop`, `error`, `pong` |

Payload keys of interest: `content`, `message_id`, `kind` (`thought`, `tool_calls`, `tool_feedback`), `tool_calls`, `model_name`, `usage`, and the legacy `thought` boolean.

## Launcher API

The launcher serves the dashboard SPA and a REST API for everything the UI does. All routes require the dashboard session cookie except the public login/setup pages and `/api/auth/*`.

| Group | Representative endpoints | Purpose |
| --- | --- | --- |
| Auth | `POST /api/auth/setup`, `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/auth/status` | Dashboard password lifecycle (public) |
| Pico proxy | `GET /api/pico/info`, `POST /api/pico/token`, `POST /api/pico/setup`, `GET /pico/ws`, `GET /pico/media/{id}` | Chat proxy to the Gateway; frontend never talks to the Gateway directly |
| Config | `GET`/`PUT`/`PATCH /api/config`, `/api/config/reset`, `/api/config/test-command-patterns` | Read and edit `config.json` |
| Gateway lifecycle | `/api/gateway/status`, `/logs`, `/logs/clear`, `/start`, `/stop`, `/restart` | Manage the `picoclaw gateway` subprocess and its log buffer |
| Models | `/api/models*` (CRUD, `/default-chain`, `/catalog`, `/{index}/test`, `/test-inline`) | Model list management |
| Channels | `/api/channels/catalog`, `/api/channels/{name}/config` | Channel configuration catalog |
| Sessions | `/api/sessions`, `/api/sessions/{id}`, `/api/sessions/{id}/fork` | Chat history |
| Skills | `/api/skills*` | Browse, import, and remove skills |
| Tools | `/api/tools`, `/api/tools/{name}/state`, `/api/tools/web-search-config` | Tool availability and settings |
| OAuth | `/api/oauth/providers`, `/login`, `/flows/{id}`, `/flows/{id}/poll`, `/logout`, `/oauth/callback` | Provider OAuth flows |
| QR flows | `/api/weixin/flows*`, `/api/wecom/flows*` | WeChat/WeCom QR binding |
| System | `/api/system/launcher-config`, `/api/system/autostart`, `/api/system/version`, `POST /api/update` | Launcher settings and self-update |

Unauthenticated requests behave as follows: dashboard pages redirect to `/launcher-login`, `/api/*` returns `401` JSON, and the `/pico/ws` proxy returns `401` plain text. The proxy injects the Pico token into upstream connections, so browser clients never handle it directly.

## Availability Summary

| Surface | Endpoints | Condition |
| --- | --- | --- |
| Gateway health | `/health`, `/ready` | Always |
| Gateway reload | `/reload` | Always (requires the PID-file token when set) |
| Pico | `/pico/ws`, `/pico/media/{id}` | `pico` channel enabled with a non-empty token (the launcher auto-enables it) |
| Channel webhooks | `/webhook/*` | The respective channel enabled |
| Launcher API | `/api/*`, `/pico/*` proxy | Launcher running; dashboard cookie required (503 when the Gateway is down for the Pico proxy) |
