---
description: "Subprocess isolation in PicoClaw: enable OS-level sandboxing for exec, CLI providers, hooks, MCP servers and plugins; configure expose_paths and block_network on Linux, macOS and Windows."
---
# Subprocess Isolation

PicoClaw can run the child processes it spawns inside an OS-level sandbox. The feature is optional and controlled by the top-level `isolation` block in `config.json`.

Isolation applies to child processes only. The main `picoclaw` process keeps running with the host user's permissions.

## What Is Covered

- `exec` tool commands
- CLI providers such as `claude-cli` and `codex-cli`
- process hooks
- MCP `stdio` servers
- plugin subprocesses
- the Delta Chat RPC server
- the Weixin SILK voice decoder

Known exception: the Linux USB device monitor spawns `udevadm` directly because it only observes host devices; it is outside the child-process isolation scope.

## Quick Start

On Linux, install bubblewrap first:

```bash
sudo apt install bubblewrap   # Debian/Ubuntu
sudo dnf install bubblewrap   # Fedora
sudo pacman -S bubblewrap     # Arch
sudo apk add bubblewrap       # Alpine
```

Then enable isolation in `config.json`:

```json
{
  "isolation": {
    "enabled": true
  }
}
```

Restart PicoClaw after changing the block. There is no automatic fallback when the platform backend is missing or unusable: startup fails instead of silently running without isolation.

## Configuration

```json
{
  "isolation": {
    "enabled": false,
    "expose_paths": [],
    "block_network": false
  }
}
```

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `false` | Enables subprocess isolation |
| `expose_paths` | array | `[]` | Explicit host paths made visible inside the isolated environment; only used when `enabled` is `true` |
| `block_network` | bool | `false` | Drops child processes into a separate network namespace. Linux only; preflight rejects it on macOS and Windows |

### `expose_paths`

Each entry describes one host path:

| Field | Description |
|-------|-------------|
| `source` | Host path |
| `target` | Path inside the isolated environment; defaults to `source` when empty |
| `mode` | `ro` or `rw` |

Rules:

- Only one final rule may exist for the same `target`.
- Later-loaded config overrides earlier rules for the same `target`.
- Linux supports real `source -> target` remapping.
- macOS requires `target` to match `source` because Seatbelt cannot remap paths.
- Windows does not currently support `expose_paths`; configuring it fails instead of pretending the paths were exposed.

Example:

```json
{
  "isolation": {
    "enabled": true,
    "expose_paths": [
      {
        "source": "/opt/toolchains/go",
        "target": "/opt/toolchains/go",
        "mode": "ro"
      },
      {
        "source": "/data/shared-assets",
        "target": "/opt/picoclaw-instance-a/workspace/assets",
        "mode": "rw"
      }
    ]
  }
}
```

### Network isolation

By default (`block_network: false`) child processes share the host network namespace. Loopback services, cloud-instance metadata endpoints such as `http://169.254.169.254/`, sidecar daemons and local databases are reachable from inside the sandbox. Any network-capable runtime (`curl`, `python`, `node`, and so on) can exfiltrate data; the exec tool's command guards are not a network boundary.

Setting `block_network: true` passes `--unshare-net` to bubblewrap and places child processes in a fresh network namespace with only a loopback interface (`lo`). Connections to host services or the internet fail with "connection refused" or "network unreachable".

What breaks when `block_network: true`:

- `curl`, `wget` and other HTTP clients inside the sandbox
- `git fetch`, `git clone`, `git push` and other network operations
- package managers that download packages (`pip`, `npm`, `apt`, and so on)
- MCP servers that need internet access
- any agent tool that relies on outbound network calls from child processes

If agents need filtered outbound internet but must not reach host-local services, route their traffic through an egress proxy instead of using `block_network`.

## Platforms

| Platform | Backend | Notes |
|----------|---------|-------|
| Linux | `bwrap` (bubblewrap) | Required. PID namespace isolation is mandatory. Optional network namespace. |
| macOS | Seatbelt via `/usr/bin/sandbox-exec` | Same-path access only. Network remains available. |
| Windows | Restricted token, low integrity, `Job Object` | Process-level restrictions. `expose_paths` is not supported. |
| Other | None | Enabling isolation fails instead of pretending it succeeded. |

