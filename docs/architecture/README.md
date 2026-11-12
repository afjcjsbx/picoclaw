---
description: "PicoClaw internals: session storage, Seahorse memory, routing, runtime events, hooks, steering, sub-turns, and tool orchestration."
---
# Architecture

Internal architecture notes for major runtime mechanisms and subsystem design.

- [Steering](steering.md): injecting messages into a running agent loop between tool calls.
- [Parallel Tool Calls](parallel-tool-calls.md): planner and execution phases for running compatible tool calls of one response concurrently.
- [SubTurn Mechanism](subturn.md): sub-agent coordination, concurrency control, and lifecycle handling.
- [Session System](session-system.md): session scope allocation, JSONL persistence, alias compatibility, and migration. ([ZH](session-system.zh.md))
- [Seahorse Memory](seahorse-memory.md): SQLite-backed context manager, bootstrap, compaction, retrieval tools, configuration, pruning, and known gaps.
- [Routing System](routing-system.md): agent dispatch, session policy selection, and light/heavy model routing. ([ZH](routing-system.zh.md))
- [Runtime Events](runtime-events.md): runtime event envelope, centralized event logging, filters, and examples. ([ZH](runtime-events.zh.md))
- [Agent Self-Evolution](agent-self-evolution.md): the optional out-of-process evolution plugin, draft generation, and state layout.
- [Hook System Guide](hooks/): current hook architecture and protocol details.
- [Agent Refactor](agent-refactor/): notes and checkpoints for the agent refactor work.

For proposal-style or exploratory docs, also see [`../design/`](../design/).
