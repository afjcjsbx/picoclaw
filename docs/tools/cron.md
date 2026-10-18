# Cron tool

The `cron` tool lets the agent schedule jobs. A job fires as a reminder, as a full agent turn, or — when it carries a `command` — as a shell command. Jobs are stored in the current workspace; see [Scheduled tasks and cron jobs](../reference/cron.md) for schedule types, time zone handling, execution modes, and storage.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.cron.enabled` | bool | `true` | Enable the agent-facing `cron` tool |
| `tools.cron.allow_command` | bool | `true` | Allow command jobs; when `false`, scheduling one still works if the call passes `command_confirm=true` |
| `tools.cron.command_allowed_remotes` | string[] | `[]` | Remote channels (or `channel:chat_id` values) allowed to schedule command jobs; `*` allows every remote channel |
| `tools.cron.exec_timeout_minutes` | int | `5` | Execution timeout in minutes for command jobs; `0` means no timeout |

Environment overrides use the `PICOCLAW_TOOLS_CRON_` prefix, for example `PICOCLAW_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10`.

Command jobs must also pass the exec guards: they are rejected when `tools.exec.enabled` is `false`, and scheduling them is restricted to internal channels (`cli`, `system`, `subagent`) or channels listed in `command_allowed_remotes`. Non-command reminders remain available on every channel.

## Tool arguments

| Argument | Type | Required for | Description |
|----------|------|--------------|-------------|
| `action` | string | every call | `add`, `list`, `get`, `update`, `remove`, `enable`, or `disable` |
| `message` | string | `add` | Reminder/task message shown when the job fires; with `command`, it describes what the command does |
| `job_id` | string | `get`, `update`, `remove`, `enable`, `disable` | Identifier of the target job |
| `name` | string | — | Optional job display name (used by `add` and `update`) |
| `at_seconds` | int | one-time | Fire once, this many seconds from now (`600` = 10 minutes); the job is removed after it runs |
| `every_seconds` | int | recurring | Recurring interval in seconds (`7200` = every 2 hours) |
| `cron_expr` | string | recurring | Cron expression for complex schedules (`0 9 * * *`, evaluated in the process time zone) |
| `command` | string | — | Shell command to execute instead of just showing the message (for example `df -h`) |
| `command_confirm` | bool | — | Explicit confirmation for scheduling or modifying a command job; required when `tools.cron.allow_command` is `false` |

Schedule fields are mutually exclusive and at least one is required for `add`; if several are supplied, priority is `at_seconds` > `every_seconds` > `cron_expr`. Use `get` before editing and `update` to change a job without losing its payload. On remote channels the tool can only list, get, update, run, remove, enable, and disable jobs bound to the current channel/chat. The Pico WebUI channel is single-owner — one shared token and the fixed `pico-user` sender — so its jobs are scoped to the whole channel: a job created in one WebUI conversation remains visible and manageable from every other conversation. Command jobs still require the channel to be allowed by `tools.cron.command_allowed_remotes`.

## Examples

Reminder in 10 minutes:

```json
{"action": "add", "message": "Check the oven", "at_seconds": 600}
```

Hourly command job (with confirmation when `allow_command` is off):

```json
{"action": "add", "message": "Report disk usage", "command": "df -h", "command_confirm": true, "every_seconds": 3600}
```

Reschedule a job while keeping its payload:

```json
{"action": "update", "job_id": "79095b2f5685a0f2", "cron_expr": "30 10 * * *"}
```

## Output

`list` shows each job with its schedule and, when known, the last and next run as ISO 8601 timestamps (RFC 3339) carrying the process time zone's UTC offset:

```text
- Daily report (id: 79095b2f5685a0f2, 0 9 * * *, last run: 2026-10-18T09:00:00+02:00 (ok), next run: 2026-10-19T09:00:00+02:00)
```

One-time jobs show `one-time at <time>`. `get` and `update` return the raw job JSON — including `state.nextRunAtMs` and `state.lastRunAtMs` as epoch milliseconds — plus a derived `localTimes` object with `lastRun` and `nextRun` in the same ISO 8601 format:

```json
{"id": "79095b2f5685a0f2", "localTimes": {"lastRun": "2026-10-18T09:00:00+02:00", "nextRun": "2026-10-19T09:00:00+02:00"}}
```

Rendered times are informational; scheduling and storage always use the raw epoch fields, and the agent does not need shell `date` conversions to report them.
