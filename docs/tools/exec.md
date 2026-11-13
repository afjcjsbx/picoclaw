---
description: "The exec tool: run shell commands with a timeout, working directory and environment, plus the PICOCLAW_TOOLS_EXEC_ environment variables that configure it."
---
# Exec tool

The exec tool is used to execute shell commands.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Enable the exec tool |
| `enable_deny_patterns` | bool | `true` | Enable the built-in dangerous-command blocking patterns |
| `custom_deny_patterns` | string[] | `[]` | Additional deny patterns (regular expressions); always enforced and they take priority over allow rules |
| `custom_allow_patterns` | string[] | `[]` | Allow patterns (regular expressions) that explicitly permit matching commands. They exempt the built-in default deny set, but custom deny patterns, the hardline floor, and other safety limits still win |
| `allow_remote` | bool | `false` | When `false`, exec only accepts commands that originate from internal channels (`cli`, `system`, `subagent`). Remote channels are then blocked (fail-closed): an empty channel is also rejected. Set it to `true` only when remote command execution is explicitly intended |
| `timeout_seconds` | int | `60` | Command timeout in seconds. `0` means the default (60s) |
| `max_output_chars` | int | `50000` | Foreground output cap. Longer output keeps the first 40% and the last 60% around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, while the full stream is spilled to a workspace artifact (up to 5,000,000 bytes). The inline view is also bounded by `tools.max_inline_result_chars`; the artifact preserves the full stream. `0` means the default (50000) |

Environment variables use the `PICOCLAW_TOOLS_EXEC_` prefix: `PICOCLAW_TOOLS_EXEC_ENABLED`, `PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_ALLOW_REMOTE`, `PICOCLAW_TOOLS_EXEC_CUSTOM_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS`, `PICOCLAW_TOOLS_EXEC_TIMEOUT_SECONDS`, `PICOCLAW_TOOLS_EXEC_MAX_OUTPUT_CHARS`.

## Tool arguments

The `exec` tool executes shell commands and manages **background sessions** (long-running commands) and **PTY sessions** (interactive programs). For a one-shot command only `command` is needed; the other arguments operate on sessions created with `background=true`.

## Foreground result format

A foreground `run` that starts a process returns a one-line metadata header to the model, followed by the raw stdout/stderr body:

```text
[exec] {"exitCode":0,"timedOut":false,"truncated":false,"durationMs":42}
hello
```

| Field | Description |
|-------|-------------|
| `exitCode` | Process exit status. `null` when the direct child did not return a numeric status: timeout, signal termination, or failure to start. A shell wrapper may turn a signal death into `128+N` instead, for example `137` (128+9, SIGKILL — often the OOM killer) or `143` (128+15, SIGTERM). A background session terminated with `kill` reports `-1` |
| `timedOut` | `true` when the configured timeout stopped the command |
| `truncated` | `true` when the output exceeded the configured cap; the omitted middle stays in the artifact |
| `durationMs` | Wall-clock duration of the command in milliseconds |
| `error` | Present when the command timed out or was terminated without a numeric exit status. Command stderr is merged into the body and is not repeated here |

The header stays outside the output body, so newlines, tabs and quotes are not JSON-escaped. The user-facing message is the raw output without the header; on failure it keeps the human-readable `[Command exited with code N]` suffix.

Non-zero exit codes are not automatically tool failures: `grep` returns `1` when there is no match and `diff` returns `1` when files differ. Interpret `exitCode` in context. The tool still marks a non-zero exit as an error result internally for UI and logging purposes.

Signal terminations are different from ordinary non-zero exits. `137` usually means SIGKILL, including the OOM killer, and an out-of-memory kill may leave no stderr of its own: check the system log (for example `dmesg -T | tail`) before retrying. `143` means SIGTERM. On Unix, when the direct child is killed by a signal the header reports `exitCode: null` and the human-readable suffix says `killed by signal`; the `kill` action reports `-1` for a session it terminated.

When output is truncated, the artifact path appears after the body as a `[file:...]` local artifact path, with the full stream stored up to `5,000,000` bytes. The message as a whole is therefore not one JSON document: only the first line is JSON metadata.

Background session actions return a JSON `ExecResponse` instead. Once the session has finished, `exitCode` is present and includes `0`; while it is still running, the field is omitted.

