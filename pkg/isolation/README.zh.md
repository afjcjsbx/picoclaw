# `pkg/isolation`

`pkg/isolation` 为 `picoclaw` 启动的子进程提供进程级隔离能力。

它当前不会把 `picoclaw` 主进程自身放进沙箱中运行。

## 生效范围

当前生效范围是子进程启动链路：

- `exec` 工具
- `claude-cli`、`codex-cli` 等 CLI provider
- 进程型 hooks
- MCP `stdio` server

## 一句话理解

- `picoclaw` 主进程仍运行在宿主环境中。
- 所有子进程都应先经过 `pkg/isolation` 的统一启动入口。
- 入口会根据配置和平台，为子进程施加对应隔离。

## 架构

当前实现可以分为四层：

1. 配置层：读取 `config.Config.Isolation`，并通过 `isolation.Configure(cfg)` 注入运行时。
2. 实例目录层：解析 `config.GetHome()`，准备实例目录，并构建运行时用户环境目录。
3. 平台后端层：Linux 使用 `bwrap`；macOS 通过 `sandbox-exec` 使用 Seatbelt；Windows 使用受限 token、低完整性级别和 `Job Object`。可选的 `docker` 后端在 Docker CLI 和 daemon 可用的平台上把子进程放进容器运行；可选的 `ssh` 后端通过 OpenSSH 客户端把子进程放到远程主机运行。
4. 统一启动层：`PrepareCommand(cmd)`、`Start(cmd)`、`Run(cmd)`。

所有启动子进程的接入点都应复用这组入口，而不是各自直接调用 `cmd.Start` 或 `cmd.Run`。

## 配置

隔离配置位于：

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

字段说明：

- `enabled`：是否启用子进程隔离。默认值：`false`。
- `backend`：`auto`（默认）、`bubblewrap`、`seatbelt`、`windows`、`docker` 或 `ssh`。`auto` 使用当前平台的本地后端；显式后端必须被当前平台支持，`docker` 在 Docker daemon 可用的平台上都可用。`ssh` 需要 POSIX 远程主机；本地 Windows 不支持该后端，因为 exec 工具在那里生成 PowerShell 命令。
- `expose_paths`：显式把宿主路径带入隔离环境。仅在 `enabled=true` 时生效。Linux 和 Docker 支持路径重映射；macOS 仅支持源路径和目标路径相同。SSH 拒绝 `expose_paths`，因为远程账号本来就能看到自己的文件系统。
- `docker`：Docker 后端设置，仅在 `backend=docker` 时生效。`docker.network` 留空使用 Docker 默认网络（启用网络）；设为 `none` 可禁用容器网络。
- `ssh`：SSH 后端设置，仅在 `backend=ssh` 时生效。`ssh.host` 必填；`ssh.remote_root` 把本地实例根映射到远程目录；`ssh.forward_env` 增加通过 `SendEnv` 传递的环境变量名。

示例：

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

示例（SSH）：

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

`expose_paths` 规则：

- `source`：宿主机路径。
- `target`：隔离环境内的目标路径。
- `mode`：只能是 `ro` 或 `rw`。
- `target` 为空时，默认等于 `source`。
- 同一个 `target` 最终只能保留一条规则。
- 后加载的配置会覆盖先加载的同目标规则。

平台说明：

- Linux 会真实使用 `source -> target` 挂载视图。
- macOS 要求 `target` 与 `source` 相同；Seatbelt 不支持路径重映射。
- Windows 当前不支持 `expose_paths`。
- Docker 按 `ro`/`rw` 把 `source` 绑定到 `target`；实例根始终以相同路径挂载。
- SSH 拒绝 `expose_paths`。远程账号本身就是边界，没有本地路径需要挂载或重映射。

## 实例根与目录

实例根遵循 `config.GetHome()`：

- 如果设置了 `PICOCLAW_HOME`，使用该值。
- 否则默认使用用户目录下的 `.picoclaw`。

如果 `config.GetHome()` 在隔离开启时最终回退到当前目录 `.`，启动应直接失败。

默认实例目录包括：

- 实例根本身
- `skills`
- `logs`
- `cache`
- `state`
- `runtime-user-env`

`workspace` 优先使用 `cfg.WorkspacePath()` 的结果；未显式配置时才按默认规则派生。

Windows 还会额外准备：

