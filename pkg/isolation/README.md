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
3. Platform backend layer: Linux uses `bwrap`; macOS uses Seatbelt through `sandbox-exec`; Windows uses a restricted token, low integrity, and a `Job Object`. The opt-in `docker` backend runs each child process in a container on any platform where the Docker CLI and daemon are available. The opt-in `ssh` backend runs each child process on a remote host through the OpenSSH client.
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
    "backend": "auto",
    "expose_paths": [],
    "docker": {
      "image": "",
      "network": "",
      "user": "",
      "pull": "missing",
      "extra_args": []
    },
    "ssh": {
      "host": "",
      "user": "",
      "port": 22,
      "key_path": "",
      "remote_root": "",
      "forward_env": [],
      "extra_args": []
    }
  }
}
```

Field meanings:

- `enabled`: enables or disables subprocess isolation. Default: `false`.
- `backend`: `auto` (default), `bubblewrap`, `seatbelt`, `windows`, `docker` or `ssh`. `auto` keeps the native backend for the current platform. An explicit backend must be supported by that platform; `docker` is available on every platform with a reachable Docker daemon. `ssh` requires a POSIX remote host and is not available on a local Windows PicoClaw because the exec tool generates PowerShell there.
- `expose_paths`: explicitly exposes host paths inside the isolated environment. It only matters when `enabled=true`. Linux and Docker support path remapping; macOS supports same-path access only. SSH rejects `expose_paths` because the remote account already sees its own filesystem.
- `docker`: Docker backend settings. It only matters when `backend` is `docker`. `docker.network` empty uses Docker's default network (enabled); `none` disables container networking.
- `ssh`: SSH backend settings. It only matters when `backend` is `ssh`. `ssh.host` is required; `ssh.remote_root` maps the local instance root to a remote directory; `ssh.forward_env` adds environment names to `SendEnv`.

Docker example:

```json
{
  "isolation": {
    "enabled": true,
    "backend": "docker",
    "docker": {
      "image": "afjcjsbx/picoclaw-isolation:latest",
      "pull": "missing"
    },
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

SSH example:

```json
{
  "isolation": {
    "enabled": true,
    "backend": "ssh",
    "ssh": {
      "host": "sandbox.internal",
      "user": "worker",
      "port": 22,
      "key_path": "~/.ssh/id_ed25519",
      "remote_root": "/srv/picoclaw",
      "forward_env": ["GOPATH"],
      "extra_args": ["-o", "ProxyJump=bastion"]
    }
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
- Docker binds `source` to `target` with the requested `ro`/`rw` mode. The
  host instance root is always mounted at the same path.
- SSH rejects `expose_paths`. The remote account is the boundary, so there is
  no local path to mount or remap.

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

Default mounts include the instance root **read-only**, the workspace and `runtime-user-env` read-write, an ephemeral tmpfs on `/tmp`, and the minimum runtime system paths such as `/usr`, `/bin`, `/lib`, `/lib64`, and `/etc/resolv.conf` read-only. Config, state, skills and logs therefore stay read-only even if an interpreter runs inside the sandbox.

At runtime, PicoClaw also adds the executable path, its directory, the effective working directory, and absolute path arguments when needed. The working directory never grants write access by itself: it is read-write only when it is already inside the workspace or an explicit `rw` expose path.

Absolute path arguments are exposed **read-only** when they exist. A non-existent path argument does not grant its parent read-write access; callers that need to create files outside the workspace must add an explicit `rw` `expose_paths` entry.

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
It exposes the instance root read-only and allows writes only under the resolved
workspace, `runtime-user-env`, and explicit `rw` paths, while exposing system
files, the command, its configured working directory (read-write only when it is
already inside the workspace or an `rw` expose path), and absolute path
arguments as needed. Network access remains available, matching Linux behavior.
Because Apple deprecated `sandbox-exec`, macOS may remove it in a future release;
when it is missing, isolation fails closed.

### Docker

The Docker backend starts every child process with `docker run --rm --init` and
overrides the image entrypoint with the original command. It is available on
Linux, macOS and Windows when the Docker CLI is on `PATH` and the daemon is
reachable; preflight runs `docker info` once per configuration generation and
fails closed when either is missing.

Behavior:

- The image must provide the command and its runtime dependencies. The `exec`
  tool always runs a POSIX shell (`sh -c`), so the image must contain `/bin/sh`;
  other integrations must use an image that contains their executable.
- The instance root is bind mounted **read-only** at the same absolute path.
  The workspace and `runtime-user-env` are mounted read-write, and
  `expose_paths` rules are added as bind mounts; `ro` maps to a read-only bind.
  The effective working directory is mounted read-write only when it is inside
  the workspace or an explicit `rw` expose path; other working directories are
  read-only. Absolute path arguments are exposed read-only, and a non-existent
  path does not grant its parent write access.
- The same absolute host path is used inside the container. This suits Linux,
  macOS and Windows containers; Docker Desktop Linux containers on Windows do
  not translate Windows drive paths automatically.
- Bind mounts are resolved by the Docker daemon, so it must run on the same
  host as PicoClaw. A remote `DOCKER_HOST` works only when the mounted paths
  exist on the remote machine.
- Containers run as the host UID/GID by default so files created in the
  workspace keep host ownership. Set `isolation.docker.user` to override it.
- The sanitized child environment is forwarded with `docker run -e NAME`;
  values are not repeated on the command line. `PATH` is intentionally not
  forwarded: the image's `ENV PATH` is used so tools resolve inside the
  container. Use `docker.extra_args` with `-e PATH=...` only when a custom
  container PATH is required.
- `isolation.docker.network` is passed to `--network` when set. An empty value
  keeps Docker's default network, so container networking is enabled by
  default. Use `none` only when network access must be disabled.
- `isolation.docker.pull` accepts `always`, `missing` (default) or `never`.
- `isolation.docker.extra_args` is trusted configuration inserted before the
  image. It can weaken isolation (for example `--privileged`), so keep it under
  the same review as `docker run` itself.
- Docker's `--rm` removes the container on normal exit. When a command is
  killed (foreground timeout or a tracked session kill), PicoClaw also runs
  `docker rm -f` for the container, because killing the docker CLI does not stop
  the container. An abrupt PicoClaw crash can still leave a container running;
  clean it up with `docker ps --filter label=picoclaw.isolation`.
- Interactive and PTY sessions use `docker run --interactive`; `--tty` is added
  when the child stdin is a terminal.

### Bundled Docker image

`docker/Dockerfile.isolation` builds the image used by the Docker backend. It
currently includes:

- Python 3.13
- Go 1.27.4 (`GOTOOLCHAIN=local`)
- `jq`, `ripgrep`, `fd`, `tmux`, `rsync`
- `git`, `make`, `curl` and CA certificates for module and package downloads

Release and nightly builds publish it next to the main PicoClaw images, using
the same version and `latest` tags:

- `ghcr.io/<owner>/picoclaw-isolation:<tag>`
- `docker.io/<DOCKERHUB_REPOSITORY>-isolation:<tag>`

For this repository that is `ghcr.io/afjcjsbx/picoclaw-isolation` and
`afjcjsbx/picoclaw-isolation`. Nightly builds use the `nightly` tag.

For local development, build it with:

```bash
make docker-isolation
```

The local tag is `picoclaw-isolation:1.27.4`; override it with
`make docker-isolation ISOLATION_IMAGE=my-registry/picoclaw-isolation:tag`.
Then point `isolation.docker.image` at the published or local tag.

### SSH

The SSH backend starts every child process through the OpenSSH client and runs
it on a remote POSIX host. It is available on Linux and macOS (and other Unix
hosts); local Windows is rejected because the exec tool generates PowerShell
commands there. The remote account is the isolation boundary: local files are
not mounted, and the remote host must provide `/bin/sh` for the exec tool, the
command binaries, and the working directory.

Behavior:

- `ssh.host` is required. `ssh.user` is optional; `user@host` works directly in
  `ssh.host` when `ssh.user` is empty. `ssh.port` defaults to `22`.
- Key or ssh-agent authentication is required. The client runs with
  `BatchMode=yes`, so password prompts cannot hang a tool call. `key_path`
  selects a key; otherwise the operator's `~/.ssh/config` and agent are used.
- Preflight runs one `true` command per configuration generation. It fails
  closed when the host is unreachable or authentication fails; reconfigure to
  retry after fixing the host.
- `ssh.remote_root` (absolute) maps the local PicoClaw instance root to a remote path.
  With the default `/home/worker/.picoclaw/workspace` and
  `remote_root=/srv/picoclaw`, commands run in `/srv/picoclaw/workspace`.
  Paths outside the instance root keep their absolute path and must exist on
  the remote host. `remote_root` must not be the remote filesystem root. The
  backend cannot change remote POSIX permissions: configure the remote account
  so `remote_root` and its config/state directories are read-only, and only the
  mapped workspace and `runtime-user-env` are writable.
- Commands are quoted for POSIX `sh` and run with `/bin/sh -c` on the remote
  host. Use workspace-relative paths in commands; absolute local paths inside a
  command string are not translated. The exec workspace guard still evaluates
  local paths, so remote absolute paths outside the local workspace may be
  rejected before the command reaches the host.
- The client enables SSH `ControlMaster` multiplexing with
  `ControlPersist=60` on Unix so repeated commands reuse one connection. This
  keeps one `ssh` process alive for up to 60 idle seconds; it is the main
  resource trade-off of the backend. Control sockets live in a private
  `picoclaw-ssh-<uid>` directory under `/tmp`.
- `ssh.forward_env` adds environment names to `SendEnv`. The defaults are
  non-secret locale and terminal variables. Values never appear in the local
  command line. The remote `sshd` must list each name or pattern in
  `AcceptEnv`; otherwise the remote process keeps its own value. Add proxy
  variables explicitly only when the remote host may receive their values.
  Avoid broad patterns such as `*` unless the remote `AcceptEnv` is equally
  restrictive.
- `ssh.extra_args` is trusted configuration. The backend rejects standalone short
  flags that replace the target or command, change the port/key/user, forward
  ports or the local agent, or force a tty. `-o` overrides remain trusted, as
  with any extra argument. The client also forces `ForwardAgent=no`,
  `ForwardX11=no`, `ClearAllForwardings=yes` and `PermitLocalCommand=no` so a
  user `ssh_config` cannot hand the remote host the local agent or a tunnel.
- Killing the local `ssh` client is best effort for the remote process. A
  remote command that detaches itself can outlive the client; remote background
  jobs are not tracked by PicoClaw.

File tools (`read_file`, `write_file`, the workspace tools, and skills) still
operate on the local filesystem. Files created by remote commands are not
visible locally unless both hosts share storage. The system prompt tells the
model about this split when the backend is active.

### Other Platforms

With an explicit `backend: "docker"`, other platforms are supported when Docker
is available. With `backend: "ssh"`, other Unix platforms are supported when
the OpenSSH client is available. Native isolation remains unsupported and fails
closed when selected through `auto`.

## Logging And Debugging

When isolation is enabled, PicoClaw logs the generated isolation plan.

Linux log name:

- `linux isolation mount plan`

Windows log name:

- `windows isolation access rules`

macOS uses the `macOS Seatbelt profile prepared` debug log.

Docker uses the `docker isolation container plan` and
`docker isolation container started` debug logs, which include the container
name, image, network and bind mounts.

SSH uses the `ssh isolation remote plan` debug log, which includes the target,
remote root, remote working directory, command path and the number of forwarded
environment names. Values are never logged.

If you suspect isolation is ineffective, check whether unexpected host paths appear in those logs.

## Relationship To `restrict_to_workspace`

- `restrict_to_workspace` limits the paths an agent is normally allowed to access.
- `pkg/isolation` limits what a child process can see and where its user environment points.

They complement each other and do not replace each other. Isolation applies to
child processes, not to the in-process file tools: with
`restrict_to_workspace=false`, `write_file`/`edit_file` can still modify files
the exec child cannot. Keep file-tool protected paths in mind when disabling the
workspace restriction. Conversely, `restrict_to_workspace=false` does not make
the exec child see more than the isolation mount plan exposes: the cwd never
grants write access by itself, and config/state stay read-only.

## Prompt Boundary Notice

When isolation is enabled and supported, the agent system prompt includes a
`Sandbox Boundaries` section. It is generated from `DescribeBoundary` when the
agent is created and lists:

- the workspace (writable) and the PicoClaw instance root (read-only);
- the scratch directory (`$TMPDIR`);
- user-configured `expose_paths`, split into writable and read-only targets;
- whether subprocess network access remains available.

The notice also tells the model that a `Read-only file system`, missing path, or
permission error at the boundary is not a broken host. The model should report
it and ask the user to add the path to `isolation.expose_paths` instead of
retrying with `sudo`, another absolute path, or another tool.

For the SSH backend the notice also states that child processes run on the
remote host, that file tools still operate locally, and that absolute local
paths are not translated; the model is asked to use workspace-relative paths.

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
- The Docker backend requires an image that contains the command, its runtime
  dependencies and a POSIX shell for the `exec` tool. It does not build or
  provision images.
- Docker's default network is preserved unless `isolation.docker.network` sets
  another mode; networking is enabled by default, so set `none` only when a
  workload must not have network access.
- The SSH backend requires a prepared POSIX remote host with the command
  binaries and mapped working directory. It does not sync files, so local file
  tools and remote commands see different filesystems unless storage is
  shared. Killing the local client does not reliably kill a detached remote
  process.
- SSH applies to every child-process spawn site. Integrations that exchange
  local files or stdio with a child process (for example voice decoding, some
  hooks and MCP servers) must be disabled or must run correctly on the remote
  host; the backend does not sync their files.
- The current design isolates child processes, not the main `picoclaw` process.

## Suggested Reading Order

If you are new to this code, read it in this order:

1. `pkg/config/config.go`
2. `pkg/isolation/runtime.go`
3. `pkg/isolation/backend.go`
4. `pkg/isolation/platform_linux.go`
5. `pkg/isolation/docker.go`
6. `pkg/isolation/ssh.go`
7. `pkg/isolation/platform_windows.go`
8. Call sites:
9. `pkg/tools/shell.go`
10. `pkg/providers/*.go`
11. `pkg/agent/hook_process.go`
12. `pkg/mcp/manager.go`

That path gives the fastest overview of the configuration model, runtime flow, and platform-specific limits.
