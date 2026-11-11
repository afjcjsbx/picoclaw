---
title: "Spawn & Async Tasks"
description: "PicoClaw supports asynchronous task execution via the spawn tool. This is primarily used by the Heartbeat system to run long-running tasks without blocking."
---
# 🔄 Spawn & Async Tasks

> Back to [Guides](./)

PicoClaw supports **asynchronous task execution** via the `spawn` tool. This is primarily used by the **Heartbeat** system to run long-running tasks without blocking the main agent loop.

## Heartbeat

The heartbeat system periodically checks `workspace/HEARTBEAT.md` for scheduled tasks. On first run, a default template is auto-generated. You can customize it to define quick tasks (handled inline) and long tasks (delegated via `spawn`). Quick answers are sent to the last active channel; if nothing needs attention the agent replies `HEARTBEAT_OK` and no message is delivered.

Tasks are read from everything after the `Add your heartbeat tasks below this line:` marker; if the marker is absent the entire file is treated as tasks.

**Example `HEARTBEAT.md`:**

```markdown
## Quick Tasks (respond directly)

- Report current time

## Long Tasks (use spawn for async)

- Search the web for AI news and summarize
- Check email and report important messages
```

**Key behaviors:**

| Feature                 | Description                                                    |
| ----------------------- | -------------------------------------------------------------- |
| **spawn**               | Creates async subagent, doesn't block heartbeat                |
| **Independent context** | Subagent has its own context, no session history               |
| **Outbound tools**      | By default the subagent can message the user directly          |
| **Non-blocking**        | After spawning, heartbeat continues to next task               |
| **Result delivery**     | The result always comes back to the parent turn when it finishes |

#### How Subagent Communication Works

```
Heartbeat triggers
    ↓
Agent reads HEARTBEAT.md
    ↓
For long task: spawn subagent
    ↓                           ↓
Continue to next task      Subagent works independently
    ↓                           ↓
All tasks done            Result returns to the parent turn
    ↓                           ↓
Respond HEARTBEAT_OK      Parent forwards it, or the subagent
                          sends it directly (outbound enabled)
```

By default a spawned subagent inherits the parent's full tool set, including the
outbound tools (`message`, `send_file`, `send_tts`, `image_generate` and
`reaction`), so it can contact the user without going through the main agent.

Two switches control that:

- `allow_outbound` on a `spawn` call applies to that task only.
- `agents.defaults.subturn.direct_message` sets the default for every sub-turn
  (`true` unless configured otherwise).

The per-call switch can only tighten the global setting: `allow_outbound: false`
always disables outbound for that task, while `true` (or omitting it) follows the
configured default and never lifts a global `direct_message: false`.

The restriction is inherited: a subagent started with outbound disabled cannot
re-enable it for the subagents it spawns, including ones that target another
agent with `agent_id`.

When outbound is disabled, the subagent runs without the whole outbound group;
the remaining tools (files, web, exec, ...) stay available, and the final result
is returned to the parent turn, which decides whether to forward it. The channel
and chat context are still available for tools that only use them for scoping,
such as `spawn_status`.

```json
{
  "agent_id": "researcher",
  "task": "Summarize the latest reports",
  "allow_outbound": false
}
```

```json
{
  "agents": {
    "defaults": {
      "subturn": {
        "direct_message": false
      }
    }
  }
}
```

Set `PICOCLAW_AGENTS_DEFAULTS_SUBTURN_DIRECT_MESSAGE=false` to change the global
default through the environment.

**Configuration:**

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

| Option     | Default | Description                        |
| ---------- | ------- | ---------------------------------- |
| `enabled`  | `true`  | Enable/disable heartbeat           |
| `interval` | `30`    | Check interval in minutes (min: 5) |

**Environment variables:**

* `PICOCLAW_HEARTBEAT_ENABLED=false` to disable
* `PICOCLAW_HEARTBEAT_INTERVAL=60` to change interval
