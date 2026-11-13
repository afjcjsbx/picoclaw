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
| `timeout_seconds` | int | `180` | Command timeout in seconds. `0` means the default (180s). Foreground runs are capped at `600`s; a larger configured timeout promotes commands to a tracked background session |
| `max_output_chars` | int | `50000` | Foreground output cap. Longer output keeps the first 40% and the last 60% around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, while the full stream is spilled to a workspace artifact (up to 5,000,000 bytes). The inline view is also bounded by `tools.max_inline_result_chars`; the artifact preserves the full stream. `0` means the default (50000) |
| `env_set` | object | `{}` | Variables always injected into every exec child process. Values are trusted configuration: they override inherited variables, including `PATH`, and are not subject to the LLM blocklist. Every listed value reaches every command, so only add secrets here when the commands genuinely need them |
| `env_allowlist` | string[] | `[]` | Additional parent variable names inherited on top of the built-in allowlist. Use explicit names, never wildcards: allowlisting a secret passes it to every command |

Environment variables use the `PICOCLAW_TOOLS_EXEC_` prefix: `PICOCLAW_TOOLS_EXEC_ENABLED`, `PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_ALLOW_REMOTE`, `PICOCLAW_TOOLS_EXEC_CUSTOM_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS`, `PICOCLAW_TOOLS_EXEC_TIMEOUT_SECONDS`, `PICOCLAW_TOOLS_EXEC_MAX_OUTPUT_CHARS`, `PICOCLAW_TOOLS_EXEC_ENV_ALLOWLIST` (comma-separated). `env_set` is JSON-only because values may contain characters used as env separators.

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
| `exitCode` | Process exit status. `null` when the process did not return a numeric status: timeout, signal termination, or failure to start |
| `timedOut` | `true` when the configured timeout stopped the command |
| `truncated` | `true` when the output exceeded the configured cap; the omitted middle stays in the artifact |
| `durationMs` | Wall-clock duration of the command in milliseconds |
| `error` | Present when the command timed out or was terminated without a numeric exit status. Command stderr is merged into the body and is not repeated here |

The header stays outside the output body, so newlines, tabs and quotes are not JSON-escaped. The user-facing message is the raw output without the header; on failure it keeps the human-readable `[Command exited with code N]` suffix.

Non-zero exit codes are not automatically tool failures: `grep` returns `1` when there is no match and `diff` returns `1` when files differ. Interpret `exitCode` in context. The tool still marks a non-zero exit as an error result internally for UI and logging purposes.

When output is truncated, the artifact path appears after the body as a `[file:...]` local artifact path, with the full stream stored up to `5,000,000` bytes. The message as a whole is therefore not one JSON document: only the first line is JSON metadata.

Background session actions return a JSON `ExecResponse` instead. Once the session has finished, `exitCode` is present and includes `0`; while it is still running, the field is omitted.

