# `pkg/isolation`

`pkg/isolation` provides process-level isolation for child processes started by `picoclaw`.

It does not sandbox the main `picoclaw` process itself.

## Scope

The current scope is the child-process startup path:

- `exec` tool
- CLI providers such as `claude-cli` and `codex-cli`
- process hooks
- MCP `stdio` servers

## One-Sentence Model

- The `picoclaw` main process still runs in the host environment.
- Every child process should enter the shared `pkg/isolation` startup path first.
- The startup path applies platform-specific isolation according to config.

## Architecture

The implementation has four layers:

1. Configuration layer: reads `config.Config.Isolation` and injects it through `isolation.Configure(cfg)`.
2. Instance layout layer: resolves `config.GetHome()`, prepares instance directories, and builds the runtime user environment.
3. Platform backend layer: Linux uses `bwrap`; macOS uses Seatbelt through `sandbox-exec`; Windows uses a restricted token, low integrity, and a `Job Object`.
4. Unified startup layer: `PrepareCommand(cmd)`, `Start(cmd)`, and `Run(cmd)`.

All integrations that spawn subprocesses should reuse these helpers instead of calling `cmd.Start` or `cmd.Run` directly.

Covered spawn sites: the `exec` tool, CLI providers (`claude-cli`, `codex-cli`), process hooks, MCP `stdio` servers, the Delta Chat RPC server, and the Weixin SILK voice decoder.

Known exception: the Linux USB device monitor spawns `udevadm` directly (it only observes host devices and is out of the child-process isolation scope).

`PrepareCommand` runs `Preflight` at most once per configuration generation; calling `isolation.Configure` again invalidates the cached result.

## Configuration

Isolation lives under:

```json
{
  "isolation": {
    "enabled": false,
    "expose_paths": [],
    "block_network": false
  }
}
```

Field meanings:

- `enabled`: enables or disables subprocess isolation. Default: `false`.
- `expose_paths`: explicitly exposes host paths inside the isolated environment. It only matters when `enabled=true`. Linux supports path remapping; macOS supports same-path access only.
- `block_network`: drops child processes into an isolated network namespace so they cannot reach loopback services, cloud-metadata endpoints (e.g. `169.254.169.254`), sidecar daemons, or other host-local services. Default: `false` (host network shared). **Linux only**; preflight returns an error on macOS and Windows.

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

### `block_network` security note

By default (`block_network: false`) child processes share the host network namespace. This means loopback
services on the host, cloud-instance metadata endpoints such as `http://169.254.169.254/`, sidecar
daemons, and local databases are reachable from inside the sandbox. A sandboxed process can exfiltrate
data with any network-capable runtime (`curl`, `python`, `node`, etc.); the exec tool's command-level
guards are not a sufficient network boundary.

Enabling `block_network: true` passes `--unshare-net` to bubblewrap, placing child processes in a fresh
network namespace that contains only a loopback interface (`lo`). Connections to host services or the
internet fail with "connection refused" / "network unreachable".

**What breaks when `block_network: true`:**

- `curl`, `wget`, or any HTTP client inside the sandbox.
- `git fetch`/`git clone`/`git push` over the network.
- Package managers (`pip`, `npm`, `apt`, etc.) that download packages.
- MCP servers that themselves need internet access.
- Any agent tool that relies on outbound network calls from child processes.

**Alternative:** if agents need a filtered outbound internet but must not reach host-local services,
consider running an egress proxy and routing traffic through it rather than using `block_network`.

Rules for `expose_paths`:

- `source` is a host path.
- `target` is the path inside the isolated environment.
- `mode` must be `ro` or `rw`.
- When `target` is empty, it defaults to `source`.
- Only one final rule may exist for the same `target`.
- Later-loaded config overrides earlier rules for the same `target`.

Platform note:

- Linux uses a real `source -> target` mount view.
- Linux probes bubblewrap namespace flags once at startup. The PID namespace (`--unshare-pid`) is required: if the probe fails (for example, in a Docker container whose `/proc` is masked by default), startup fails with a clear error naming the fix (`--security-opt systempaths=unconfined` for Docker, `procMount: Unmasked` for Kubernetes). To opt out entirely, set `"isolation.enabled": false in config.json` (risk: no filesystem isolation). Optional flags such as `--unshare-ipc` are dropped with a warning when the kernel rejects them with `Creating new namespace failed` (EINVAL) while mount namespace, filesystem view, and user environment remain active.
- macOS requires `target` to match `source`; Seatbelt does not remap paths.
  Read-only rules under the instance root are rejected because Seatbelt cannot
  revoke the root's write access.
- Windows does not currently support `expose_paths`.

## Instance Root And Directories

The instance root follows `config.GetHome()`:

- If `PICOCLAW_HOME` is set, use it.
- Otherwise use the default `.picoclaw` directory under the user home.

If `config.GetHome()` falls back to `.` while isolation is enabled, startup should fail.

Default instance directories include:

- instance root
- `skills`
- `logs`
- `cache`
- `state`
- `runtime-user-env`

