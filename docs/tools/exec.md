# Exec tool

The exec tool is used to execute shell commands.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Enable the exec tool |
| `enable_deny_patterns` | bool | `true` | Enable the default dangerous-command blocking |
| `custom_deny_patterns` | string[] | `[]` | Additional deny patterns (regular expressions); they take priority over allow rules |
| `custom_allow_patterns` | string[] | `[]` | Allow patterns (regular expressions) that explicitly permit matching commands. Deny patterns still win, so this cannot unlock a denied command |
| `allow_remote` | bool | `true` | When `false`, exec only accepts commands that originate from internal channels (`cli`, `system`, `subagent`). Remote channels are then blocked (fail-closed): an empty channel is also rejected |
| `timeout_seconds` | int | `60` | Command timeout in seconds. `0` means the default (60s) |

Environment variables use the `PICOCLAW_TOOLS_EXEC_` prefix: `PICOCLAW_TOOLS_EXEC_ENABLED`, `PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_ALLOW_REMOTE`, `PICOCLAW_TOOLS_EXEC_CUSTOM_DENY_PATTERNS`, `PICOCLAW_TOOLS_EXEC_CUSTOM_ALLOW_PATTERNS`, `PICOCLAW_TOOLS_EXEC_TIMEOUT_SECONDS`.

## Tool arguments

The `exec` tool executes shell commands and manages **background sessions** (long-running commands) and **PTY sessions** (interactive programs). For a one-shot command only `command` is needed; the other arguments operate on sessions created with `background=true`.

| Argument | Type | Required for | Description |
|----------|------|--------------|-------------|
| `action` | string | every call | `run` (execute a command), `list` (show sessions), `poll` (check a session's status), `read` (get a session's output), `write` (send input to a session's stdin), `kill` (terminate a session), `send-keys` (send keys to a PTY session) |
| `command` | string | `run` | Shell command to execute |
| `sessionId` | string | `poll`, `read`, `write`, `kill`, `send-keys` | Session ID returned by a background `run` |
| `data` | string | `write` | Data to write to the session's stdin |
| `keys` | string | `send-keys` | Key names: `up`, `down`, `left`, `right`, `enter`, `tab`, `escape`, `backspace`, `ctrl-c`, `ctrl-d`, `home`, `end`, `pageup`, `pagedown`, `f1`–`f12` |
| `background` | bool | — | With `run`, start the command in the background immediately; the result includes a `sessionId` |
| `pty` | bool | — | Allocate a pseudo-terminal for interactive programs; not supported on Windows |
| `cwd` | string | — | Working directory for the command; defaults to the agent workspace |
| `timeout` | int | — | Timeout in seconds for foreground commands; overrides `tools.exec.timeout_seconds`, and `0` disables it. Cannot be combined with `background=true` |

Behavior notes:

- Run one independent command per tool call and chain dependent commands with `&&` (`;` when later commands must run regardless of earlier failures). Do not separate commands with newlines.
- Prefer `cwd` over `cd` inside the command, and the filesystem tools over shell file operations.
- Foreground output is truncated after 10,000 bytes; background session output is limited to 1 MB.
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

- **`enable_deny_patterns`**: Set to `false` to completely disable the default dangerous command blocking patterns
- **`custom_deny_patterns`**: Add custom deny regex patterns; commands matching these will be blocked

## Default Blocked Command Patterns

By default, PicoClaw blocks the following dangerous commands:

- Delete commands: `rm -rf`, `del /f/q`, `rmdir /s`
- Disk operations: `format`, `mkfs`, `diskpart`, `dd if=`, writing to `/dev/sd*`
- System operations: `shutdown`, `reboot`, `poweroff`
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
