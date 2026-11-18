---
description: "The exec tool: run shell commands with a timeout, working directory and environment, plus the PICOCLAW_TOOLS_EXEC_ environment variables that configure it."
---
# Exec tool

The exec tool is used to execute shell commands.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Enable the exec tool |
| `enable_deny_patterns` | bool | `true` | Enable the built-in shell-construct and destructive policy deny set |
| `custom_deny_patterns` | string[] | `[]` | Additional deny patterns (regular expressions); always enforced and they take priority over allow rules |
| `custom_allow_patterns` | string[] | `[]` | Full-command allow patterns (regular expressions, anchored automatically). They exempt the default and policy deny sets; hardline and custom deny patterns still win |
| `allow_remote` | bool | `false` | When `false`, exec only accepts commands that originate from internal channels (`cli`, `system`, `subagent`). Remote channels are then blocked (fail-closed): an empty channel is also rejected. Set it to `true` only when remote command execution is explicitly intended |
| `timeout_seconds` | int | `180` | Command timeout in seconds. `0` means the default (180s). Foreground runs are capped at `600`s; a larger configured timeout promotes commands to a tracked background session |
| `max_output_chars` | int | `50000` | Foreground output cap. Longer output keeps the first 40% and the last 60% around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, while the full stream is spilled to a workspace artifact (up to 5,000,000 bytes). The inline view is also bounded by `tools.max_inline_result_chars`; the artifact preserves the full stream. `0` means the default (50000) |
| `env_set` | object | `{}` | Variables always injected into every exec child process. Values are trusted configuration: they override inherited variables, including `PATH`. Every listed value reaches every command, so only add secrets here when the commands genuinely need them |
| `env_allowlist` | string[] | `[]` | Additional parent variable names inherited on top of the built-in allowlist. Use explicit names, never wildcards: allowlisting a secret passes it to every command |

`allow_remote` controls direct exec calls. Scheduled cron command jobs are
authorized by `tools.cron.command_allowed_remotes` instead and are not blocked by
`allow_remote`.

Environment variables use the `PICOCLAW_TOOLS_EXEC_` prefix: `PICOCLAW_TOOLS_EXEC_ENABLED`, `PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_ALLOW_REMOTE`, `PICOCLAW_TOOLS_EXEC_CUSTOM_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS`, `PICOCLAW_TOOLS_EXEC_TIMEOUT_SECONDS`, `PICOCLAW_TOOLS_EXEC_MAX_OUTPUT_CHARS`, `PICOCLAW_TOOLS_EXEC_ENV_ALLOWLIST` (comma-separated). `env_set` is JSON-only because its value is structured.

### Isolation

The OS sandbox is the hard boundary. `isolation.mode` is `required` by default: exec fails closed when the platform backend is unavailable. `auto` uses the local backend when available and falls back to the static policy; `off` explicitly runs on the host.

| Mode | Behavior |
|------|----------|
| `off` | Run on the host. The static policy still applies. |
| `auto` | Use the configured platform backend when it is available; otherwise fall back to the static policy. |
| `required` | Require the backend. A missing backend or a failed preflight rejects the command. |

The instance root is mounted read-only. Only the workspace and `runtime-user-env` are writable. `isolation.expose_paths` adds explicit `ro`/`rw` paths, and the working directory is writable only when it is inside the workspace or an `rw` expose path. Absolute command arguments are read-only; they never make their parent writable.

Exec uses the top-level `isolation` block.

```json
{
  "isolation": {
    "mode": "required",
    "expose_paths": [
      {"source": "/opt/toolchain", "target": "/opt/toolchain", "mode": "ro"}
    ]
  }
}
```

### Policy patterns

The static guard is a policy layer, not a containment boundary. It does not parse the command. With `enable_deny_patterns: true` (the default) it enables:

- shell-construct patterns: command substitution `$()`/backticks, pipe-to-shell and shell heredocs;
- destructive policy patterns: recursive `rm`, force-push, `git reset --hard`, `git clean -f`, `terraform destroy`, `kubectl delete`/`apply`/`exec`, `helm uninstall`/`delete`/`install`/`upgrade`, AWS delete verbs, `npm publish`, `docker rm`/`rmi`, `docker run --privileged`, recursive `chmod`/`chown`, `find -delete`/`-exec`, `truncate`, `shred` and process kills.

Custom deny patterns always apply. Custom allow patterns are anchored to the whole command and exempt the default and policy set, but never hardline or custom deny. `enable_deny_patterns: false` disables the policy set; hardline and custom deny remain.

