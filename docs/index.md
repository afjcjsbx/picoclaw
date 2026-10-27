# PicoClaw Documentation

PicoClaw is an ultra-lightweight personal AI assistant written in Go. It runs on $10 hardware with 10–20 MB RAM, boots in under a second, and ships as a single binary across RISC-V, ARM, MIPS, LoongArch, and x86.

This is the documentation home. For the project overview, feature comparison, releases, and install commands, see the [repository README](https://github.com/afjcjsbx/picoclaw#readme).

## New to PicoClaw?

1. **Install** — follow the [repository README](https://github.com/afjcjsbx/picoclaw#-install) (installer, binary download, or build from source) or the [Docker & Quick Start Guide](guides/docker.md).
2. **Open the Web UI** — the [Web UI & Launcher Guide](guides/webui.md) covers configuration and chat in the browser, with no command-line knowledge required.
3. **Add a model provider** — see [Providers & Model Configuration](guides/providers.md).
4. **Connect a chat app** — start with [Chat Apps Configuration](guides/chat-apps.md), then follow the per-channel guides under `channels/<name>/README.md` (for example [Telegram](channels/telegram/README.md)).
5. **Look something up** — browse the [Reference](reference/README.md) and the [FAQ](guides/faq.md).

## Documentation Map

| Section | What it covers |
| --- | --- |
| [Guides](guides/README.md) | Setup, configuration, providers, chat apps, sessions, builds, hardware |
| [Reference](reference/README.md) | CLI, tools, MCP, cron, gateway API, config versioning, rate limiting |
| [Operations](operations/README.md) | Troubleshooting and debugging |
| [Security](security/README.md) | Hardening, credentials, sensitive data, provider auth |
| [Architecture](architecture/README.md) | Runtime internals: sessions, Seahorse memory, routing, events, hooks, steering, sub-turns |
| [Channels](guides/chat-apps.md) | Per-platform chat setup guides (`channels/<name>/README.md`) |
| [Tools](tools/README.md) | Documentation for every tool family, with switches and parameters |
| [Design Notes](design/README.md) | Proposals and investigations |
| [Migration](migration/README.md) | Breaking changes and upgrade notes |
| [Project Translations](https://github.com/afjcjsbx/picoclaw/tree/main/docs/project) | Translated versions of the repository README and contributing guide |

## Popular Pages

- [Configuration Guide](guides/configuration.md)
- [Tools Configuration](reference/tools_configuration.md)
- [CLI Reference](reference/cli.md)
- [Gateway HTTP API](reference/gateway-api.md)
- [Scheduled Tasks and Cron Jobs](reference/cron.md)
- [FAQ](guides/faq.md)
- [Troubleshooting](operations/troubleshooting.md)

## Contributing to the Docs

See [Documentation conventions](README.md) for the recommended layout, naming rules for translations, and what `make lint-docs` checks.
