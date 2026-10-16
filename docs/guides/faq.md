# FAQ

Short answers to questions that come up often. When an answer points to a full document, start there for details.

## Requirements and installation

**What hardware do I need?**
Any Linux board from ~$10 upward, plus macOS, Windows, and Android. Recent builds use 10–20 MB of RAM and boot in under a second even on 0.6 GHz single-core CPUs. See the [Hardware Compatibility List](hardware-compatibility.md).

**How do I install PicoClaw?**
Use the one-line installer (Linux/macOS/PowerShell), download the binaries from [GitHub Releases](https://github.com/afjcjsbx/picoclaw/releases), or read the [Docker & Quick Start Guide](docker.md). The installer sets up both `picoclaw` and `picoclaw-launcher`.

**What is the difference between `picoclaw` and `picoclaw-launcher`?**
`picoclaw` is the main binary and CLI ([CLI Reference](../reference/cli.md)). `picoclaw-launcher` is the browser-based dashboard that manages the Gateway for you — see the [Web UI & Launcher Guide](webui.md).

**macOS says the launcher cannot be opened — what do I do?**
Approve it once in System Settings → Privacy & Security → **Open Anyway**; the [repository README](https://github.com/afjcjsbx/picoclaw#readme) walks through it with screenshots.

**Can I switch from OpenClaw?**
Yes: `picoclaw migrate` imports config and workspace files (`--dry-run` first). See the [CLI Reference](../reference/cli.md#migrate).

## Configuration

**Where is my configuration stored?**
`~/.picoclaw/config.json`, with the workspace at `~/.picoclaw/workspace`. Override locations with `PICOCLAW_CONFIG` and `PICOCLAW_HOME`. See the [Configuration Guide](configuration.md).

**Where do API keys go?**
In `~/.picoclaw/.security.yml` (set `chmod 600`), referenced from `config.json`, so the main config stays shareable — see the Security Configuration section in the [Configuration Guide](configuration.md) and [Credential Encryption](../security/credential_encryption.md).

**How do I keep secrets out of what the model sees?**
Sensitive data filtering strips secrets from tool output before it reaches the model — see [Sensitive Data Filtering](../security/sensitive_data_filtering.md).

**How do I change the default model?**
```bash
picoclaw model              # show the current default
picoclaw model gpt-5.2      # set a new default
```
Or use the `/models` page in the [Web UI](webui.md). Provider setup is in [Providers & Model Configuration](providers.md).

**How do I add a model from a custom endpoint?**
`picoclaw model add -b <api-base> -k <api-key>` — it lists the endpoint's models and sets your pick as default. Details in the [CLI Reference](../reference/cli.md#model-add).

## Running

**Which ports does PicoClaw use?**
The Gateway listens on `127.0.0.1:18790` (`gateway.host`/`gateway.port`) and the launcher dashboard on `127.0.0.1:18800` (`-port`). Webhook channels share the Gateway port — see the [Gateway HTTP API](../reference/gateway-api.md).

**How do I see logs?**
Gateway logs are written under your PicoClaw home (`logs/gateway.log`, with rotation configured by `gateway.log_max_size_mb`/`log_max_backups`), or read them from the `/logs` page in the Web UI. Use `picoclaw gateway --debug` for verbose output — see [Debugging](../operations/debug.md).

**How do I get verbose debugging output?**
```bash
picoclaw gateway --debug            # or -d
picoclaw gateway --debug --no-truncate
```

**How do I connect Telegram, Discord, WhatsApp, or another chat app?**
Start with [Chat Apps Configuration](chat-apps.md); per-channel guides live under `docs/channels/<name>/README.md` (for example [Telegram](../channels/telegram/README.md)).

**How do I run PicoClaw at boot?**
Run it with Docker, or enable **launch at login** in the launcher config page (macOS, Linux, Windows). For the raw Gateway without the launcher, run it under your init system, for example a systemd service.

**How do I update?**
`picoclaw update` (checks GitHub releases), re-run the installer script, or `docker compose pull` for Docker installs.

## Features

**What is the difference between cron jobs and the heartbeat?**
Cron jobs are scheduled tasks that deliver messages to a channel ([Scheduled Tasks and Cron Jobs](../reference/cron.md)). The heartbeat periodically checks `workspace/HEARTBEAT.md` inside your workspace and only messages you when something needs attention ([Spawn & Async Tasks](spawn-tasks.md)).

**What is the difference between skills, plugins, and MCP?**
Skills are `SKILL.md` folders with instructions and resources ([authoring guide](skills-authoring.md)). Agent Plugins are installable packages that can bundle skills plus MCP servers ([Plugin Installation](plugins.md)). MCP connects external tool servers ([Tools Configuration](../reference/tools_configuration.md)).

**Where are sessions and memory stored?**
Session history is stored per session as JSONL files in the workspace, with summaries and scoping rules described in the [Session Guide](session-guide.md) and [Session System](../architecture/session-system.md).

**Does PicoClaw support voice or images?**
Yes: audio transcription uses `voice.model_name` with a multimodal provider (Groq Whisper as fallback — see [Providers](providers.md)), and image generation uses the `image_generate` tool ([Image Generation](../tools/image-generation.md)).

**Can I make custom builds without all channels?**
Yes — build tags select channels, and optional features like Seahorse or SQLite can be excluded. See [Custom Builds](custom-builds.md) and measure with the sizes table there.

## Troubleshooting

**The log says "model not found in model_list" — why?**
The model alias you referenced is not defined in `model_list`, or the alias/provider value is wrong. Check [Troubleshooting](../operations/troubleshooting.md) for this and other common errors.

**The Web UI cannot start the Gateway.**
The launcher only auto-starts the Gateway when a usable default model is configured. Add credentials/models first, or use **Start Gateway** manually — see [Web UI & Launcher Guide](webui.md).

**Where do I report bugs or ask questions?**
Join the [Discord community](https://discord.gg/bEuEDnKpf) or open an issue on [GitHub](https://github.com/afjcjsbx/picoclaw/issues).
