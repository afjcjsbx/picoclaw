---
title: "Is PicoClaw still maintained?"
description: "Maintenance status for PicoClaw: the upstream sipeed/picoclaw release and main-branch dates, this community fork's latest release, and how to migrate."
---

# Is PicoClaw still maintained?

**Short answer: yes. This repository is actively maintained.** It is a community continuation of [sipeed/picoclaw](https://github.com/sipeed/picoclaw), not an official Sipeed repository.

This page records verifiable dates so you can compare the upstream project and this fork without relying on rumours. Last checked: **2026-11-07**.

## Release and branch status

| Project | Latest release | Last commit on `main` |
| --- | --- | --- |
| [`sipeed/picoclaw`](https://github.com/sipeed/picoclaw) (upstream) | [`v0.3.1`](https://github.com/sipeed/picoclaw/releases/tag/v0.3.1) — 2026-07-03 | 2026-08-19 |
| [`afjcjsbx/picoclaw`](https://github.com/afjcjsbx/picoclaw) (this repository) | [`v0.5.3`](https://github.com/afjcjsbx/picoclaw/releases/tag/v0.5.3) — 2026-11-01 | 2026-11-07 |

Upstream still receives issue and pull-request activity. The table records the last commit on the upstream `main` branch and the latest published release; it is not a claim that the upstream project is archived or abandoned.

## What is maintained here

This fork publishes its own releases and continues development on top of the upstream codebase. The most visible fork-specific areas are:

- [Web UI and Launcher](../guides/webui.md): browser-based setup, chat, remote access, tray, and logs.
- [Agent Plugins](../guides/plugins.md): installable packages that bundle skills and MCP servers.
- [Seahorse memory](../architecture/seahorse-memory.md): long-term memory, compaction, and retrieval.
- [Spawn and async tasks](../guides/spawn-tasks.md): background work and sub-agent orchestration.
- [SubTurn](../architecture/subturn.md): subagent coordination, concurrency control, and lifecycle.
- [Scheduled tasks and cron](../reference/cron.md): schedule types, delivery modes, and command gates.
- [Hardware compatibility](../guides/hardware-compatibility.md): tested boards, including Raspberry Pi and RISC-V devices.

## Migrating from upstream

If you are currently running `sipeed/picoclaw` and want to switch, start with the [migration guide](../migration/from-sipeed-picoclaw.md). The short version is: back up `~/.picoclaw`, install the latest release from this repository, then verify the version, configuration, and gateway.

## How to verify these dates

- [Releases in this repository](https://github.com/afjcjsbx/picoclaw/releases)
- [Commits on this repository's `main` branch](https://github.com/afjcjsbx/picoclaw/commits/main)
- [Releases in the upstream repository](https://github.com/sipeed/picoclaw/releases)
- [Commits on the upstream `main` branch](https://github.com/sipeed/picoclaw/commits/main)

If this page becomes stale, please [open an issue](https://github.com/afjcjsbx/picoclaw/issues/new) with the current dates.
