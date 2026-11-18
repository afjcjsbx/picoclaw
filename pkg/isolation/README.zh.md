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
3. 平台后端层：Linux 使用 `bwrap`；macOS 通过 `sandbox-exec` 使用 Seatbelt；Windows 使用受限 token、低完整性级别和 `Job Object`。可选的 `docker` 后端在 Docker CLI 和 daemon 可用的平台上把子进程放进容器运行。
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
    }
  }
}
```

字段说明：

- `enabled`：是否启用子进程隔离。默认值：`false`。
- `backend`：`auto`（默认）、`bubblewrap`、`seatbelt`、`windows` 或 `docker`。`auto` 使用当前平台的本地后端；显式后端必须被当前平台支持，`docker` 在 Docker daemon 可用的平台上都可用。
- `expose_paths`：显式把宿主路径带入隔离环境。仅在 `enabled=true` 时生效。Linux 和 Docker 支持路径重映射；macOS 仅支持源路径和目标路径相同。
- `docker`：Docker 后端设置，仅在 `backend=docker` 时生效。`docker.network` 留空使用 Docker 默认网络（启用网络）；设为 `none` 可禁用容器网络。

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

### 其他平台

显式配置 `backend: "docker"` 时，只要 Docker 可用，其他平台也可以使用。原生隔离仍然不支持，并会在 `auto` 或显式选择时失败关闭。

## 日志与排障

隔离开启后，PicoClaw 会打印生成后的隔离计划，便于排障。

Linux 日志名：

- `linux isolation mount plan`

Windows 日志名：

- `windows isolation access rules`

macOS 调试日志名：`macOS Seatbelt profile prepared`。

Docker 使用 `docker isolation container plan` 和 `docker isolation container started` 调试日志，包含容器名、镜像、网络和 bind mount。

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
- 当前隔离的是子进程，不是 `picoclaw` 主进程自身。

## 建议阅读顺序

如果你是第一次看这部分代码，建议按这个顺序阅读：

1. `pkg/config/config.go`
2. `pkg/isolation/runtime.go`
3. `pkg/isolation/platform_linux.go`
4. `pkg/isolation/platform_windows.go`
5. 调用点：
6. `pkg/tools/shell.go`
7. `pkg/providers/*.go`
8. `pkg/agent/hook_process.go`
9. `pkg/mcp/manager.go`

这样能最快建立对配置模型、运行流程和平台边界的整体理解。