Hardline patterns are always on: fork bombs, disk formatting/partitioning, raw disk writes, recursive deletion of protected system roots, `kill -1`, `kill -9 1`, host power changes, and self-protection against killing PicoClaw or deleting its state directory or executable.

The policy is static and best-effort. It cannot inspect script files, interpreter programs (`python3 -c`, `perl -e`) or build tools; the sandbox is responsible for containment in those cases.

## Tool arguments

The `exec` tool executes shell commands and manages **background sessions** (long-running commands) and **PTY sessions** (interactive programs). For a one-shot command only `command` is needed; the other arguments operate on sessions created with `background=true`.

## Foreground result format

A foreground `run` that starts a process returns a one-line metadata header to the model, followed by the stdout/stderr body. ANSI escape sequences are removed while the stream is read, so the body, the truncation count and the spill artifact all contain visible text only:

```text
[exec] {"exitCode":0,"timedOut":false,"truncated":false,"totalBytes":6,"durationMs":42}
hello
```

| Field | Description |
|-------|-------------|
| `exitCode` | Process exit status. `null` when the direct child did not return a numeric status: timeout, signal termination, or failure to start. A shell wrapper may turn a signal death into `128+N` instead, for example `137` (128+9, SIGKILL — often the OOM killer) or `143` (128+15, SIGTERM). A background session terminated with `kill` reports `-1` |
| `timedOut` | `true` when the configured timeout stopped the command |
| `truncated` | `true` when the output exceeded the configured cap; the omitted middle stays in the artifact |
| `totalBytes` | Total bytes the command wrote to stdout/stderr before any inline bounding. Omitted when the stream was empty |
| `omittedBytes` | Present when `truncated` is `true`: the stream bytes outside the inline head/tail view, so the size of the gap is known before reading the artifact |
| `artifactPath` | Present when `truncated` is `true` and the artifact was written: absolute path of the full stream, capped at `5,000,000` bytes. The same path is also exposed as a `[file:...]` tag |
| `durationMs` | Wall-clock duration of the command in milliseconds |
| `cwd` | Present when a foreground command ran on Unix: the absolute directory where it ended (`pwd -P`). It is remembered for later calls when it stays inside the workspace |
| `exitNote` | Present for a non-zero status with a well-known non-error meaning, for example `grep`/`rg` exit `1` (no matches), `diff` exit `1` (files differ), `test` exit `1` (false), `git` exit `1` (often normal), or a signal status such as `137` (SIGKILL, often the OOM killer), `139` (SIGSEGV) or `143` (SIGTERM) |
| `error` | Present when the command timed out or was terminated without a numeric exit status. Command stderr is merged into the body and is not repeated here |

The header stays outside the output body, so newlines, tabs and quotes are not JSON-escaped. The user-facing message is the output body without the header; on failure it keeps the human-readable `[Command exited with code N]` suffix.

Non-zero exit codes are not automatically tool failures: `grep` returns `1` when there is no match and `diff` returns `1` when files differ. Interpret `exitCode` in context, or use the `exitNote` field when it is present. The tool still marks a non-zero exit as an error result internally for UI and logging purposes.

Signal terminations are different from ordinary non-zero exits. `137` usually means SIGKILL, including the OOM killer, and an out-of-memory kill may leave no stderr of its own: check the system log (for example `dmesg -T | tail`) before retrying. `143` means SIGTERM. On Unix, when the direct child is killed by a signal the header reports `exitCode: null` and the human-readable suffix says `killed by signal`; the `kill` action reports `-1` for a session it terminated.

When output is truncated, the header carries `totalBytes`, `omittedBytes` and `artifactPath`, and the same artifact path also appears after the body as a `[file:...]` local artifact path, with the full stream stored up to `5,000,000` bytes. The message as a whole is therefore not one JSON document: only the first line is JSON metadata.

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
| `cwd` | string | — | Working directory for the command. The last cwd observed after a foreground command is remembered for later calls in the same conversation; omit `cwd` to reuse it. Defaults to the agent workspace when no value is remembered |
| `timeout` | int | — | Timeout in seconds for foreground commands; overrides `tools.exec.timeout_seconds`. Foreground runs are capped at `600`s: `0` (no timeout) and values above the cap start a tracked background session instead of blocking the call. Cannot be combined with `background=true` (except `0`); ignored with a note when a trailing `&` normalizes the run to background. With `wait`: maximum seconds to block (a positive integer), defaulting to and capped by `tools.exec.timeout_seconds` |
| `notify` | bool | — | With `background=true` or a run promoted to background, send a completion message when the process exits (default `false`). Manually killed sessions do not notify. Use it for jobs with a defined end; do not poll or wait in a loop |
| `offset` | int | — | With `read`: absolute byte offset into the retained session log. By default the response starts at the most recent `limit` bytes; use `0` for the oldest retained byte |
| `limit` | int | — | With `read`: maximum bytes to return. Defaults to `tools.exec.max_output_chars` |