`workspace` is derived from `cfg.WorkspacePath()` when configured, otherwise from the default workspace rule.

Windows also prepares:

- `runtime-user-env/AppData/Roaming`
- `runtime-user-env/AppData/Local`

## User Environment Redirect

When isolation is enabled, child processes receive a redirected per-instance user environment.

Linux and macOS variables:

- `HOME`
- `TMPDIR`
- `XDG_CONFIG_HOME`
- `XDG_CACHE_HOME`
- `XDG_STATE_HOME`

Windows variables:

- `USERPROFILE`
- `HOME`
- `TEMP`
- `TMP`
- `APPDATA`
- `LOCALAPPDATA`

These paths point into `runtime-user-env` under the instance root.

## Platform Behavior

### Linux

The Linux backend currently depends on `bwrap` (`bubblewrap`).

Capabilities:

- minimal filesystem view
- `pid` namespace isolation (required; sandbox has its own PID table with `/proc` only describing its own processes)
- `ipc` namespace isolation
- redirected child-process user environment
- `source -> target` read-only or read-write mounts
- all Linux capabilities dropped (`--cap-drop ALL`) inside every sandbox
- new session (`--new-session`): the sandbox runs in a detached session with no controlling terminal, blocking TIOCSTI-based terminal injection (CVE-2017-5226)
- optional network namespace isolation (`block_network: true`)

**Do not run PicoClaw as root.** When the effective uid is 0, bwrap does not
create a user namespace automatically, so the sandboxed process would otherwise
retain the full host capability set even after `--cap-drop`. PicoClaw works
around this by passing `--unshare-user` for root callers, which creates a new
user namespace where capabilities are reset to zero. Running as root still
leaves a wider attack surface than a dedicated non-privileged user.

Default mounts inside the instance root are intentionally narrow:

| Path | Mode | Purpose |
|------|------|---------|
| `<root>/workspace` | rw | agent working area |
| `<root>/runtime-user-env` | rw | redirected `HOME`, `TMPDIR`, XDG dirs |
| `<root>/skills` | ro | skill scripts the sandbox may execute |

`config.json` (API keys), `.security.yml`, `logs`, `cache`, `state`, and all other instance-root files are **not mounted**. A sandboxed command cannot read or modify the host configuration.

Minimum runtime system paths (`/usr`, `/bin`, `/lib`, `/lib64`, `/etc/resolv.conf`, etc.) are mounted read-only.

At runtime, PicoClaw also adds the executable path, its directory, the effective working directory, and absolute path arguments when needed.

Absolute path arguments are auto-exposed with the following rules:

