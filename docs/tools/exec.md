---
description: "The exec tool: run shell commands with a timeout, working directory and environment, plus the PICOCLAW_TOOLS_EXEC_ environment variables that configure it."
---
# Exec tool

The exec tool is used to execute shell commands.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Enable the exec tool |
| `enable_deny_patterns` | bool | `true` | Enable the built-in shell-construct deny set (command substitution, pipe-to-shell, shell heredocs) and the AST risk classifier |
| `custom_deny_patterns` | string[] | `[]` | Additional deny patterns (regular expressions); always enforced and they take priority over allow rules |
| `custom_allow_patterns` | string[] | `[]` | Full-command allow patterns (regular expressions, anchored automatically). They exempt only the built-in shell-construct deny set. They do not exempt the AST risk classifier; hardline and custom deny patterns still win |
| `risk_threshold` | string | `"medium"` | Highest risk level the AST classifier allows: `low`, `medium`, `high` or `critical`. Part of the built-in guard set, so it is inactive when `enable_deny_patterns` is `false`. Empty means `medium` |
| `unknown_command_policy` | string | `"ask"` | What to do with a command the AST classifier cannot resolve: `ask` (default) requests approval from the originating user, `allow` runs it without approval, `block` rejects it. With `ask`, a missing interactive user denies the command. Inactive when `enable_deny_patterns` is `false` or on Windows |
| `approval_timeout_seconds` | int | `300` | How long an unknown-command approval prompt waits for the user before the command is denied. `0` means the default (300s). It overrides `hooks.defaults.approval_timeout_ms` for this prompt |
| `allow_remote` | bool | `false` | When `false`, exec only accepts commands that originate from internal channels (`cli`, `system`, `subagent`). Remote channels are then blocked (fail-closed): an empty channel is also rejected. Set it to `true` only when remote command execution is explicitly intended |
| `timeout_seconds` | int | `180` | Command timeout in seconds. `0` means the default (180s). Foreground runs are capped at `600`s; a larger configured timeout promotes commands to a tracked background session |
| `max_output_chars` | int | `50000` | Foreground output cap. Longer output keeps the first 40% and the last 60% around a `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, while the full stream is spilled to a workspace artifact (up to 5,000,000 bytes). The inline view is also bounded by `tools.max_inline_result_chars`; the artifact preserves the full stream. `0` means the default (50000) |
| `env_set` | object | `{}` | Variables always injected into every exec child process. Values are trusted configuration: they override inherited variables, including `PATH`. Every listed value reaches every command, so only add secrets here when the commands genuinely need them |
| `env_allowlist` | string[] | `[]` | Additional parent variable names inherited on top of the built-in allowlist. Use explicit names, never wildcards: allowlisting a secret passes it to every command |

`allow_remote` controls direct exec calls. Scheduled cron command jobs are
authorized by `tools.cron.command_allowed_remotes` instead and are not blocked by
`allow_remote`.

Environment variables use the `PICOCLAW_TOOLS_EXEC_` prefix: `PICOCLAW_TOOLS_EXEC_ENABLED`, `PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_RISK_THRESHOLD`, `PICOCLAW_TOOLS_EXEC_UNKNOWN_COMMAND_POLICY`, `PICOCLAW_TOOLS_EXEC_ALLOW_REMOTE`, `PICOCLAW_TOOLS_EXEC_CUSTOM_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS`, `PICOCLAW_TOOLS_EXEC_TIMEOUT_SECONDS`, `PICOCLAW_TOOLS_EXEC_MAX_OUTPUT_CHARS`, `PICOCLAW_TOOLS_EXEC_ENV_ALLOWLIST` (comma-separated). `env_set` is JSON-only because its value is structured.

### Risk classification

When the built-in guard is enabled (`enable_deny_patterns: true`, the default), PicoClaw parses every Unix command with `mvdan.cc/sh/v3/syntax` before execution and classifies each simple command in the AST. Pipelines, `&&`/`||`/`;` lists, loops, functions and command substitutions are all walked. This catches indirection that regex deny lists cannot resolve, for example `x=rm; $x -rf /` or `"r"'m' -rf /`.

On Unix the AST classifier is the **single source of truth for command danger**. The default deny set only covers shell constructs that hide a command from the AST: command substitution, pipe-to-shell and shell heredocs. Command names such as `sudo`, `chmod`, `git push` or `rm -rf` follow `risk_threshold`.

The built-in table assigns a base level to known commands and a small set of argument modifiers raises destructive verbs. Commands that are not listed are **unknown**: the caller decides whether to allow, review or block them.

| Level | Meaning | Examples |
|-------|---------|----------|
| `low` | Read-only or informational | `ls`, `cat`, `grep`, `find`, `echo` |
| `medium` | Writes files or reads the network | `cp`, `mv`, `tar`, `curl`, `git status` |
| `high` | Destructive or system-wide side effects | `rm -r`, `chmod`, `git push`, `ssh`, `systemctl start` |
| `critical` | Privilege escalation or always dangerous | `sudo`, `dd`, `mkfs`, `shutdown`, `rm -rf` |

Commands above `risk_threshold` are rejected before any process starts, with a structured message:

```text
Command blocked by risk classifier: command="chmod +x script.sh" risk_level=high threshold=medium reason=command risk exceeds configured threshold
```

A command that is not in the table, a dynamic command name, a shell that reads commands from stdin or a script file, and an unparseable command are classified as unknown. `tools.exec.unknown_command_policy` decides what happens next: `ask` (default) requests approval from the user, `allow` runs the command, and `block` rejects it. Set `enable_deny_patterns: false` to disable the built-in guard set.

The classifier also:

- resolves a constant the command assigned earlier at the top level of the script (`R=rm; $R -rf x`, `set -- rm -rf; "$@" x`) and classifies the command it expands to. Resolution stops at the first construct that could change a variable unseen (a function, `eval`, `read`, a loop or `if`, a conditional assignment, a custom `IFS`); later words stay dynamic;
- classifies a command whose command word is dynamic and cannot be resolved (`"$cmd" -rf /`) as unknown, so `unknown_command_policy` decides;
- parses a literal `sh -c '...'` / `bash -c '...'` script recursively, and treats a shell that reads commands from stdin or a script file as unknown;
- parses a literal `trap '...'` action recursively, and treats an `xargs` invocation that launches a shell as unknown;
- inspects common wrappers (`env`, `command`, `nohup`, `time`, `nice`, `timeout`, `xargs`, `watch`, `flock`, `chrt`, `taskset`, `busybox`, `runuser`, `setpriv`, `capsh`, `machinectl`, `lxc`, `incus`) and classifies the command they launch; `runuser -c` and `flock -c` command strings are parsed as shell scripts;
- inspects the destructive verbs of `lxc`, `incus`, `machinectl` and `virsh` (for example `delete`, `shell`, `poweroff`, `destroy` and `qemu-monitor-command`);
- treats `env -S` as unknown, because it splits a string into a command;
- classifies process-detaching and namespace commands (`setsid`, `daemon`, `unshare`, `nsenter`, `bwrap`) as `high`. This is an approval signal, not a containment boundary: the exec tool's process scope is responsible for killing detached descendants.

Argument modifiers are applied after flag normalization (`rm -rf`, `rm -fr` and `rm -r -f` are equivalent; `-XPOST` and `--request=POST` match their spaced flag+value forms). The built-in modifiers cover:

- `rm`: `-r` is `high`, `-r -f` is `critical`;
- `git`: `push` is `high`, `push --force` is `critical`, `reset --hard`, `clean -f` and `branch -D`/`-d` are `high`;
- `systemctl` and `service`: state-changing verbs are `high`; `poweroff`, `reboot`, `halt`, `kexec` and `isolate` are `critical`;
- `cryptsetup`, `zpool`, `zfs` and `mdadm`: destructive verbs are `critical`;
- `lxc`, `incus`, `machinectl` and `virsh`: destructive verbs are `high` or `critical`;
- `find`: `-delete` and `-exec*` are `high`;
- `kill`: `-9` is `critical`;
- `aws`: any `delete-*`, `rm`, `rb`, `mv`, `purge`, `terminate-instances`, `stop-instances` or `--delete` verb is `high`.

`wipefs`, `blkdiscard`, `lvremove`, `dd`, `mkfs`, `fdisk`, `parted`, `mount`, `umount`, `shutdown`, `reboot`, `poweroff` and `halt` are always `critical`.

`rm` is not in the default deny set: the risk levels above are the source of truth. The hardline rule for recursive deletion of `/` still applies.

Custom threshold:

```json
{
  "tools": {
    "exec": {
      "risk_threshold": "medium",
      "unknown_command_policy": "ask",
      "approval_timeout_seconds": 300
    }
  }
}
```

### Unknown-command approval

When the classifier returns unknown and `unknown_command_policy` is `ask` (the default), PicoClaw sends a prompt to the conversation that requested the command:

```text
Approval required for an unknown exec command.
Reply "approve" to run it or "deny" to cancel. If more than one request is pending, reply "approve <token>" or "deny <token>" instead. This request expires in 1m0s.

