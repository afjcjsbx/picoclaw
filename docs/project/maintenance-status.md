---
title: "Is PicoClaw still maintained?"
description: "Maintenance status for PicoClaw: the upstream sipeed/picoclaw release and main-branch activity, this community fork's latest release, and how to migrate."
---

# Is PicoClaw still maintained?

**Short answer: yes. This repository is actively maintained.** It is a community continuation of [sipeed/picoclaw](https://github.com/sipeed/picoclaw), not an official Sipeed repository.

This page compares the upstream project and this fork using live badges, so there are no dates to update by hand.

## Release and branch status

| Project | Latest release | Last commit on `main` |
| --- | --- | --- |
| [`sipeed/picoclaw`](https://github.com/sipeed/picoclaw) (upstream) | [![upstream release](https://img.shields.io/github/v/release/sipeed/picoclaw?label=release&color=blue)](https://github.com/sipeed/picoclaw/releases) | [![upstream last commit](https://img.shields.io/github/last-commit/sipeed/picoclaw/main?label=last%20commit)](https://github.com/sipeed/picoclaw/commits/main) |
| [`afjcjsbx/picoclaw`](https://github.com/afjcjsbx/picoclaw) (this repository) | [![fork release](https://img.shields.io/github/v/release/afjcjsbx/picoclaw?label=release&color=green)](https://github.com/afjcjsbx/picoclaw/releases) | [![fork last commit](https://img.shields.io/github/last-commit/afjcjsbx/picoclaw/main?label=last%20commit)](https://github.com/afjcjsbx/picoclaw/commits/main) |

The badges are rendered by shields.io from GitHub. Upstream still receives issue and pull-request activity; the badges describe release and `main`-branch activity, not whether the upstream project is archived or abandoned.

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

## How to verify these values

- [Releases in this repository](https://github.com/afjcjsbx/picoclaw/releases)
- [Commits on this repository's `main` branch](https://github.com/afjcjsbx/picoclaw/commits/main)
- [Releases in the upstream repository](https://github.com/sipeed/picoclaw/releases)
- [Commits on the upstream `main` branch](https://github.com/sipeed/picoclaw/commits/main)

If a badge looks wrong, please [open an issue](https://github.com/afjcjsbx/picoclaw/issues/new) with the URL and the value GitHub shows.