### Linux

The Linux backend depends on `bwrap` (bubblewrap). It provides:

- a minimal filesystem view
- PID namespace isolation (required; the sandbox has its own PID table and `/proc` only describes its own processes)
- IPC namespace isolation
- a redirected child-process user environment
- `source -> target` read-only or read-write mounts
- all Linux capabilities dropped (`--cap-drop ALL`)
- a new session (`--new-session`) with no controlling terminal, blocking TIOCSTI-based terminal injection (CVE-2017-5226)
- an ephemeral `/tmp` tmpfs per sandbox; the host `/tmp` is not exposed
- optional network namespace isolation (`block_network: true`)

#### Instance root and default mounts

The instance root follows `config.GetHome()`: `PICOCLAW_HOME` when set, otherwise `~/.picoclaw`.

Default mounts inside the instance root are intentionally narrow:

| Path | Mode | Purpose |
|------|------|---------|
| `<root>/workspace` | rw | Agent working area |
| `<root>/runtime-user-env` | rw | Redirected `HOME`, `TMPDIR` and XDG directories |
| `<root>/skills` | ro | Skill scripts the sandbox may execute |

`config.json` (API keys), `.security.yml`, `logs`, `cache`, `state` and all other instance-root files are **not mounted**. A sandboxed command cannot read or modify the host configuration.

Minimum runtime system paths (`/usr`, `/bin`, `/lib`, `/lib64`, `/etc/resolv.conf`, and so on) are mounted read-only.

At runtime PicoClaw also adds the executable path, its directory, the effective working directory and absolute path arguments when needed. The working directory is mounted read-only unless it is already covered by a writable rule (`workspace`, `runtime-user-env` or an explicit `rw` `expose_paths` rule), so a remembered `cwd` outside the writable boundary cannot turn into a writable hole. Symlinked working directories are resolved to their canonical path before mounting so bubblewrap 0.12 and later accept them as bind-mount targets.

Absolute path arguments follow these rules:

- An argument is treated as a path only when it is a plain absolute token with no whitespace, control characters or shell metacharacters (`;`, `&`, `|`, `<`, `>`, `(`, `)`, `$`, backtick, quotes, `\`, `*`, `?`, `[`, `]`, `{`, `}`). Shell command strings passed to `sh -c` are therefore never treated as paths.
- Paths under `/proc`, `/sys` and `/dev` are never auto-mounted from the host. The sandbox provides its own `/proc` and `/dev`.
- Existing paths are exposed **read-only**. Directories passed on the command line do not become writable holes; writable locations come from `expose_paths` or the effective working directory.
- Non-existent paths expose their parent directory **read-write** so the command can create output files. This supports tools such as the Weixin SILK voice decoder that write to a not-yet-existing output path.
- Broad paths are **refused**: `/`, any path directly under `/` (for example `/home`, `/etc`, `/tmp`, `/proc`), the invoking user's home directory and every ancestor of that home directory. A single warning is logged per distinct refused path. Use `isolation.expose_paths` to grant access explicitly.
- If the mount plan already covers the path (an exact match or an ancestor), no additional rule is added, preventing conflicting mode stacking.

#### Re-exposing the instance root

An operator who needs the full root visible can add an `expose_paths` rule whose target is the instance root:

```json
{
  "isolation": {
    "enabled": true,
    "expose_paths": [
      { "source": "/home/alice/.picoclaw", "target": "/home/alice/.picoclaw", "mode": "rw" }
    ]
  }
}
```

That rule merges in after the defaults and restores the full root view. The sandbox boundary notice in the agent system prompt reflects the change.

#### Spawn sites that need extra paths

Some channels and hooks store data under the instance root and need an explicit `expose_paths` rule when isolation is enabled:

| Spawn site | Path needed | Suggested rule |
|------------|-------------|----------------|
| Delta Chat RPC server | `~/.picoclaw/deltachat/<channel>` (rw) | Add that directory to `expose_paths` |
| Process hook with a script under the instance root | Hook script directory (ro or rw as needed) | Add the hook directory to `expose_paths` |
| Plugin MCP stdio server with a working directory under the instance root | Plugin directory (ro) | Add it to `expose_paths` |

The Weixin SILK voice decoder uses the OS temp directory (`/tmp`), not the instance root, so no extra rule is needed.

#### `PICOCLAW_CONFIG`

When isolation is enabled on Linux, `PICOCLAW_CONFIG` is not passed to `exec` child processes because `config.json` is not mounted and the path would point to a non-existent file. `PICOCLAW_HOME` is still set.

#### bubblewrap requirements

There is no automatic fallback when `bwrap` is missing. PicoClaw probes bubblewrap namespace flags once at startup.

The PID namespace (`--unshare-pid`) is required. If the probe fails, startup fails with a clear error naming the fix:

- Docker: add `--security-opt systempaths=unconfined`
- Kubernetes: set `procMount: Unmasked`

To opt out entirely, set `"isolation.enabled": false` (risk: no filesystem isolation). Optional flags such as `--unshare-ipc` are dropped with a warning when the kernel rejects them with `Creating new namespace failed` (EINVAL); the mount namespace, filesystem view and user environment remain active.

Supported bubblewrap versions:

| Version range | Support status |
|---------------|----------------|
| < 0.3.3 | **Refused at startup.** 0.1.x and 0.2.x lack the `--` argument separator PicoClaw always passes; they fail with `Unknown option --`. |
| >= 0.3.3, < 0.12.0 | Accepted with a one-time warning. CVE-2026-87766 (symlink traversal during sandbox setup can create files outside the sandbox) is not fixed. Distro packages may carry a backport; upgrading or using such a build is recommended. |
| >= 0.12.0 | Fully supported and recommended. CVE-2026-87766 is fixed. |

Setuid bubblewrap binaries are **always refused** regardless of version. The setuid execution model is the attack surface of privilege-escalation CVEs such as CVE-2026-41163. bubblewrap 0.12+ no longer uses setuid; remove the setuid bit or install a 0.12+ package.

Linux also rejects any `expose_paths` rule whose `target` passes through a symlink inside an earlier writable mount. This prevents a sandboxed agent from planting a symlink in its workspace and redirecting a mount to an arbitrary host path (CVE-2026-87766 on bwrap < 0.12.0). System-level symlinks such as Debian's `/bin -> usr/bin` are not affected because they sit under read-only mounts. Remove the symlink or choose a target path that does not traverse it.

#### Do not run as root

When the effective uid is 0, bwrap does not create a user namespace automatically, so the sandboxed process would otherwise keep the full host capability set even after `--cap-drop`. PicoClaw passes `--unshare-user` for root callers, which creates a new user namespace where capabilities are reset to zero. Running as root still leaves a wider attack surface than a dedicated unprivileged user.

### macOS

The macOS backend uses `/usr/bin/sandbox-exec` with a Seatbelt default-deny profile. It allows writes under the PicoClaw instance root and explicit `rw` paths, while exposing system files, the command, its configured working directory and absolute path arguments as needed.

- A read-only `expose_paths` rule under the instance root is rejected: Seatbelt rules are additive, so a later read-only allow cannot revoke the root's write access.
- `source -> target` remapping is not supported; `target` must equal `source`.
- Network access remains available, matching Linux with `block_network: false`.
- Apple deprecated `sandbox-exec`; when it is missing, isolation fails closed.

### Windows

Windows isolation applies process-level restrictions:

- a restricted primary token
- low integrity level
- a `Job Object`
- redirected child-process user-environment directories (`USERPROFILE`, `HOME`, `TEMP`, `TMP`, `APPDATA`, `LOCALAPPDATA`)

It does not implement true `source -> target` filesystem remapping, and `expose_paths` is not supported. The sandbox boundary notice in the agent prompt has no path list on Windows.

### Other platforms

When isolation is explicitly enabled on an unsupported platform, PicoClaw reports it as an unsupported configuration instead of pretending isolation succeeded.

## Instance Root and Directories

The instance root follows `config.GetHome()`:

- If `PICOCLAW_HOME` is set, use it.
- Otherwise use the default `~/.picoclaw` directory under the user home.

If `config.GetHome()` falls back to `.` while isolation is enabled, startup fails.

Default instance directories include:

- the instance root
- `skills`
- `logs`
- `cache`
- `state`
- `runtime-user-env`

`workspace` comes from `cfg.WorkspacePath()` when configured, otherwise from the default workspace rule.

Windows also prepares `runtime-user-env/AppData/Roaming` and `runtime-user-env/AppData/Local`.

When isolation is enabled, child processes receive a redirected per-instance user environment:

- Linux and macOS: `HOME`, `TMPDIR`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `XDG_STATE_HOME`
- Windows: `USERPROFILE`, `HOME`, `TEMP`, `TMP`, `APPDATA`, `LOCALAPPDATA`

These paths point into `runtime-user-env` under the instance root.

## Environment Inheritance

When isolation is **enabled**, child processes inherit only a narrow base set of environment variables from the parent. All other variables — including API keys and other secrets set in the parent shell — are excluded.

The base set covers runtime necessities: `PATH`, `HOME`, `USER`, `LANG`, `LC_ALL`, `TERM`, `SHELL`, `TMPDIR` and variables with the `XDG_` prefix. Windows includes a broader set of Windows-specific variables.

Each spawn site may declare additional variables or vendor-namespace prefixes:

- **`claude-cli` provider**: `ANTHROPIC_*`, `CLAUDE_*`, proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` and lower-case variants), `SSL_CERT_FILE`, `NODE_EXTRA_CA_CERTS`
- **`codex-cli` provider**: `OPENAI_*`, `CODEX_*`, the same proxy and TLS variables
- **MCP `stdio` servers**: base set only, unless `inherit_env: true` is set in the server config, which passes the full parent environment
- **Process hooks**: base set plus variables declared in the hook configuration (`Env` field)
- **Delta Chat RPC server**: base set plus `DC_ACCOUNTS_PATH`
- **Weixin voice decoder** (ffmpeg / silk): base set only