Reason: command is not in the risk table
Command: "./deploy.sh --target staging"
```

The command runs only after the requesting user replies `approve`; `deny` cancels it and returns a denial to the model. The bare reply answers the single live request addressed to that user, so it needs no token on a phone keyboard. When several requests are pending the bare reply is refused and the user must send `approve <token>` or `deny <token>` with the token from the request; the tokenized form always works. In the Pico Web UI the prompt arrives as an interactive card with Approve and Deny buttons: clicking one sends the tokenized reply directly, even while the agent turn is blocked waiting for it. A request is bound to the requesting channel, account, chat, topic, space and sender, so another user in the same group cannot answer it. Approval replies are consumed by the agent loop and never enter the conversation or the LLM context.

The prompt waits `tools.exec.approval_timeout_seconds` (default `300`), not `hooks.defaults.approval_timeout_ms`; the setting applies to the next request after a config reload. A timeout, a missing or stopped channel, an automated sender (`cron`, `heartbeat`) or a missing origin denies the command. An unknown command longer than 12,000 characters is denied too: it cannot be reviewed safely in chat. This is deliberate fail-closed behavior: an unclassified command is never approved silently.

Only unknown commands use this gate. Commands above `risk_threshold` stay blocked and approval does not raise the threshold. Set `unknown_command_policy` to `allow` to restore the previous behavior, or `block` to reject unknown commands without prompting.

The CLI (`picoclaw agent`, both one-shot and interactive) does not run the gateway inbound loop, so it has no channel to answer the prompt and denies unknown commands. Use `allow` there, or run the gateway and approve from a chat channel.

`custom_allow_patterns` is anchored to the whole command (`^(?:...)$`). A bare `git` pattern matches only the command `git`, not `git push --force`. It exempts the default regex deny set only and does not affect the AST risk classifier. To allow a high-risk or unclassifiable command, raise `risk_threshold` globally. Hardline and custom deny patterns always win.

Limits worth knowing:

- The classifier is static. A dynamic argument that hides a flag (for example `curl "$FLAGS"`) cannot be inspected; a dynamic command word is treated as unknown and follows `unknown_command_policy`. An operand of a destructive command that comes from an expansion (for example `rm "$TARGET"`), even a resolved constant, is still rejected. Commands that run an interpreter (`python3 -c`, `perl -e`, `awk`) or rewrite files through other tools (`sed -i`, `cp /dev/null f`) are classified by name only; use [subprocess isolation](../security/isolation.md) to bound them.
- External programs still run with the OS user's file permissions. Shell redirections are checked by the path guard, but a dynamically built redirection target and file access performed by an external binary are not sandboxed by this layer. See [Subprocess Isolation](../security/isolation.md) for the optional OS-level child-process boundary.
- Windows is skipped because exec runs PowerShell there. The Windows-only regex floor keeps generic command patterns (`npm install -g`, `git push`, `docker run`, process kills, and so on) plus the PowerShell-specific checks described below.

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

- **`enable_deny_patterns`**: Set to `false` to disable the optional built-in guard set (the shell-construct deny patterns and the AST risk classifier). The hardline floor and `custom_deny_patterns` still apply.
- **`custom_deny_patterns`**: Add custom deny regex patterns. These are always enforced, even when `enable_deny_patterns` is `false`, and custom allow patterns cannot exempt them.

## Hardline Always-On Patterns

Some catastrophic commands are an immutable floor: they stay blocked even when `enable_deny_patterns` is `false`, and no custom allow pattern can exempt them.

- Fork bombs: `:(){ :|:& };:`
- Disk formatting/partitioning: `mkfs`, `mkfs.*`, `diskpart`, `format C:`
- Raw disk access: `dd if=`, writing to `/dev/sd*` and other block devices
- Recursive deletion of `/` such as `rm -rf /` or `rm -rf /*`
- Power state changes: `shutdown`, `reboot`, `poweroff`
- Self-protection: killing the PicoClaw process (`pkill`/`killall` with `picoclaw`, `PICOCLAW_SERVICE_NAME` or `$PICOCLAW_EXE`), stopping its systemd service, deleting the state directory (`~/.picoclaw`, `$HOME/.picoclaw`, `$PICOCLAW_HOME` or the resolved `PICOCLAW_HOME`), or deleting the running executable (including through `$PICOCLAW_EXE`, `which` or `command -v`). The executable, state directory and service name are resolved when the tool is created, so the floor follows the running instance.

Hardline and custom deny patterns are matched against a normalized command. Fixed shell obfuscation is resolved first: `$IFS`/`${IFS}` become spaces, ANSI-C `$'...'` strings are decoded, backslash-newline continuations are joined, and the AST exposes quote-spliced argv words. As a result, `r''m -rf /`, `rm${IFS}-rf${IFS}/` and `$'\x72\x6d' -rf /` are evaluated as `rm -rf /`. This normalization runs independently of `enable_deny_patterns`, so disabling the risk classifier does not disable the hardline floor.

## Workspace Restriction

When `agents.defaults.restrict_to_workspace` is `true` (the default), exec on Unix parses the command and validates it against the workspace before running it. Besides the absolute-path and `../` checks, this pass rejects:

- Tilde and `$HOME` expansion, standalone or as a path prefix, including `${HOME:-...}`: `cat ~/.ssh/id_rsa`, `cat $HOME/.ssh/id_rsa`, `echo pwn > ~/pwn`, `echo pwn > $HOME/pwn`.
- Parent traversal in directory positions, written without a slash: `cd ..`, `pushd ..`, `tar -C ..`, `git -C ..`, not only `../path`.
- Directory changes through `cd` and `pushd` are validated, including paths that escape through symlinks. `cd -`, bare `cd` (home) and dynamic targets such as `cd "$DIR"` are rejected when they cannot be validated statically. `env -C`/`--chdir` is rejected under workspace restriction instead of tracked, and `pushd` is validated like `cd`; the directory stack starts empty in each exec shell, so `popd`/rotation cannot reach an unvalidated directory.
- Relative operands are resolved against every directory the command entered, not only the workspace root. A second `cd` or a symlink under a subdirectory cannot be used to reach outside: `cd sub; cd link; cat secret.txt` and `cd sub; cat link/secret.txt` are rejected when `sub/link` points outside the workspace.
- Assignments or unsets of `HOME`, `PWD`, `OLDPWD` and `CDPATH` are rejected under workspace restriction, so `$HOME`/`$PWD` always resolve against the policy values instead of a value the static pass did not see.
- Dynamic redirection targets (`> "$OUT"`) and dynamic nested scripts.
- Commands nested in `sh -c`/`bash -c`, shell heredoc scripts, `exec`, `find -exec`, common wrappers (`command`, `builtin`, `env`, `timeout`, `xargs`, and similar) and the command strings of `flock -c`, `runuser -c` and `capsh --`. Backslash escapes are resolved first, so `cat \/etc/passwd` is checked as `/etc/passwd`. `eval` and `trap` are rejected because their command string cannot be followed reliably.

Expansion uses the child shell's `HOME`. Quoted literals such as `echo '$HOME'` and `echo '~'` are not treated as paths, and web URLs are exempt from local path checks. `file:` URIs are rejected fail-closed because they name local files that the static pass cannot validate.

This is a static best-effort layer, not an OS sandbox: child processes, scripts loaded after startup, file contents, dynamic operands and globs that expand to outside-pointing symlinks are outside its reach. Symlinks are resolved against the filesystem at validation time, so a symlink created or retargeted later in the same command is not visible to the static pass. Use [subprocess isolation](../security/isolation.md), containers, VMs, or a dedicated user for untrusted workloads. The AST pass runs only when workspace restriction is enabled, only for the command being executed, and keeps no persistent state, goroutines or polling timers. When the risk classifier is also enabled, both passes share one parse of the command; commands containing ANSI-C quoting or line continuations are re-parsed after normalization because those rewrites can change word boundaries.

## Default Blocked Shell Constructs

On Unix the AST risk classifier is the source of truth for command danger. The default deny set only covers constructs it cannot inspect; it is enabled by default and can be disabled with `enable_deny_patterns: false`. The constructs are read from the parse of the command, including the scripts it runs through `sh -c`, `eval` and wrappers, so quoted text is not a hit:

- Command substitution: `$()` and backticks. `echo '$(date)'` and arithmetic expansion `$((1+2))` are not substitutions.
- Pipe to shell: a pipe into a shell that executes its stdin, such as `| sh`, `| bash -s`, `| /bin/sh` or `| env bash`. A shell with its own `-c` script is classified by that script instead.
- Shell heredoc: a heredoc or here-string feeding a shell that executes stdin, such as `sh <<EOF` or `bash <<< 'cmd'`.

A command that does not parse, and every Windows command, falls back to equivalent text patterns. `custom_allow_patterns` exempts this set as before.

Command-level dangers such as `sudo`, `chmod`, `chown`, `kill`, `git push`, `eval` and `source` are classified by the risk table. Use `risk_threshold` to tune them.

Windows has no AST classifier, so its regex floor keeps the equivalent generic command patterns plus the PowerShell-specific checks: recursive `Remove-Item` (including `rm`/`ri`/`del` aliases), `Clear-Disk`, `Format-Volume`, `vssadmin delete shadows`, `bcdedit /set`, `reg delete`, `icacls /grant Everyone`, download-and-execute pipelines such as `iwr ... | iex`, `taskkill /F`, and `Stop-Process -Force`.

## Repeated Blocked Commands

Exec tracks guard rejections per conversation. The first two consecutive rejections of the same command with the same reason return the normal block message. The third and later rejections add a `HARD STOP` message that tells the model to stop retrying, not to reword or split the command, and to report the blocker to the user instead.

The streak starts over when a different command is blocked, when an allowed run succeeds, or when the conversation is cleared. State is process-local and bounded (64 conversations), and each entry holds only a 64-bit hash of the command and reason plus a counter; it is not persisted across restarts.

## Known Architectural Limitation

The exec guard only validates the top-level command sent to PicoClaw. It does **not** recursively inspect child
processes spawned by build tools or scripts after that command starts running.

Examples of workflows that can bypass the direct command guard once the initial command is allowed:

- `make run`
- `go run ./cmd/...`
- `cargo run`
- `npm run build`

This means the guard is useful for blocking obviously dangerous direct commands, but it is **not** a full sandbox for
unreviewed build pipelines. If your threat model includes untrusted code in the workspace, use
[subprocess isolation](../security/isolation.md) or stronger isolation such as containers, VMs, or an approval flow
around build-and-run commands.

## Configuration Example

```json
{
  "tools": {
    "exec": {
      "enable_deny_patterns": true,
      "risk_threshold": "medium",
      "unknown_command_policy": "ask",
      "approval_timeout_seconds": 300,
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