| Argument | Type | Required for | Description |
|----------|------|--------------|-------------|
| `action` | string | every call | `run` (execute a command), `list` (show sessions), `poll` (check a session's status; returns immediately), `wait` (block until a session exits or the timeout elapses), `read` (get a session's output), `write` (send input to a session's stdin), `kill` (terminate a session), `send-keys` (send keys to a PTY session) |
| `command` | string | `run` | Shell command to execute |
| `sessionId` | string | `poll`, `wait`, `read`, `write`, `kill`, `send-keys` | Session ID returned by a background `run` |
| `data` | string | `write` | Data to write to the session's stdin |
| `keys` | string | `send-keys` | Key names: `up`, `down`, `left`, `right`, `enter`, `tab`, `escape`, `backspace`, `ctrl-c`, `ctrl-d`, `home`, `end`, `pageup`, `pagedown`, `f1`–`f12` |
| `background` | bool | — | With `run`, start the command in the background immediately; the result includes a `sessionId`. On Unix, a trailing `&` in the command is normalized to this mode automatically |
| `pty` | bool | — | Allocate a pseudo-terminal for interactive programs; not supported on Windows |
| `cwd` | string | — | Working directory for the command. The last explicit value is remembered for later calls in the same conversation; omit `cwd` to reuse it. Defaults to the agent workspace when no value is remembered |
| `env` | object | `run` | Additional environment variables for this run. Values must be strings. The map is remembered for later calls in the same conversation and merged with new entries; pass an empty object (`{}`) to clear remembered variables. Cannot override `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `LD_AUDIT`, `LD_DEBUG`, or any `PICOCLAW_*` variable; those entries are ignored |
| `timeout` | int | — | Timeout in seconds for foreground commands; overrides `tools.exec.timeout_seconds`. Foreground runs are capped at `600`s: `0` (no timeout) and values above the cap start a tracked background session instead of blocking the call. Cannot be combined with `background=true` (except `0`); ignored with a note when a trailing `&` normalizes the run to background. With `wait`: maximum seconds to block (a positive integer), defaulting to and capped by `tools.exec.timeout_seconds` |
| `notify` | bool | — | With `background=true` or a run promoted to background, send a completion message when the process exits (default `false`). Manually killed sessions do not notify. Use it for jobs with a defined end; do not poll or wait in a loop |
| `offset` | int | — | With `read`: absolute byte offset into the retained session log. By default the response starts at the most recent `limit` bytes; use `0` for the oldest retained byte |
| `limit` | int | — | With `read`: maximum bytes to return. Defaults to `tools.exec.max_output_chars` |

Behavior notes:

- Run one independent command per tool call and chain dependent commands with `&&` (`;` when later commands must run regardless of earlier failures). Do not separate commands with newlines.
- A foreground run is capped at `600`s. If the effective timeout is larger (including `timeout: 0`, which disables the limit), the command is not rejected: it starts as a tracked background session, the result contains its `sessionId` and a `note` explaining the promotion, and `notify: true` is accepted. Use `wait`, `poll`, or `read` with that id.
- When a foreground run times out, the command is terminated and the model-facing result body ends with a hint to re-run it with `background=true` if it is a server, daemon, or watch process. This is the reliable way to keep a long-lived process: a timeout is evidence that the command outlived the call.
- Unix only: a trailing `&` in `command` is normalized to a tracked background session. The operator is removed so the tracked process is the command itself instead of a shell that exits immediately; the result contains the `sessionId` and a `note`. A `timeout` supplied with that command is ignored with a note because background sessions are not time-limited. Only a standalone final operator is rewritten: `&&`, `|&`, `2>&1`, quoted or escaped `&`, comments, and heredoc bodies are left untouched.
- Prefer `cwd` over `cd` inside the command, and the filesystem tools over shell file operations.
- Foreground output is bounded while the command runs: the first 40% and the last 60% of `max_output_chars` (default 50,000, also capped by `tools.max_inline_result_chars`) stay in memory around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, and the full stream is spilled to a workspace artifact (up to 5,000,000 bytes) as soon as the cap is exceeded. The result includes the artifact path, so the omitted middle stays readable; `truncated` in the metadata header is `true`. Background sessions retain a rolling 1 MB window of output; earlier bytes are dropped and reported via `droppedBytes`.
- `poll` never blocks and returns a short output preview. `read` is non-destructive and pageable with `offset`/`limit`; the response includes `totalBytes`, `offset`, `nextOffset`, `hasMore`, `droppedBytes` (bytes evicted from the rolling window since the session started) and `skippedBytes` (bytes skipped because the requested offset predates the retained window). `wait` returns the exit status and output when the process exits, and a partial output tail when its window expires. `kill` returns the final output tail and keeps the session readable for the retention window.
- To wait for a background session, call `wait` once instead of polling in a loop. If the window elapses first, the result has status `running` and a note saying this is not an error; call `wait` again if the job is expected to take longer. After two consecutive `poll`/`wait` calls with no new output the tool enables a completion notification and tells the model to stop checking (use `wait`, read the output, or kill the session).
- Sessions are cleaned up 30 minutes after the process exits. `write` sends stdin, `send-keys` sends keys to a PTY session; combine `background=true` with `pty=true` for interactive sessions such as a REPL.

## Environment sanitization

Exec does not hand the full PicoClaw process environment to child processes. Unrelated variables — provider API keys, channel tokens, and other credentials — are stripped unless explicitly allowed.

Inherited by default:

- Core: `PATH`, `HOME`, `USER`, `LANG`, `SHELL`, `TERM`, `PWD`, `OLDPWD`, `HOSTNAME`, `LOGNAME`, `TZ`, `DISPLAY`, `TMPDIR`, `EDITOR`, `PAGER`
- Proxies: `HTTP_PROXY`, `http_proxy`, `HTTPS_PROXY`, `https_proxy`, `NO_PROXY`, `no_proxy`
- Locale: all `LC_*` variables (`LC_ALL`, `LC_CTYPE`, `LC_MESSAGES`, `LC_MONETARY`, `LC_NUMERIC`, `LC_TIME`, `LC_PAPER`, `LC_NAME`, `LC_ADDRESS`, `LC_TELEPHONE`, `LC_MEASUREMENT`, `LC_IDENTIFICATION`, `LC_COLLATE`)
- Windows only, additionally: `PATHEXT`, `SYSTEMROOT`, `SYSTEMDRIVE`, `WINDIR`, `COMSPEC`, `APPDATA`, `LOCALAPPDATA`, `PROGRAMDATA`, `USERPROFILE`, `HOMEDRIVE`, `HOMEPATH`, `TEMP`, `TMP`

Sources are merged in this order:

1. `tools.exec.env_set` — trusted values from config; they override inherited variables, including `PATH`.
2. The built-in allowlist above, plus names from `tools.exec.env_allowlist`.
3. The per-call `env` argument — merged last, except for blocked names.
4. Trusted `PICOCLAW_EXEC_TIME` and `PICOCLAW_EXEC_TIMEOUT` values for the current run.

The per-call `env` argument cannot override `PATH`, `HOME`, `USER`, `LOGNAME`, `SHELL`, `LD_PRELOAD`, `LD_LIBRARY_PATH`, `LD_AUDIT`, `LD_DEBUG`, or any `PICOCLAW_*` variable; blocked entries are ignored. The managed `PICOCLAW_*` names above are agent-controlled, so `env_set` cannot override them either. Commands can read these variables:

| Variable | Value |
|----------|-------|
| `PICOCLAW_HOME` | PicoClaw home (`PICOCLAW_HOME` or `~/.picoclaw`) |
| `PICOCLAW_CONFIG` | Config path (`PICOCLAW_CONFIG` or `$PICOCLAW_HOME/config.json`) |
| `PICOCLAW_AGENT_WORKSPACE` | The agent workspace passed to the exec tool |
| `PICOCLAW_EXE` | Path to the running PicoClaw executable |
| `PICOCLAW_SERVICE_NAME` | `PICOCLAW_SERVICE_NAME`, or `picoclaw` |
| `PICOCLAW_EXEC_TIME` | RFC 3339 timestamp when the command started |
| `PICOCLAW_EXEC_TIMEOUT` | Effective foreground timeout as a Go duration (for example `3m0s` for the 180s default); `0s` when no timeout applies, including background sessions |

The same environment is used for foreground runs, background and PTY sessions, and cron shell commands. If an existing workflow relies on an inherited variable, add its name to `env_allowlist` (inherits the parent value) or add a fixed value to `env_set`. Allowlisting a secret passes it to every exec command, so prefer `env_set` for values that only one workflow needs. A command that prints its environment exposes those values in the tool result.

## Per-conversation state

Within one agent conversation, the exec tool remembers the last explicit `cwd` and `env` arguments:

- `cwd`: passing a directory stores it for later calls; omit `cwd` to reuse it. A new `cwd` replaces the remembered one. The path is revalidated against the workspace and must still exist; if it was deleted or became invalid, it is forgotten and the agent workspace is used.
- `env`: entries are merged into the remembered overlay, so a later call only needs to pass new variables. When `env` is omitted, the overlay is reused. An explicit empty object (`{}`) clears the overlay. Remembered variables are still filtered by the LLM blocklist and merged with `tools.exec.env_set` and the inherited allowlist on every run.

This state is **argument state**, not shell state: `cd` or `export` executed inside a command still disappears when that command's fresh shell exits. Use the `cwd` and `env` arguments (or a background PTY session) when state must survive across calls.

State is memory-only, scoped to the agent and session, and lost when PicoClaw restarts or the conversation is cleared with `/clear` or `/reset`. At most 64 conversations are retained; the least recently used conversation is evicted first. Calls without a session context (for example some cron paths) remain stateless.

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
      ],
      "env_set": {
        "APP_ENV": "production"
      },
      "env_allowlist": [
        "GOPATH",
        "GOROOT"
      ]
    }
  }
}
```