CLI providers have no environment setting of their own: a variable outside the lists above (for example `AWS_*` for a Bedrock-backed `claude` CLI) is not forwarded while isolation is enabled.

When isolation is **disabled**, every child process inherits the full parent environment unchanged.

## Prompt Boundary Notice

When isolation is enabled and supported, the agent system prompt includes a `Sandbox Boundaries` section. It is generated from `DescribeBoundary` when the agent is created and lists:

- the workspace and the PicoClaw instance root
- the scratch directory (`$TMPDIR`)
- user-configured `expose_paths`, split into writable and read-only targets
- whether subprocess network access remains available

The notice tells the model that a `Read-only file system`, missing path or permission error at the boundary is not a broken host. The model should report it and ask the user to add the path to `isolation.expose_paths` instead of retrying with `sudo`, another absolute path or another tool.

The notice describes child-process isolation only. File tools follow `restrict_to_workspace` and `allow_read_outside_workspace` independently. Config reload rebuilds the agent registry, so a changed isolation configuration is reflected in the next system prompt.

## Logs and Troubleshooting

When isolation is enabled, PicoClaw logs the generated isolation plan:

- Linux: `linux isolation mount plan`
- Windows: `windows isolation access rules`
- macOS: debug log `macOS Seatbelt profile prepared`

If you suspect isolation is ineffective, check those logs for unexpected host paths.

## Relationship To `restrict_to_workspace`

- `restrict_to_workspace` limits the paths an agent is normally allowed to access.
- Subprocess isolation limits what a child process can see and where its user environment points.

They complement each other and do not replace each other.

## Current Limits

- Linux isolation uses `bwrap`, not a custom in-process isolation runtime.
- Linux enables a dedicated PID namespace by default; Docker containers must add `--security-opt systempaths=unconfined`.
- Windows does not yet implement full host ACL enforcement for every allowed or denied path.
- macOS Seatbelt does not support `source -> target` path remapping and relies on Apple's deprecated `sandbox-exec`.
- Isolation covers child processes, not the main `picoclaw` process.
- `block_network` is Linux only.

## Implementation Reference

The package-level implementation notes, covering the startup path and the code reading order, are in [`pkg/isolation/README.md`](https://github.com/afjcjsbx/picoclaw/blob/main/pkg/isolation/README.md).

Related documentation:

- [Configuration Guide](../guides/configuration.md): all `config.json` blocks, including `isolation`
- [Exec Tool](../tools/exec.md): command guard, risk classification and exec limitations
- [Tools Configuration](../reference/tools_configuration.md): tool switches and filesystem access policy
