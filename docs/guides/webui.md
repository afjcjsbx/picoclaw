# Web UI & Launcher Guide

The **PicoClaw Launcher** (`picoclaw-launcher`) is the browser-based interface for configuring and chatting with PicoClaw. It is the recommended way to get started on desktop: no command-line knowledge required.

The launcher is more than a UI — it runs as a small service that manages the `picoclaw gateway` subprocess for you, serves the dashboard, and proxies chat traffic between the browser and the Gateway.

> [!NOTE]
> Auth internals (password storage, session lifetime, rate limiting) are documented in [Configuration — Web launcher dashboard](configuration.md#web-launcher-dashboard). Docker-specific setup lives in the [Docker & Quick Start Guide](docker.md).

## Starting the Launcher

If you installed PicoClaw with the quick installer, both `picoclaw` and `picoclaw-launcher` are already on your machine. Otherwise download `picoclaw-launcher` from [GitHub Releases](https://github.com/afjcjsbx/picoclaw/releases) or build it with `make build-launcher`.

```bash
picoclaw-launcher
# The browser opens at http://localhost:18800
```

On Windows and Linux desktop builds, a system tray icon appears while the launcher runs; double-clicking `picoclaw-launcher` (or `picoclaw-launcher.exe`) starts it the same way. On macOS, the first launch may be blocked by Gatekeeper — see the note in the [repository README](https://github.com/afjcjsbx/picoclaw#readme).

### Launcher flags

| Flag | Default | Description |
| --- | --- | --- |
| `-port` | `18800` | Listen port (1–65535) |
| `-host` | `""` | Bind host; when set, overrides `-public` |
| `-public` | `false` | Bind all interfaces instead of localhost |
| `-no-browser` | `false` | Do not auto-open the browser |
| `-lang` | auto | UI language (`en` or `zh`) |
| `-console` | `false` | Console mode: no tray, logs to the terminal |
| `-d, --debug` | `false` | Debug logging |
| *(positional)* | `~/.picoclaw/config.json` | Config path; `PICOCLAW_CONFIG` is honored too |

```bash
picoclaw-launcher -port 18800          # default local-only access
picoclaw-launcher -no-browser -console # headless-friendly
```

The launcher stores its own settings (port, public flag, IP allowlists) in `launcher-config.json`, next to `config.json`. Explicit flags win over stored values for that run.

## First Run

1. If no `config.json` exists yet, the launcher automatically runs the equivalent of `picoclaw onboard` to create the configuration and workspace.
2. The browser opens the **setup page** (`/launcher-setup`): create a dashboard password (at least 8 characters). It is stored as a bcrypt hash in `launcher-auth.db` (or in `launcher-config.json` on platforms without the SQLite store).
3. After setup, the dashboard opens. When the launcher auto-opens a **local** browser, it logs you in automatically via a one-shot loopback endpoint; manual sign-ins happen at `/launcher-login`.

Sessions last about 31 days but are invalidated when the launcher restarts, so a restart asks you to log in again. Login attempts are rate-limited per client IP.

## Dashboard Tour

| Page | What you can do |
| --- | --- |
| `/` | Chat with the agent, browse session history, pick the default model |
| `/models` | Add, edit, delete, and set the default model (API-key, OAuth, and local/CLI models) |
| `/credentials` | Manage provider credentials (OpenAI, Anthropic, Google Antigravity) |
| `/channels` | Configure any supported channel from a shared catalog; QR-based binding for WeChat and WeCom |
| `/agent/hub`, `/agent/skills`, `/agent/tools` | Browse and import skills; view and toggle agent tools |
| `/config` | Edit agent defaults, self-evolution, exec/cron controls, heartbeat, device monitoring, and launcher networking/autostart |
| `/logs` | Follow the in-memory Gateway log buffer and clear it |

### Voice messages

The chat composer has a microphone button to the left of the send button. Press it to record a voice message; press the stop button (or wait for the 5-minute limit) to send it immediately, so no extra send step is needed. The recording is uploaded to the Gateway and, when a voice transcription model is configured, transcribed before the agent replies. Audio never leaves your machine except for the transcription request to the configured provider. Browsers without `MediaRecorder` hide the button or show an error. The microphone API is only available in a secure context, so when the dashboard is opened over plain HTTP on a LAN address the button explains that HTTPS or localhost is required; use `http://localhost:18800` on the launcher machine or terminate TLS in front of it.

The **Gateway** runs as a subprocess managed through the launcher (`picoclaw gateway -E`, with `-d` when debug logging is on). The top bar lets you start, stop, and restart it, and shows its state. Auto-start only happens when a usable default model is configured; otherwise use **Start Gateway** after adding credentials in `/models` or `/credentials`.

## Remote Access

By default the launcher listens on `127.0.0.1` only. To reach the dashboard from another machine:

```bash
picoclaw-launcher -public        # bind all interfaces, or
picoclaw-launcher -host 0.0.0.0  # explicit bind address
```

Remote-facing deployments are controlled by the launcher config (also editable from the dashboard's config page):

| Setting | Meaning |
| --- | --- |
| `allowed_cidrs` | IP allowlist for non-local clients |
| `allow_localhost_bypass` | Allow localhost without restriction (default `true`) |
| `trusted_proxy_cidrs` | CIDRs whose forwarding headers are trusted (for reverse proxies) |

> [!WARNING]
> Never expose the launcher directly to the public internet. The dashboard is protected by its password and the IP allowlist; if you need public access, put it behind a reverse proxy with TLS. The session cookie is only marked `Secure` when TLS is terminated in front of the launcher (or `X-Forwarded-Proto: https` is trusted).

## Tray, Autostart, and Logs

- **Tray icon** (Windows, Linux, and macOS builds with cgo): Open dashboard, About (version, GitHub, docs), Restart Gateway, Quit. Builds without tray support run headless and log "running without tray".
- **Launch at login**: enable it from the config page (or `PUT /api/system/autostart`). Supported on macOS (LaunchAgent), Linux (XDG autostart), and Windows (registry `Run` key); changes apply at next login.
- **Logs**: GUI mode writes to `~/.picoclaw/logs/launcher.log` (with `launcher_panic.log` for crashes). Use `-console` to print the banner and logs to the terminal instead.

## Docker

The Docker launcher profile starts the same Web UI inside a container:

```bash
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Open http://localhost:18800
```

The container image includes both `picoclaw` and the launcher. See the [Docker & Quick Start Guide](docker.md) for first-run steps (`docker/data/config.json`) and how the `gateway` profile differs from `launcher`.

## Troubleshooting

- **macOS blocks the launcher on first launch** — approve it in System Settings → Privacy & Security → Open Anyway; see the [repository README](https://github.com/afjcjsbx/picoclaw#readme).
- **Dashboard keeps asking me to log in** — sessions are invalidated on launcher restart; log in at `/launcher-login`.
- **"Start Gateway" is disabled** — no usable default model is configured. Add a provider in `/models` or credentials in `/credentials`.
- **Remote clients cannot connect** — start the launcher with `-public` (or `-host`) and check `allowed_cidrs`.
- **Port already in use** — run with `picoclaw-launcher -port 18900` or change the port in the config page.

For platform-specific issues, see [Troubleshooting](../operations/troubleshooting.md).