- `runtime-user-env/AppData/Roaming`
- `runtime-user-env/AppData/Local`

## 用户环境重定向

隔离开启后，子进程会收到重定向到实例目录下的独立用户环境。

Linux 和 macOS 注入变量：

- `HOME`
- `TMPDIR`
- `XDG_CONFIG_HOME`
- `XDG_CACHE_HOME`
- `XDG_STATE_HOME`

Windows 注入变量：

- `USERPROFILE`
- `HOME`
- `TEMP`
- `TMP`
- `APPDATA`
- `LOCALAPPDATA`

这些路径都会指向实例根下的 `runtime-user-env`。

## 平台行为

### Linux

Linux 后端当前依赖 `bwrap`（`bubblewrap`）。

能力：

- 最小文件系统视图
- `ipc namespace`
- 子进程用户环境重定向
- `source -> target` 只读或读写挂载

默认映射包括实例根，以及 `/usr`、`/bin`、`/lib`、`/lib64`、`/etc/resolv.conf` 等最小运行时系统路径。

运行时还会按需补充可执行文件本身、其所在目录、生效后的工作目录，以及命令行中的绝对路径参数。

缺少 `bwrap` 时不会自动回退。

安装示例：

- `apt install bubblewrap`
- `dnf install bubblewrap`
- `yum install bubblewrap`
- `pacman -S bubblewrap`
- `apk add bubblewrap`

如果需要临时关闭隔离：

```json
{
  "isolation": {
    "enabled": false
  }
}
```

关闭隔离后，子进程访问或修改更多宿主文件的风险会明显上升。

### Windows

Windows 隔离当前提供的是进程级限制，例如 restricted token、low integrity、job object，以及用户环境目录重定向。

`expose_paths` 目前不支持 Windows。如果配置了该字段，启动应直接失败，而不是假装这些路径已经被暴露进隔离环境。

Windows 后端当前使用：

- 受限 primary token
- 低完整性级别
- `Job Object`
- 子进程用户环境重定向

它当前不会实现真正的 `source -> target` 文件系统重映射。

### macOS

macOS 后端通过 `/usr/bin/sandbox-exec` 使用默认拒绝的 Seatbelt 配置文件。
它允许在 PicoClaw 实例根目录和明确配置的 `rw` 路径下写入，并按需开放系统文件、命令、配置的工作目录和绝对路径参数。网络访问保持开启，与 Linux 行为一致。
Apple 已弃用 `sandbox-exec`，未来 macOS 版本可能移除它；如果该命令不存在，隔离启动会失败。

### Docker

Docker 后端通过 `docker run --rm --init` 启动每个子进程，并用原命令覆盖镜像的 entrypoint。只要 Docker CLI 在 `PATH` 中且 daemon 可访问，它在 Linux、macOS 和 Windows 上都可用；预检会执行一次 `docker info`，失败时直接拒绝启动。

行为：

- 镜像必须提供要执行的命令及其运行时依赖。`exec` 工具始终通过 POSIX shell（`sh -c`）运行，因此镜像必须包含 `/bin/sh`；其他接入点需要使用包含对应可执行文件的镜像。
- 实例根以相同绝对路径读写挂载。`expose_paths` 会转换为 bind mount，`ro` 对应只读挂载。生效的工作目录以读写方式挂载，绝对路径参数以只读方式暴露（输出路径不存在时改为挂载其父目录的读写）。
- 容器内使用相同的宿主绝对路径。这适用于 Linux、macOS 和 Windows 容器；Windows 上的 Docker Desktop Linux 容器不会自动转换 Windows 盘符路径。
- bind mount 由 Docker daemon 解析，因此 daemon 必须与 PicoClaw 运行在同一台主机上。远程 `DOCKER_HOST` 只有在远端存在对应路径时才可用。
- 容器默认以宿主 UID/GID 运行，保证工作区文件保持宿主所有权；可用 `isolation.docker.user` 覆盖。
- 经过清洗的子进程环境通过 `docker run -e NAME` 传入，不在命令行中重复值。`PATH` 不会传入：容器使用镜像的 `ENV PATH`，确保 Go、Python 等工具在容器内正确解析。只有确实需要自定义容器 PATH 时，才通过 `docker.extra_args` 传入 `-e PATH=...`。
- `isolation.docker.network` 会传给 `--network`；留空即使用 Docker 默认网络，因此容器网络默认启用。只有需要禁用网络时才设为 `none`。
- `isolation.docker.pull` 支持 `always`、`missing`（默认）和 `never`。
- `isolation.docker.extra_args` 是受信任配置，会插在镜像之前。它可以削弱隔离（例如 `--privileged`），应按 `docker run` 本身的标准审查。
- 正常退出时由 `--rm` 删除容器。命令被终止（前台超时或会话 kill）时，PicoClaw 还会执行 `docker rm -f`，因为杀掉 docker CLI 不会停止容器。PicoClaw 异常崩溃时仍可能残留容器，可用 `docker ps --filter label=picoclaw.isolation` 清理。
- 交互式和 PTY 会话使用 `docker run --interactive`；当子进程 stdin 是终端时追加 `--tty`。