Behavior notes:

- Run one independent command per tool call and chain dependent commands with `&&` (`;` when later commands must run regardless of earlier failures). Do not separate commands with newlines.
- A foreground run is capped at `600`s. If the effective timeout is larger (including `timeout: 0`, which disables the limit), the command is not rejected: it starts as a tracked background session, the result contains its `sessionId` and a `note` explaining the promotion, and `notify: true` is accepted. Use `wait`, `poll`, or `read` with that id.
- When a foreground run times out, the command is terminated and the model-facing result body ends with a hint to re-run it with `background=true` if it is a server, daemon, or watch process. This is the reliable way to keep a long-lived process: a timeout is evidence that the command outlived the call.
- Unix only: a trailing `&` in `command` is normalized to a tracked background session. The operator is removed so the tracked process is the command itself instead of a shell that exits immediately; the result contains the `sessionId` and a `note`. A `timeout` supplied with that command is ignored with a note because background sessions are not time-limited. Only a standalone final operator is rewritten: `&&`, `|&`, `2>&1`, quoted or escaped `&`, comments, and heredoc bodies are left untouched.
- A foreground command that starts untracked background work is rejected with guidance to use `background=true`: an inner `&` (`a & b`, `a & # comment`) and the detaching commands `nohup`, `setsid` and `disown`. The single trailing `&` form is converted, not rejected; `background=true` also accepts `nohup`/`setsid` because the tracked shell remains the session root.
- The cwd where a foreground command ends is reported as `cwd` in the `[exec]` metadata header and remembered for the next call in the conversation. A `cd` inside the command is therefore visible, but `cwd` remains the clearest way to select the starting directory. Prefer the filesystem tools over shell file operations.
- Foreground output is bounded while the command runs: the first 40% and the last 60% of `max_output_chars` (default 50,000, also capped by `tools.max_inline_result_chars`) stay in memory around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, and the full stream is spilled to a workspace artifact (up to 5,000,000 bytes) as soon as the cap is exceeded. The `[exec]` header reports `truncated: true` together with `totalBytes`, `omittedBytes` and `artifactPath`, so the model knows the real size of the full output and can read the omitted middle instead of re-running the command. Background sessions retain a rolling 100 KB window in memory; as soon as it overflows, the complete stream is written to an artifact (up to 5,000,000 bytes). `poll`, `wait`, `read` and `kill` responses include `artifactPath` (and `artifactCapped` when the 5,000,000-byte limit is reached), so earlier bytes stay readable without keeping them in RAM. `droppedBytes` still reports the bytes evicted from the in-memory window.
- ANSI escape sequences (colors, cursor movement, OSC/DCS/APC strings) are stripped from foreground and background output as it is read. Stripping happens before truncation and spilling, so escape bytes do not consume the output budget and do not reach the model or an artifact. A sequence split across read chunks is still removed.
- `poll` never blocks and returns a short output preview. `read` is non-destructive and pageable with `offset`/`limit`; the response includes `totalBytes`, `offset`, `nextOffset`, `hasMore`, `droppedBytes` (bytes evicted from the rolling window since the session started) and `skippedBytes` (bytes skipped because the requested offset predates the retained window). `wait` returns the exit status and output when the process exits, and a partial output tail when its window expires. `kill` returns the final output tail and keeps the session readable for the retention window.
- To wait for a background session, call `wait` once instead of polling in a loop. If the window elapses first, the result has status `running` and a note saying this is not an error; call `wait` again if the job is expected to take longer. After two consecutive `poll`/`wait` calls with no new output the tool enables a completion notification and tells the model to stop checking (use `wait`, read the output, or kill the session).
- Starting a background session without `notify=true` returns a note that no completion message will be sent, so the model does not assume it will be woken when the job ends. Bounded jobs should normally use `notify=true`.
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
3. Trusted `PICOCLAW_EXEC_TIME` and `PICOCLAW_EXEC_TIMEOUT` values for the current run.

The model cannot add environment variables to a run. To set a variable for one command, use an inline assignment such as `FOO=bar command`; the guard inspects the whole command. The managed `PICOCLAW_*` names above are agent-controlled, so `env_set` cannot override them either. Commands can read these variables:

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

Within one agent conversation, the exec tool remembers the last working directory observed after a foreground command:

