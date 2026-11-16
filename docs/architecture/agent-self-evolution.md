---
description: "The optional evolution plugin lets PicoClaw learn from completed turns without adding cost to the core binary."
---
# Evolution Plugin

Self-evolution is an optional, out-of-process PicoClaw plugin. It is **not**
compiled into the default `picoclaw` binary and is disabled until the plugin is
installed, enabled, and granted `allow_hooks: true`.

The plugin lives in [`plugins/evolution`](../../plugins/evolution/README.md).

## Flow

PicoClaw emits `agent.turn.end` runtime events. The plugin's process hook reads
those events, records a slim learning case, and updates skill profiles. Raw tool
executions, attempt-trail snapshots, and tool kinds are used only to derive the
used skill names and are not persisted.

The cold path groups related task records, checks the configured success
threshold, and prepares skill drafts. Drafts can target new skills or
append/replace/merge existing workspace skills. The apply path validates
generated `SKILL.md` content before writing; invalid drafts are rejected before
a skill directory or file is created.

## Modes

| Mode | Behavior |
|------|----------|
| `observe` | Record learning data only. No cold-path draft generation runs automatically. |
| `draft` | Record learning data and generate candidate skill drafts when the cold path runs. |
| `apply` | Generate drafts and allow accepted drafts to update workspace skills. |

When `enabled` is false inside the plugin, `mode` is treated as disabled.

## Cold Path Trigger

`cold_path_trigger` only matters in `draft` and `apply` modes.

| Trigger | Behavior |
|---------|----------|
| `after_turn` | Run the cold path after eligible turns. |
| `scheduled` | Run the cold path at configured `cold_path_times` using one timer, not a poll loop. |
| `manual` | Do not run automatically. Use `evolution-hook --cold-path <workspace>` for a one-shot run. |

`cold_path_times` uses `HH:MM` strings and is ignored unless the trigger is
`scheduled`.

## State

State is stored under the plugin data directory (`PLUGIN_DATA/state/<workspace
hash>`), not inside the agent workspace. This keeps learning records, patterns,
drafts, and skill profiles out of the writable prompt-injection surface. The
plugin accepts `state_dir` as an explicit override.

## Resource Profile

- **Disabled (default):** the core binary contains no evolution code, config
  block, goroutine, or timer.
- **Enabled:** one child process with a single worker goroutine and a bounded
  32-turn queue. Event handling is a JSON decode plus an enqueue. Cold-path runs
  are one goroutine per workspace.
- **LLM calls:** only the cold path calls the optional OpenAI-compatible
  endpoint. `observe` mode makes no LLM calls.

## Safety Considerations

Evolution creates a persistent feedback loop: user input can become a task
record, task records can be clustered into an LLM-generated draft, and an
accepted draft can become `SKILL.md` content that is loaded into future agent
prompts. Treat generated skill content as prompt-sensitive material, especially
in `apply` mode.

The local scanner is a narrow guardrail, not a complete safety boundary. It
rejects structurally invalid drafts and a small set of obvious secret-like
substrings, but it does not reliably detect prompt injection, unsafe
instructions, or every form of sensitive data. Use `observe` or `draft` when
human review is required before skill changes reach disk.

In `apply` mode, accepted drafts can update workspace skills automatically.
Existing skills are backed up before replacement, but recovery is manual: an
operator must restore the desired backup if an applied skill should be rolled
back.

For user-facing configuration fields, see the
[Configuration Guide](../guides/configuration.md#agent-self-evolution-plugin)
and the [plugin README](../../plugins/evolution/README.md).
