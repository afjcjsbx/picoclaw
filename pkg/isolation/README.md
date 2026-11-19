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
    "expose_paths": []
  }
}
```

Field meanings:

- `enabled`: enables or disables subprocess isolation. Default: `false`.
- `expose_paths`: explicitly exposes host paths inside the isolated environment. It only matters when `enabled=true`. Linux supports path remapping; macOS supports same-path access only.

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

Rules for `expose_paths`:

- `source` is a host path.
- `target` is the path inside the isolated environment.
- `mode` must be `ro` or `rw`.
- When `target` is empty, it defaults to `source`.
- Only one final rule may exist for the same `target`.
- Later-loaded config overrides earlier rules for the same `target`.

Platform note:

- Linux uses a real `source -> target` mount view.
- Linux probes optional bubblewrap namespace flags once. If the kernel rejects one with `Creating new namespace failed` (EINVAL), that namespace isolation is dropped with a warning while the mount namespace, filesystem view, and user environment remain active. This weakens isolation for that namespace; other probe failures stop startup.
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
- `ipc` namespace isolation
- redirected child-process user environment
- `source -> target` read-only or read-write mounts

Default mounts include the instance root plus the minimum runtime system paths such as `/usr`, `/bin`, `/lib`, `/lib64`, and `/etc/resolv.conf`.

At runtime, PicoClaw also adds the executable path, its directory, the effective working directory, and absolute path arguments when needed.

Absolute path arguments are exposed **read-only**: a directory passed on the command line does not become a writable hole in the sandbox. Writable locations come from `expose_paths`, the effective working directory, or the parent of a non-existent path argument (so output files can still be created).

There is no automatic fallback when `bwrap` is missing.

Install examples:

- `apt install bubblewrap`
- `dnf install bubblewrap`
- `yum install bubblewrap`
- `pacman -S bubblewrap`
- `apk add bubblewrap`

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
- Linux does not currently enable a dedicated `pid` namespace by default.
- Windows does not yet implement full host ACL enforcement for every allowed or denied path.
- macOS Seatbelt does not support `source -> target` path remapping and relies on Apple's deprecated `sandbox-exec` command.
- The current design isolates child processes, not the main `picoclaw` process.

## Running Isolation Inside Containers

bubblewrap relies on Linux user namespaces to build its filesystem view.
Most container runtimes restrict or deny user namespace creation by default.
The table below shows what happens when PicoClaw runs inside a container with
`isolation.enabled = true`.

Each requirement is stated separately so the documentation stays correct after
planned changes land (PID namespace, seccomp denylist, `--cap-drop ALL`,
`--new-session`, opt-in `isolation.block_network`).

| Environment | Outcome (lab-verified unless noted) |
|---|---|
| **Docker, default seccomp, non-root** | `bwrap: No permissions to create a new namespace` — PicoClaw fails closed. Verified: `docker run -u bob picoclaw-bwrap-lab bwrap … → EPERM`. |
| **Docker, default seccomp, root** | `bwrap: Creating new namespace failed: Operation not permitted` — PicoClaw fails closed. Verified: `docker run -u root picoclaw-bwrap-lab bwrap … → EPERM`. |
| **Docker + `--security-opt seccomp=docs/isolation/seccomp-bwrap.json`, non-root** | Works. Verified in lab (see profile notes below). |
| **Docker + `--security-opt seccomp=unconfined`, non-root** | Works — but removes all syscall filtering for the container; see the trade-off note below. |
| **Docker + `--security-opt seccomp=unconfined`, root** | Still fails: `bwrap: Creating new namespace failed: Operation not permitted`. Root also needs `--cap-add SYS_ADMIN`. Verified in lab. |
| **Kubernetes `RuntimeDefault` seccomp profile** | Blocks user namespace creation; bwrap fails closed. A custom profile equivalent to `docs/isolation/seccomp-bwrap.json` is required. Kubernetes-specific securityContext requirements are listed below (untested — no Kubernetes cluster available in this lab). |
| **Podman rootless** | Podman allows user namespace creation by default (rootless mode implies a user namespace); bwrap typically works without seccomp changes. Not verified in this lab (Podman machine not running). |
| **Plain host (Linux, non-root, kernel ≥ 3.8 with unprivileged userns enabled)** | Works with no container overhead. |

### What bubblewrap needs from the container runtime

Each line is a separate requirement; adjust your deployment as individual PRs land:

**User namespace creation.** `bwrap` calls `clone(CLONE_NEWUSER | CLONE_NEWNS | …)`. Docker's default seccomp profile gates this on `CAP_SYS_ADMIN`, which a non-root container process does not have. Solution: use the custom profile at `docs/isolation/seccomp-bwrap.json` (adds an unconditional allow for the namespace and mount syscalls bwrap requires) or use `seccomp=unconfined`.

**Mount namespace operations.** After creating the user namespace, bwrap performs `mount(MS_SLAVE)`, bind-mounts, `pivot_root`, and `umount2`. These are also gated on `CAP_SYS_ADMIN` in the Docker default profile. The custom profile lifts that gate for exactly these syscalls.

**Fresh `/proc` inside a PID namespace (future change — `--unshare-pid`).** `bwrap --unshare-pid --proc /proc` mounts a new procfs. Docker masks `/proc` paths by default; the mount fails with `EPERM` even with the custom seccomp profile. Requires `--security-opt systempaths=unconfined`. Verified: without it, `bwrap: Can't mount proc on /proc: Operation not permitted`; with it, bwrap succeeds and shows a fresh PID 1.