### 内置 Docker 镜像

`docker/Dockerfile.isolation` 用于构建 Docker 后端使用的镜像，当前包含：

- Python 3.13
- Go 1.27.4（`GOTOOLCHAIN=local`）
- `jq`、`ripgrep`、`fd`、`tmux`、`rsync`
- `git`、`make`、`curl` 和 CA 证书，用于下载模块和包

发布和 nightly 构建会与 PicoClaw 主镜像一起发布该镜像，使用相同的版本标签和 `latest`：

- `ghcr.io/<owner>/picoclaw-isolation:<tag>`
- `docker.io/<DOCKERHUB_REPOSITORY>-isolation:<tag>`

本仓库对应 `ghcr.io/afjcjsbx/picoclaw-isolation` 和
`afjcjsbx/picoclaw-isolation`。nightly 构建使用 `nightly` 标签。

本地开发时构建：

```bash
make docker-isolation
```

本地标签为 `picoclaw-isolation:1.27.4`；可用
`make docker-isolation ISOLATION_IMAGE=my-registry/picoclaw-isolation:tag`
覆盖。然后将 `isolation.docker.image` 指向已发布或本地标签。

### SSH

SSH 后端通过 OpenSSH 客户端启动每个子进程，并在远程 POSIX 主机上执行。它支持 Linux、macOS 和其他 Unix 本机；本地 Windows 会被拒绝，因为那里的 exec 工具生成 PowerShell 命令。远程账号就是隔离边界：本地文件不会被挂载，远程主机必须提供 `/bin/sh`（exec 工具需要）、命令二进制以及映射后的工作目录。

行为：

- `ssh.host` 必填。`ssh.user` 可选；当 `ssh.user` 为空时，`ssh.host` 可以直接写成 `user@host`。`ssh.port` 默认 `22`。
- 必须使用密钥或 ssh-agent 认证。客户端以 `BatchMode=yes` 运行，密码提示不会挂住工具调用。`key_path` 指定密钥；否则使用操作者的 `~/.ssh/config` 和 agent。
- 预检在每次配置代际执行一次 `true` 命令。主机不可达或认证失败时直接失败关闭；修复后重新加载配置再试。
- `ssh.remote_root`（绝对路径）把本地 PicoClaw 实例根映射到远程路径。例如本地工作区是 `/home/worker/.picoclaw/workspace`，`remote_root=/srv/picoclaw` 时命令在 `/srv/picoclaw/workspace` 执行。实例根之外的路径保持绝对路径，并且必须在远程主机上存在。远程根必须提前创建且可写。
- 命令按 POSIX `sh` 规则引用，并作为 `/bin/sh -c` 在远程执行。命令中请使用相对于工作区的路径；命令字符串里的本地绝对路径不会自动转换。exec 的工作区守卫仍然按本地路径检查，因此本地工作区之外的远程绝对路径可能会在到达主机前被拒绝。
- Unix 上启用 SSH `ControlMaster` 多路复用，`ControlPersist=60`，重复命令复用同一连接。这会让一个 `ssh` 进程最多空闲存活 60 秒，是该后端的主要资源权衡。控制 socket 位于 `/tmp` 下私有的 `picoclaw-ssh-<uid>` 目录。
- `ssh.forward_env` 增加传给 `SendEnv` 的环境变量名。默认只包含非敏感的语言和终端变量。值不会出现在本地命令行。远程 `sshd` 必须在 `AcceptEnv` 中列出对应名称或模式，否则远程进程保留自己的值。只有确认远程主机可以接收代理变量值时才显式添加它们。除非远程 `AcceptEnv` 同样严格，否则不要使用 `*` 这类宽泛模式。
- `ssh.extra_args` 是受信任配置。后端会拒绝替换目标或命令、修改端口/密钥/用户、转发端口或本地 agent、强制 tty 的独立短参数；`-o` 覆盖与其他额外参数一样属于受信任配置。客户端还会强制 `ForwardAgent=no`、`ForwardX11=no`、`ClearAllForwardings=yes` 和 `PermitLocalCommand=no`，避免用户的 `ssh_config` 把本地 agent 或隧道交给远程主机。
- 杀掉本地 `ssh` 客户端对远程进程只是尽力而为。自行脱离的远程命令可能继续运行；PicoClaw 不跟踪远程后台任务。

