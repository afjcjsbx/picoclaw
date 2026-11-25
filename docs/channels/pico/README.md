---
description: "Pico is PicoClaw's native WebSocket protocol. Use the pico channel to serve it, or pico_client to connect to a remote pico server."
---
> Back to [README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md)

# Pico / Pico Client

PicoClaw ships a native WebSocket protocol called **Pico**. Two channel types use it:

- **`pico`** — runs a WebSocket server on this instance so external clients (the Web UI, scripts, other agents) can connect to it.
- **`pico_client`** — connects as a WebSocket client to a remote `pico` server, forwarding messages in both directions.

---

## pico (server)

### Configuration

```json
{
  "channel_list": {
    "pico": {
      "enabled": true,
      "type": "pico",
      "allow_from": [],
      "settings": {
        "token": "NOT_HERE",
        "allow_token_query": false,
        "allow_origins": [],
        "streaming": {
          "enabled": false,
          "throttle_seconds": 0,
          "min_growth_chars": 0
        },
        "ping_interval": 30,
        "read_timeout": 60,
        "max_connections": 0
      }
    }
  }
}
```

Store the token in `~/.picoclaw/.security.yml`:

```yaml
channel_list:
  pico:
    settings:
      token: "your-secret-token"
```

### Configuration Fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `token` | Yes | — | Bearer token clients must supply in the `Authorization` header or the `token.<value>` WebSocket subprotocol. Stored in `.security.yml` |
| `allow_token_query` | No | `false` | When `true`, also accepts the token via the `?token=` query parameter. Useful for browser WebSocket clients that cannot set headers |
| `allow_origins` | No | `[]` | List of allowed `Origin` header values. Empty allows all origins. Use `["*"]` as an explicit wildcard |
| `streaming.enabled` | No | `false` | Enable streaming replies over the WebSocket connection |
| `streaming.throttle_seconds` | No | `0` | Minimum seconds between partial streaming updates |
| `streaming.min_growth_chars` | No | `0` | Minimum character growth before a streaming update is sent |
| `ping_interval` | No | `30` | Seconds between WebSocket ping frames sent to the client |
| `read_timeout` | No | `60` | Seconds of inactivity before a client connection is closed |
| `max_connections` | No | `0` | Maximum concurrent WebSocket connections. `0` means unlimited |

### Environment Variables

| Variable | Field |
|----------|-------|
| `PICOCLAW_CHANNELS_PICO_TOKEN` | `token` |

### Setup

1. Set a strong secret `token` in `.security.yml`.
2. Enable the channel and start the gateway:

```bash
picoclaw gateway
```

The pico server binds on the shared gateway HTTP server (`gateway.host`:`gateway.port`, default `127.0.0.1:18790`) under the path `/ws`.

---

## pico_client (outbound connector)

`pico_client` connects to a remote pico server and relays messages from that server into the local agent loop.

### Configuration

```json
{
  "channel_list": {
    "pico_client": {
      "enabled": true,
      "type": "pico_client",
      "settings": {
        "url": "wss://remote-host:18790/ws",
        "token": "NOT_HERE",
        "session_id": "",
        "ping_interval": 30,
        "read_timeout": 60
      }
    }
  }
}
```

Store the token in `~/.picoclaw/.security.yml`:

```yaml
channel_list:
  pico_client:
    settings:
      token: "remote-secret-token"
```

### Configuration Fields

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `url` | Yes | — | WebSocket URL of the remote pico server, e.g. `wss://host:18790/ws` |
| `token` | No | — | Bearer token to authenticate with the remote server. Stored in `.security.yml` |
| `session_id` | No | — | Optional session identifier to resume an existing conversation on the remote server |
| `ping_interval` | No | `30` | Seconds between WebSocket ping frames |
| `read_timeout` | No | `60` | Seconds of inactivity before the connection is considered stale and reconnected |

### Environment Variables

| Variable | Field |
|----------|-------|
| `PICOCLAW_CHANNELS_PICO_CLIENT_URL` | `url` |
| `PICOCLAW_CHANNELS_PICO_CLIENT_TOKEN` | `token` |

### Setup

1. Configure the `url` and optionally the `token` in `.security.yml`.
2. Start the gateway:

```bash
picoclaw gateway
```

`pico_client` reconnects automatically when the connection is lost.