| Argument | Type | Required for | Description |
|----------|------|--------------|-------------|
| `action` | string | every call | `run` (execute a command), `list` (show sessions), `poll` (check a session's status; returns immediately), `wait` (block until a session exits or the timeout elapses), `read` (get a session's output), `write` (send input to a session's stdin), `kill` (terminate a session), `send-keys` (send keys to a PTY session) |
| `command` | string | `run` | Shell command to execute |
| `sessionId` | string | `poll`, `wait`, `read`, `write`, `kill`, `send-keys` | Session ID returned by a background `run` |
| `data` | string | `write` | Data to write to the session's stdin |
| `keys` | string | `send-keys` | Key names: `up`, `down`, `left`, `right`, `enter`, `tab`, `escape`, `backspace`, `ctrl-c`, `ctrl-d`, `home`, `end`, `pageup`, `pagedown`, `f1`–`f12` |
| `background` | bool | — | With `run`, start the command in the background immediately; the result includes a `sessionId` |
| `pty` | bool | — | Allocate a pseudo-terminal for interactive programs; not supported on Windows |
| `cwd` | string | — | Working directory for the command; defaults to the agent workspace |
| `timeout` | int | — | Timeout in seconds for foreground commands; overrides `tools.exec.timeout_seconds`, and `0` disables it. Cannot be combined with `background=true`. With `wait`: maximum seconds to block (a positive integer), defaulting to and capped by `tools.exec.timeout_seconds` |
| `notify` | bool | — | With `background=true`, send a completion message when the process exits (default `false`). Manually killed sessions do not notify. Use it for jobs with a defined end; do not poll or wait in a loop |
| `offset` | int | — | With `read`: absolute byte offset into the retained session log. By default the response starts at the most recent `limit` bytes; use `0` for the oldest retained byte |
| `limit` | int | — | With `read`: maximum bytes to return. Defaults to `tools.exec.max_output_chars` |

Behavior notes:

- Run one independent command per tool call and chain dependent commands with `&&` (`;` when later commands must run regardless of earlier failures). Do not separate commands with newlines.
- Prefer `cwd` over `cd` inside the command, and the filesystem tools over shell file operations.
- Foreground output is bounded while the command runs: the first 40% and the last 60% of `max_output_chars` (default 50,000, also capped by `tools.max_inline_result_chars`) stay in memory around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, and the full stream is spilled to a workspace artifact (up to 5,000,000 bytes) as soon as the cap is exceeded. The result includes the artifact path, so the omitted middle stays readable; `truncated` in the metadata header is `true`. Background sessions retain a rolling 1 MB window of output; earlier bytes are dropped and reported via `droppedBytes`.
- `poll` never blocks and returns a short output preview. `read` is non-destructive and pageable with `offset`/`limit`; the response includes `totalBytes`, `offset`, `nextOffset`, `hasMore`, `droppedBytes` (bytes evicted from the rolling window since the session started) and `skippedBytes` (bytes skipped because the requested offset predates the retained window). `wait` returns the exit status and output when the process exits, and a partial output tail when its window expires. `kill` returns the final output tail and keeps the session readable for the retention window.
- To wait for a background session, call `wait` once instead of polling in a loop. If the window elapses first, the result has status `running` and a note saying this is not an error; call `wait` again if the job is expected to take longer. After two consecutive `poll`/`wait` calls with no new output the tool enables a completion notification and tells the model to stop checking (use `wait`, read the output, or kill the session).
- Sessions are cleaned up 30 minutes after the process exits. `write` sends stdin, `send-keys` sends keys to a PTY session; combine `background=true` with `pty=true` for interactive sessions such as a REPL.

## Disabling the Exec Tool

To completely disable the `exec` tool, set `enabled` to `false`:

**Via config file:**
```json
{
  "tools": {
    "exec": {
      "enabled": false
    }
  }
}
```

**Via environment variable:**
```bash
PICOCLAW_TOOLS_EXEC_ENABLED=false
```

> **Note:** When disabled, the agent will not be able to execute shell commands. This also affects the Cron tool's ability to run scheduled shell commands.

## Functionality

- **`enable_deny_patterns`**: Set to `false` to disable the built-in default dangerous-command patterns. The hardline floor and `custom_deny_patterns` still apply.
- **`custom_deny_patterns`**: Add custom deny regex patterns. These are always enforced, even when `enable_deny_patterns` is `false`, and custom allow patterns cannot exempt them.

## Hardline Always-On Patterns

Some catastrophic commands are an immutable floor: they stay blocked even when `enable_deny_patterns` is `false`, and no custom allow pattern can exempt them.

- Fork bombs: `:(){ :|:& };:`
- Disk formatting/partitioning: `mkfs`, `mkfs.*`, `diskpart`, `format C:`
- Raw disk access: `dd if=`, writing to `/dev/sd*` and other block devices
- Recursive deletion of `/` such as `rm -rf /` or `rm -rf /*`
- Power state changes: `shutdown`, `reboot`, `poweroff`

## Default Blocked Command Patterns

These patterns are enabled by default and can be disabled with `enable_deny_patterns: false`:

- Delete commands: `rm -rf`, `del /f/q`, `rmdir /s`
- Disk operations: broad `format`/`mkfs`/`diskpart`/`dd if=` detection (the hardline subset above stays blocked)
- System operations: broad `shutdown`/`reboot`/`poweroff` detection (the hardline subset above stays blocked)
- Command substitution: `$()`, `${}`, backticks
- Pipe to shell: `| sh`, `| bash`
- Privilege escalation: `sudo`, `chmod`, `chown`
- Process control: `pkill`, `killall`, `kill -9`
- Remote operations: `curl | sh`, `wget | sh`, `ssh`
- Package management: `apt`, `yum`, `dnf`, `npm install -g`, `pip install --user`
- Containers: `docker run`, `docker exec`
- Git: `git push`, `git force`
- Other: `eval`, `source *.sh`

## Known Architectural Limitation

The exec guard only validates the top-level command sent to PicoClaw. It does **not** recursively inspect child
processes spawned by build tools or scripts after that command starts running.

Examples of workflows that can bypass the direct command guard once the initial command is allowed:

- `make run`
- `go run ./cmd/...`
- `cargo run`
- `npm run build`

This means the guard is useful for blocking obviously dangerous direct commands, but it is **not** a full sandbox for
unreviewed build pipelines. If your threat model includes untrusted code in the workspace, use stronger isolation such
as containers, VMs, or an approval flow around build-and-run commands.

## Configuration Example

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "custom_deny_patterns": [
        "\\brm\\s+-r\\b",
        "\\bkillall\\s+python"
      ]
    }
  }
}
```
