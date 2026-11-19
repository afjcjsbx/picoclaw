---
description: "PicoClaw documentation home for the community-maintained fork: install the ultra-lightweight Go AI assistant, add model providers, connect chat apps, and run it on any Linux board."
---
# PicoClaw Documentation

PicoClaw is an ultra-lightweight personal AI assistant written in Go. It runs on $10 hardware with 10–20 MB RAM, boots in under a second, and ships as a single binary across RISC-V, ARM, MIPS, LoongArch, and x86.

This is the documentation home for the community-maintained fork of PicoClaw. See [maintenance status](project/maintenance-status.md) for upstream and fork release dates. For the project overview, feature comparison, releases, and install commands, see the [repository README](https://github.com/afjcjsbx/picoclaw#readme).

## New to PicoClaw?

1. **Install** — follow the [repository README](https://github.com/afjcjsbx/picoclaw#-install) (installer, binary download, or build from source) or the [Docker & Quick Start Guide](guides/docker.md).
2. **Open the Web UI** — the [Web UI & Launcher Guide](guides/webui.md) covers configuration and chat in the browser, with no command-line knowledge required.
3. **Add a model provider** — see [Providers & Model Configuration](guides/providers.md).
4. **Connect a chat app** — start with [Chat Apps Configuration](guides/chat-apps.md), then follow the per-channel guides under `channels/<name>/README.md` (for example [Telegram](channels/telegram/)).
5. **Look something up** — browse the [Reference](reference/) and the [FAQ](guides/faq.md).

## Documentation Map

| Section | What it covers |
| --- | --- |
| [Guides](guides/) | Setup, configuration, providers, chat apps, sessions, builds, hardware |
| [Reference](reference/) | CLI, tools, MCP, cron, gateway API, config versioning, rate limiting |
| [Operations](operations/) | Troubleshooting and debugging |
| [Security](security/) | Hardening, credentials, sensitive data, provider auth |
| [Architecture](architecture/) | Runtime internals: sessions, Seahorse memory, routing, events, hooks, steering, sub-turns |
| [Channels](guides/chat-apps.md) | Per-platform chat setup guides (`channels/<name>/README.md`) |
| [Tools](tools/) | Documentation for every tool family, with switches and parameters |
| [Design Notes](design/) | Proposals and investigations |
| [Migration](migration/) | Breaking changes and upgrade notes |
| [Maintenance status](project/maintenance-status.md) | Upstream vs fork release dates and migration path |
| [Migrating from sipeed/picoclaw](migration/from-sipeed-picoclaw.md) | Back up, install, and verify the fork |
| [Project Translations](https://github.com/afjcjsbx/picoclaw/tree/main/docs/project) | Chinese translations of the repository README and contributing guide |

## Popular Pages

- [Configuration Guide](guides/configuration.md)
- [Tools Configuration](reference/tools_configuration.md)
- [CLI Reference](reference/cli.md)
- [Gateway HTTP API](reference/gateway-api.md)
- [Scheduled Tasks and Cron Jobs](reference/cron.md)
- [FAQ](guides/faq.md)
- [Troubleshooting](operations/troubleshooting.md)
- [Maintenance status](project/maintenance-status.md)

## Contributing to the Docs

See [Documentation conventions](https://github.com/afjcjsbx/picoclaw/blob/main/docs/README.md) for the recommended layout, naming rules for translations, and what `make lint-docs` checks.