文件工具（`read_file`、`write_file`、工作区工具和 skills）仍然操作本地文件系统。除非两台主机共享存储，远程命令创建的文件在本地不可见。后端启用时，系统提示会告知模型这种分工。

### 其他平台

显式配置 `backend: "docker"` 时，只要 Docker 可用，其他平台也可以使用。显式配置 `backend: "ssh"` 时，其他 Unix 平台只要 OpenSSH 客户端可用也可以使用。原生隔离仍然不支持，并会在 `auto` 或显式选择时失败关闭。

## 日志与排障

隔离开启后，PicoClaw 会打印生成后的隔离计划，便于排障。

Linux 日志名：

- `linux isolation mount plan`

Windows 日志名：

- `windows isolation access rules`

macOS 调试日志名：`macOS Seatbelt profile prepared`。

Docker 使用 `docker isolation container plan` 和 `docker isolation container started` 调试日志，包含容器名、镜像、网络和 bind mount。

SSH 使用 `ssh isolation remote plan` 调试日志，包含目标、远程根、远程工作目录、命令路径和转发的环境变量名数量；不会记录变量值。

如果你怀疑隔离未生效，先检查这些日志里是否出现了不应暴露的宿主路径。

## 与 `restrict_to_workspace` 的关系

- `restrict_to_workspace` 限制的是 agent 默认可访问的路径。
- `pkg/isolation` 限制的是子进程运行时能看到什么文件系统，以及它的用户环境指向哪里。

两者互补，不互相替代。

## 当前限制

- Linux 基于 `bwrap` 实现，而不是纯内建 isolation runtime。
- Linux 当前没有默认启用独立的 `pid namespace`。
- Windows 还没有对所有允许/拒绝路径做完整 ACL 落地。
- macOS Seatbelt 不支持 `source -> target` 路径重映射，并依赖 Apple 已弃用的 `sandbox-exec` 命令。
- Docker 后端要求镜像自带命令、运行时依赖，以及 `exec` 工具需要的 POSIX shell；它不会构建或准备镜像。
- 除非 `isolation.docker.network` 指定其他模式，否则保留 Docker 默认网络；网络默认启用，只有工作负载必须禁止网络时才设为 `none`。
- SSH 后端需要预先准备好的 POSIX 远程主机，并包含命令二进制和映射后的工作目录。它不同步文件，因此除非共享存储，本地文件工具和远程命令看到的是不同文件系统。杀掉本地客户端不能可靠杀掉已脱离的远程进程。
- SSH 会应用于所有子进程启动点。与子进程交换本地文件或 stdio 的集成（例如语音解码、部分 hooks 和 MCP server）必须禁用，或者必须能在远程主机正确运行；该后端不同步它们的文件。
- 当前隔离的是子进程，不是 `picoclaw` 主进程自身。

## 建议阅读顺序

如果你是第一次看这部分代码，建议按这个顺序阅读：

1. `pkg/config/config.go`
2. `pkg/isolation/runtime.go`
3. `pkg/isolation/backend.go`
4. `pkg/isolation/platform_linux.go`
5. `pkg/isolation/docker.go`
6. `pkg/isolation/ssh.go`
7. `pkg/isolation/platform_windows.go`
8. 调用点：
9. `pkg/tools/shell.go`
10. `pkg/providers/*.go`
11. `pkg/agent/hook_process.go`
12. `pkg/mcp/manager.go`

这样能最快建立对配置模型、运行流程和平台边界的整体理解。
