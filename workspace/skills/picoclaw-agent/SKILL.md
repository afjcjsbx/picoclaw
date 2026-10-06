---
name: picoclaw-agent
description: "Configure, extend, debug, or contribute to PicoClaw itself. Use for PicoClaw CLI, configuration, providers, channels, tools, MCP, agent runtime, skills, sessions, routing, cron, or repository internals. Follow the repository documentation and source of truth."
metadata: {"nanobot":{"emoji":"🦞"}}
---

# PicoClaw Agent

Use this skill for work on PicoClaw or this repository.

## Working rules

- Treat the [repository README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md), [documentation index](https://github.com/afjcjsbx/picoclaw/blob/main/docs/README.md), and current source as the source of truth.
- Do not restate or copy documentation into answers, prompts, or new docs. Read the relevant page and link to it.
- For code changes, trace the existing implementation and callers before editing. If docs do not cover a behavior, inspect the relevant package and say so instead of guessing.
- Prefer links below; for channel-specific behavior, follow the channel guide to `docs/channels/<name>/README.md`.

## Documentation map

| Topic | Canonical reference |
| --- | --- |
| Getting started, CLI, project overview | [README](https://github.com/afjcjsbx/picoclaw/blob/main/README.md), [Docker and quick start](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/docker.md) |
| Configuration, environment, workspace, routing, sandbox | [Configuration guide](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/configuration.md) |
| Secrets and credential storage | [Security configuration](https://github.com/afjcjsbx/picoclaw/blob/main/docs/security/security_configuration.md), [credential encryption](https://github.com/afjcjsbx/picoclaw/blob/main/docs/security/credential_encryption.md) |
| Config migrations and schema changes | [Config versioning](https://github.com/afjcjsbx/picoclaw/blob/main/docs/reference/config-versioning.md), [model-list migration](https://github.com/afjcjsbx/picoclaw/blob/main/docs/migration/model-list-migration.md) |
| Providers, model list, model routing, voice models | [Providers and models](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/providers.md), [Antigravity setup](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/ANTIGRAVITY_USAGE.md), [rate limiting](https://github.com/afjcjsbx/picoclaw/blob/main/docs/reference/rate-limiting.md) |
| Channels, gateway, platform setup | [Chat apps guide](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/chat-apps.md); channel-specific docs live under [`docs/channels/`](https://github.com/afjcjsbx/picoclaw/tree/main/docs/channels) |
| Tools, execution policies, skills, MCP settings | [Tools configuration](https://github.com/afjcjsbx/picoclaw/blob/main/docs/reference/tools_configuration.md), [image generation](https://github.com/afjcjsbx/picoclaw/blob/main/docs/tools/image-generation.md) |
| MCP transports, discovery, server configuration and CLI | [Tools configuration — MCP](https://github.com/afjcjsbx/picoclaw/blob/main/docs/reference/tools_configuration.md#mcp-tool), [MCP CLI](https://github.com/afjcjsbx/picoclaw/blob/main/docs/reference/mcp-cli.md) |
| Agent loop and runtime internals | [Architecture index](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/README.md), [agent refactor notes](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/agent-refactor/README.md), [runtime events](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/runtime-events.md), [steering](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/steering.md) |
| Sessions, context isolation, routing | [Session guide](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/session-guide.md), [session system](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/session-system.md), [routing system](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/routing-system.md) |
| Subagents, spawn, background tasks | [Spawn and async tasks](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/spawn-tasks.md), [SubTurn architecture](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/subturn.md) |
| Self-evolution | [Configuration guide](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/configuration.md), [self-evolution architecture](https://github.com/afjcjsbx/picoclaw/blob/main/docs/architecture/agent-self-evolution.md) |
| Cron and scheduled jobs | [Cron reference](https://github.com/afjcjsbx/picoclaw/blob/main/docs/reference/cron.md) |
| Build tags, supported hardware | [Custom builds](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/custom-builds.md), [hardware compatibility](https://github.com/afjcjsbx/picoclaw/blob/main/docs/guides/hardware-compatibility.md) |
| Logs, debugging, troubleshooting | [Debugging](https://github.com/afjcjsbx/picoclaw/blob/main/docs/operations/debug.md), [troubleshooting](https://github.com/afjcjsbx/picoclaw/blob/main/docs/operations/troubleshooting.md) |
| Contributing and development workflow | [CONTRIBUTING](https://github.com/afjcjsbx/picoclaw/blob/main/CONTRIBUTING.md) |