- An explicit `cwd` sets the starting directory and is remembered.
- On Unix, the shell wrapper appends a marker with `pwd -P` after the command, so `cd sub` inside a command updates the remembered directory too. The marker is stripped from the output and the path is reported as `cwd` in the `[exec]` header.
- The observed path is stored only when it is inside the workspace (when restriction is on) and still exists. A command that `cd`s outside, an interrupted command or a timeout leaves the previous value unchanged.
- Omit `cwd` to reuse the remembered directory. A path that disappeared or no longer passes the workspace policy is forgotten and the agent workspace is used.

This state is **cwd state**, not shell state: exported variables and aliases still disappear when that command's fresh shell exits. Use the `cwd` argument (or a background PTY session) when other state must survive across calls.

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

- **`enable_deny_patterns`**: Set to `false` to disable the default shell-construct and destructive policy patterns. The hardline floor and `custom_deny_patterns` still apply.
- **`custom_deny_patterns`**: Add custom deny regex patterns. These are always enforced, even when `enable_deny_patterns` is `false`, and custom allow patterns cannot exempt them.

## Hardline Always-On Patterns

Some catastrophic commands are an immutable floor: they stay blocked even when `enable_deny_patterns` is `false`, and no custom allow pattern can exempt them.

- Fork bombs: `:(){ :|:& };:`
- Disk formatting/partitioning: `mkfs`, `mkfs.*`, `diskpart`, `format C:`
- Raw disk access: `dd if=`, writing to `/dev/sd*` and other block devices
- Recursive deletion of `/` such as `rm -rf /` or `rm -rf /*`
- Power state changes: `shutdown`, `reboot`, `poweroff`
- Self-protection: killing the PicoClaw process (`pkill`/`killall` with `picoclaw`, `PICOCLAW_SERVICE_NAME` or `$PICOCLAW_EXE`), stopping its systemd service, deleting the state directory (`~/.picoclaw`, `$HOME/.picoclaw`, `$PICOCLAW_HOME` or the resolved `PICOCLAW_HOME`), or deleting the running executable (including through `$PICOCLAW_EXE`, `which` or `command -v`). The executable, state directory and service name are resolved when the tool is created, so the floor follows the running instance.

Hardline and custom deny patterns are matched against the command with `$IFS`/`${IFS}` expanded to spaces and backslash-newline continuations joined. This is a cheap best-effort normalization, not a parser; the sandbox remains the boundary for obfuscated commands. It runs independently of `enable_deny_patterns`, so disabling the policy set does not disable the hardline floor.

## Workspace and Sandbox Boundary

`agents.defaults.restrict_to_workspace` still validates an explicit `cwd` and the remembered cwd (symlink-resolved) before execution. The Unix AST workspace pass has been removed: path containment is enforced by the mandatory sandbox, which mounts the instance root read-only and the workspace read-write. Windows keeps the byte-level scanner because its restricted-token backend does not remap paths.

Inside the sandbox, commands can read the read-only system mounts and write only to the workspace, `runtime-user-env` and explicit `rw` expose paths. Absolute paths passed as command arguments do not become writable.

## Default Blocked Shell Constructs

With `enable_deny_patterns: true` (the default), the policy layer blocks:

- Command substitution: `$()`, backticks
- Pipe to shell: `| sh`, `| bash` (and the other POSIX shells)
- Shell heredoc: `sh <<EOF`, `bash <<'EOF'`
- Recursive deletion, force pushes, infrastructure destroy verbs, recursive permission changes, `find -delete`/`-exec` and process kills.

The list is deliberately compact and does not enumerate every dangerous command. Use `custom_deny_patterns` for site-specific rules. Windows keeps its PowerShell regex floor.

## Repeated Blocked Commands

Exec tracks guard rejections per conversation. The first two consecutive rejections of the same command with the same reason return the normal block message. The third and later rejections add a `HARD STOP` message that tells the model to stop retrying, not to reword or split the command, and to report the blocker to the user instead.

The streak starts over when a different command is blocked, when an allowed run succeeds, or when the conversation is cleared. State is process-local and bounded (64 conversations), and each entry holds only a 64-bit hash of the command and reason plus a counter; it is not persisted across restarts.

## Known Architectural Limitation

The policy layer only inspects the top-level command string. Build tools and interpreters can run code the policy cannot see. This is acceptable because the OS sandbox is the containment boundary: the child can write only to the workspace and the explicit `rw` mounts, regardless of the program it runs. Review `isolation.expose_paths` carefully; a broad `rw` mount widens the boundary.

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
