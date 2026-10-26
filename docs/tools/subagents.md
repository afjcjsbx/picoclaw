# Subagent tools

Subagent tools let an agent hand work to isolated sub-turns or to other named agents. Sub-turns run with their own ephemeral session history, so their intermediate steps never pollute the parent conversation, and they share the same workspace sandbox and path policies as the parent.

| Tool | Style | What it does | Switch (default) |
|------|-------|--------------|------------------|
| `spawn` | async | Start a subagent in the background; returns immediately | `tools.spawn.enabled` (`true`) |
| `subagent` | sync | Run a sub-task and wait for the result | `tools.subagent.enabled` (`true`) |
| `spawn_status` | inspect | List or inspect spawned tasks for the conversation | `tools.spawn_status.enabled` (`false`) |
| `delegate` | sync | Hand a task to a named peer agent and wait | always registered (multi-agent setups) |

For the workflow perspective (heartbeat integration, background reporting), see [Spawn & Async Tasks](../guides/spawn-tasks.md). For execution internals, see the [Sub-turn architecture](../architecture/subturn.md) notes.

## Registration rules

The switches interact, and some tools depend on others:

- `spawn` is registered when `tools.spawn.enabled` **and** `tools.subagent.enabled` are true.
- `spawn_status` is registered when `tools.spawn_status.enabled` **and** `tools.subagent.enabled` are true.
- The synchronous `subagent` tool is registered whenever the subagent tooling is active, that is when `tools.subagent.enabled` is true and at least one of `spawn` or `spawn_status` is enabled. It has no dedicated sub-switch beyond that.
- If `spawn` or `spawn_status` is enabled while `tools.subagent.enabled` is `false`, PicoClaw logs `spawn/spawn_status tools require subagent to be enabled` and registers neither.
- `delegate` has no config switch: it is registered automatically for every agent whenever more than one agent exists in the configuration.

## `spawn`

Starts a subagent for a task in the background. The subagent completes the task independently and reports back; it never blocks the current tool call. Several `spawn` calls in one step run in parallel, and each result is tagged with `[Result from subagent "<label>" (agent <id>)]`.

When `spawn` is called from a sub-turn (for example a peer agent that was itself spawned or delegated to), that sub-turn waits for its spawned children before it finishes, and their results are handed back to its model. Root turns do not wait: results arrive as a follow-up message.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `task` | string | yes | The task for the subagent to complete |
| `label` | string | no | Short label for display in status output |
| `agent_id` | string | no | Target a specific peer agent instead of a generic child turn |

## `subagent`

Executes a subagent task synchronously and returns the result. Use it for bounded work that the parent needs before continuing.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `task` | string | yes | The task for the subagent to complete |
| `label` | string | no | Short label for display |

## `spawn_status`

Inspects spawned tasks. Without arguments it lists the tasks visible to the current conversation; with an ID it returns the details of one task (for example `subagent-1`).

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `task_id` | string | no | Task ID to inspect (for example `subagent-1`); omit to list visible tasks |

## `delegate`

Delegates a task to another agent by ID and waits for the result. The task runs as the target peer agent, with that peer's identity, model, workspace, and tools — unlike `spawn`/`subagent`, which run a generic child turn.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `agent_id` | string | yes | ID of the target agent |
| `task` | string | yes | Clear description of the task to delegate |

Delegation and targeted spawning are restricted by the caller's subagent allowlist: an agent can only target peers listed in its `agents.list[].subagents.allow_agents` configuration. When the subagents block is absent or `allow_agents` is unset, no peer can be targeted; `["*"]` allows all peers.

## Agent configuration

Which peers are visible and spawnable is controlled by the multi-agent configuration:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `agents.list[].subagents.allow_agents` | string[] | unset (none) | Peer agent IDs this agent may spawn or delegate to; `["*"]` allows all peers |
| `agents.list[].subagents.model` | object | — | Optional model override (`primary` / `fallbacks`) when this agent runs as a subagent |

Each peer's identity comes from its `AGENT.md` frontmatter (`name`, `description`, `tools`, `skills`, and optional model overrides). When an agent has `spawn` and at least one allowed peer, PicoClaw injects the eligible peers into the system prompt automatically, so `spawn(..., agent_id="research")` and `delegate` work without a separate discovery call.

## Runtime limits

Sub-turn execution is bounded by the global SubTurn settings under `agents.defaults.subturn`:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `max_depth` | int | `3` | Maximum nested sub-turn depth |
| `max_concurrent` | int | `3` | Concurrent sub-turns per parent turn; values above the hard cap of `32` are clamped |
| `concurrency_timeout_sec` | int | `30` | How long a sub-turn waits for a concurrency slot before failing |
| `default_timeout_minutes` | int | `5` | Default per-sub-turn timeout |
| `default_token_budget` | int | `0` (none) | Token budget applied to a sub-turn when greater than zero |

Environment variables use the `PICOCLAW_AGENTS_DEFAULTS_SUBTURN_` prefix (for example `PICOCLAW_AGENTS_DEFAULTS_SUBTURN_MAX_CONCURRENT`).

Operational behavior that matters:

- Subagents share the parent's workspace security boundary; they do not bypass sandbox or path restrictions.
- `spawn_status` is scoped to the current conversation when channel/chat context exists.
- Spawned background tasks are marked critical so they survive graceful parent completion; hard aborts (including `/stop` on a root turn) cascade to child and grandchild sub-turns.
- In chat channels, `/subagents` shows the active subagent tree for the current session.