**No `--cap-add SYS_ADMIN` required for non-root.** The custom profile makes the flag unnecessary for non-root. For root, `--cap-add SYS_ADMIN` is still required (but running PicoClaw as root inside a container is strongly discouraged; the sandbox provides little protection in that configuration).

### Custom Docker seccomp profile (`docs/isolation/seccomp-bwrap.json`)

The profile is Docker's default (fetched from `moby/profiles` on 2026-11-19) with two additions:

1. The `clone3` explicit-deny rule is removed (moby blocks it entirely; bwrap uses it on aarch64).
2. An unconditional `SCMP_ACT_ALLOW` rule is added for: `clone`, `clone3`, `unshare`, `setns`, `mount`, `mount_setattr`, `move_mount`, `umount`, `umount2`, `pivot_root`. These are the syscalls bwrap needs to set up its namespace and filesystem view that the moby default profile only allows when `CAP_SYS_ADMIN` is present.

**What the profile widens compared to Docker's default:**
- Non-root processes can create user namespaces (`clone(CLONE_NEWUSER)`).
- Non-root processes can call `mount`/`umount2`/`pivot_root` inside a user namespace.
- `unshare -U` outside of bwrap also succeeds; this is a known widening.

**What remains blocked:**
- `bpf`, `perf_event_open`, `ptrace`, `process_vm_readv/writev`, kernel module operations, raw I/O, and all other dangerous syscalls remain gated on their respective capabilities or the default deny action.

Usage:

```
docker run --security-opt seccomp=docs/isolation/seccomp-bwrap.json \
           --security-opt systempaths=unconfined \   # required only when --unshare-pid lands
           -u <non-root-uid> \
           <image> picoclaw …
```

### Kubernetes notes (untested — no cluster available in this lab)

The fields below are derived from the seccomp requirements above and standard Kubernetes documentation; they have not been verified against a real cluster. Adjust after testing.

```yaml
# In the Pod or container securityContext:
securityContext:
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  seccompProfile:
    type: Localhost
    localhostProfile: seccomp-bwrap.json  # deploy docs/isolation/seccomp-bwrap.json to the node
  # procMount: Unmasked    # required when --unshare-pid lands
```

Kubernetes `RuntimeDefault` blocks user namespace creation. `seccomp=Unconfined` removes all filtering and is not recommended. Use the custom profile.

### Trade-off notice

Disabling isolation (`isolation.enabled = false`) or using `seccomp=unconfined` removes the filesystem view that bubblewrap provides, increasing the risk that child processes can read or modify host files, environment variables containing secrets, and agent configuration.

**Recommendation:** use `docs/isolation/seccomp-bwrap.json` as the seccomp profile. It is narrower than `seccomp=unconfined` and was verified to allow bwrap to operate correctly.

Do not use `seccomp=unconfined` as a shortcut to make bwrap work. Use the custom profile instead; it removes only the syscall restrictions that bwrap actually needs and leaves dangerous syscalls blocked.

## Resource Limits

bubblewrap has no built-in resource-limit options. The flag `--rlimit-*` does not exist; it is not listed in `bwrap --help` and was confirmed absent in the lab. Resource limits must come from the deployment layer.

### Why per-process rlimits are insufficient for sandboxing

`RLIMIT_NPROC` counts all processes of the same real uid on the host, including the main PicoClaw process and any other concurrent sandboxed commands. Setting it inside the sandbox via `setrlimit` would apply the limit to the entire uid, not just the child command. `RLIMIT_AS` (address space) breaks V8, the JVM, and the Go runtime, which map large virtual address ranges at startup. Neither rlimit provides a reliable per-sandbox process cap.

### Deployment-level limits (recommended)

Apply limits at the deployment layer, not inside PicoClaw:

**Docker:**

```
docker run --pids-limit 64 --memory 512m --cpus 1.0 …
```

`--pids-limit` is enforced via cgroup and is visible inside the sandbox as `/sys/fs/cgroup/pids.max`. Verified in lab: `docker run --pids-limit 20 … → cat /sys/fs/cgroup/pids.max → 20`.

**Kubernetes:**

```yaml
resources:
  limits:
    memory: "512Mi"
    cpu: "1"
  # pids limit via Pod-level spec.os.linux.pidLimit (Kubernetes ≥ 1.27)
```

**systemd (host deployment):**

```
# In the unit file running picoclaw:
TasksMax=64
MemoryMax=512M
CPUQuota=100%
```

### Per-command limit

The only per-command resource limit PicoClaw applies directly is the execution timeout and process-tree kill in the `exec` tool. All other limits must come from the deployment.

A safe demonstration (bounded, no fork bomb risk):

```
docker run --pids-limit 64 --security-opt seccomp=docs/isolation/seccomp-bwrap.json \
  -u bob picoclaw-bwrap-lab \
  bwrap --ro-bind / / --dev /dev --proc /proc --unshare-ipc --die-with-parent -- \
  sh -c 'for i in $(seq 1 70); do sleep 60 & done; wait'
# Processes beyond the cgroup limit are rejected with EAGAIN; the command
# fails but does not exhaust the host.
```

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