- An argument is treated as a path only when it is a plain absolute token with no whitespace, control characters, or shell metacharacters (`;`, `&`, `|`, `<`, `>`, `(`, `)`, `$`, backtick, quotes, `\`, `*`, `?`, `[`, `]`, `{`, `}`). Shell command strings passed to `sh -c` are therefore never treated as paths.
- Paths under `/proc`, `/sys`, and `/dev` are never auto-mounted from the host. The sandbox provides its own `/proc` and `/dev`, and host pseudo-filesystem state is not exposed.
- Existing paths are exposed **read-only**. Directories passed on the command line do not become writable holes in the sandbox; writable locations come from `expose_paths` or the effective working directory.
- Non-existent paths expose their parent directory **read-write** so the command can create output files. This supports tools such as the Weixin SILK voice decoder that write to a not-yet-existing output path.
- Broad paths are **refused** automatically: `/`, any path directly under `/` (e.g. `/home`, `/etc`, `/tmp`, `/proc`), the invoking user's home directory, and every ancestor of the home directory. A single warning is logged per distinct refused path. Use `isolation.expose_paths` in `config.json` to grant access to these paths explicitly when needed.
- If the mount plan already has a rule that covers the path (an exact match or an ancestor), no additional rule is added, preventing conflicting mode stacking.

**Re-exposing the instance root.** An operator who needs the full root visible can add an `expose_paths` rule whose target is the instance root:

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

That rule merges in after the defaults and restores the full root view. The sandbox boundary notice in the agent system prompt will reflect the change (`PicoClaw instance root: … writable`).

**`PICOCLAW_CONFIG` environment variable.** When isolation is enabled on Linux, `PICOCLAW_CONFIG` is not passed to `exec` child processes because `config.json` is not mounted and the path would point to a non-existent file. `PICOCLAW_HOME` is still set.

**Spawn sites that need extra expose_paths on Linux.** Some channels and hooks store data under the instance root and require an explicit `expose_paths` rule when isolation is enabled:

| Spawn site | Path needed | Suggested rule |
|-----------|-------------|----------------|
| Delta Chat RPC server | `~/.picoclaw/deltachat/<channel>` (rw) | `expose_paths: [{source: "...", target: "...", mode: "rw"}]` |
| Process hooks with script under instance root | hook script directory (ro or rw as needed) | add the hook directory to `expose_paths` |
| Plugin MCP stdio servers with working directory under instance root | the plugin directory (ro) | add to `expose_paths` |

Weixin SILK voice decoder uses the OS temp dir (`/tmp`), not the instance root; no extra rule is needed.

There is no automatic fallback when `bwrap` is missing.

Install examples:

- `apt install bubblewrap`
- `dnf install bubblewrap`
- `yum install bubblewrap`
- `pacman -S bubblewrap`
- `apk add bubblewrap`

#### Supported bubblewrap versions

| Version range       | Support status                                                                      |
|---------------------|-------------------------------------------------------------------------------------|
| < 0.3.3             | **Refused at startup.** 0.1.x and 0.2.x lack the `--` argument separator PicoClaw always passes; they fail with an obscure `Unknown option --` error. |
| >= 0.3.3, < 0.12.0  | Accepted; a one-time warning is logged. CVE-2026-87766 (symlink traversal during sandbox setup can create files outside the sandbox) is not fixed. Distro packages may carry a backport the version string cannot show; upgrading bubblewrap or using such a distro build is recommended. |
| >= 0.12.0           | Fully supported and recommended. CVE-2026-87766 is fixed.                           |

Setuid bubblewrap binaries are **always refused** regardless of version. The setuid execution model is the attack surface of privilege-escalation CVEs such as CVE-2026-41163. bubblewrap 0.12+ no longer uses setuid; remove the setuid bit or install a 0.12+ package.

If isolation must be disabled temporarily:

```json
{
  "isolation": {
    "enabled": false
  }
}
```

Disabling isolation increases the risk that child processes can access or modify more host files.

### Windows

Windows isolation currently supports process-level restrictions such as restricted tokens, low integrity, job objects, and redirected user-environment directories.

`expose_paths` is not currently supported on Windows. If it is configured, startup should fail instead of pretending the paths were exposed.

The Windows backend currently uses:

- a restricted primary token
- low integrity level
- a `Job Object`
- redirected child-process user environment

It does not currently implement true `source -> target` filesystem remapping.

### macOS

The macOS backend uses `/usr/bin/sandbox-exec` with a Seatbelt default-deny profile.
It allows writes under the PicoClaw instance root and explicit `rw` paths, while
exposing system files, the command, its configured working directory, and
absolute path arguments as needed. A read-only `expose_paths` rule under the
instance root is rejected: Seatbelt rules are additive, so a later read-only
allow cannot revoke the root's write access. Network access remains available,
matching Linux behavior.
Because Apple deprecated `sandbox-exec`, macOS may remove it in a future release;
when it is missing, isolation fails closed.

### Other Platforms

When isolation is explicitly enabled on an unsupported platform, the higher-level runtime should surface that as an unsupported configuration instead of pretending isolation succeeded.

## Logging And Debugging

When isolation is enabled, PicoClaw logs the generated isolation plan.

Linux log name:

- `linux isolation mount plan`

Windows log name:

- `windows isolation access rules`

macOS uses the `macOS Seatbelt profile prepared` debug log.

If you suspect isolation is ineffective, check whether unexpected host paths appear in those logs.

## Relationship To `restrict_to_workspace`

- `restrict_to_workspace` limits the paths an agent is normally allowed to access.
- `pkg/isolation` limits what a child process can see and where its user environment points.

They complement each other and do not replace each other.

## Prompt Boundary Notice

When isolation is enabled and supported, the agent system prompt includes a
`Sandbox Boundaries` section. It is generated from `DescribeBoundary` when the
agent is created and lists:

- the workspace and the PicoClaw instance root;
- the scratch directory (`$TMPDIR`);
- user-configured `expose_paths`, split into writable and read-only targets;
- whether subprocess network access remains available.

The notice also tells the model that a `Read-only file system`, missing path, or
permission error at the boundary is not a broken host. The model should report
it and ask the user to add the path to `isolation.expose_paths` instead of
retrying with `sudo`, another absolute path, or another tool.

The notice describes child-process isolation only. File tools follow
`restrict_to_workspace` and `allow_read_outside_workspace` independently. On
Windows the notice has no path list because that backend applies process-level
restrictions without path-level filesystem remapping.

Config reload rebuilds the agent registry, so a changed isolation configuration
is reflected in the next system prompt.

## Current Limits

- Linux isolation is implemented with `bwrap`, not a custom in-process isolation runtime.
- Linux enables a dedicated `pid` namespace by default; Docker containers must add `--security-opt systempaths=unconfined`.
- Windows does not yet implement full host ACL enforcement for every allowed or denied path.
- macOS Seatbelt does not support `source -> target` path remapping and relies on Apple's deprecated `sandbox-exec` command.
- The current design isolates child processes, not the main `picoclaw` process.

## Suggested Reading Order

If you are new to this code, read it in this order:

1. `pkg/config/config.go`
2. `pkg/isolation/runtime.go`
3. `pkg/isolation/platform_linux.go`
4. `pkg/isolation/platform_windows.go`
5. Call sites:
6. `pkg/tools/shell.go`
7. `pkg/providers/*.go`
8. `pkg/agent/hook_process.go`
9. `pkg/mcp/manager.go`

That path gives the fastest overview of the configuration model, runtime flow, and platform-specific limits.
