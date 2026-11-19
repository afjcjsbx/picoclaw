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
3. 平台后端层：Linux 使用 `bwrap`；macOS 通过 `sandbox-exec` 使用 Seatbelt；Windows 使用受限 token、低完整性级别和 `Job Object`。
4. 统一启动层：`PrepareCommand(cmd)`、`Start(cmd)`、`Run(cmd)`。

所有启动子进程的接入点都应复用这组入口，而不是各自直接调用 `cmd.Start` 或 `cmd.Run`。

## 配置

隔离配置位于：

```json
{
  "isolation": {
    "enabled": false,
    "expose_paths": []
  }
}
```

字段说明：

- `enabled`：是否启用子进程隔离。默认值：`false`。
- `expose_paths`：显式把宿主路径带入隔离环境。仅在 `enabled=true` 时生效。Linux 支持路径重映射；macOS 仅支持源路径和目标路径相同。

示例：

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

### 其他平台

当在未支持的平台上显式开启隔离时，上层运行时应将其视为不支持的配置，而不是假装隔离成功。

## 日志与排障

隔离开启后，PicoClaw 会打印生成后的隔离计划，便于排障。

Linux 日志名：

- `linux isolation mount plan`

Windows 日志名：

- `windows isolation access rules`

macOS 调试日志名：`macOS Seatbelt profile prepared`。

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
- 当前隔离的是子进程，不是 `picoclaw` 主进程自身。

## 在容器内运行隔离

bubblewrap 通过 Linux user namespace 构建其文件系统视图。大多数容器运行时默认会限制或拒绝 user namespace 的创建。下表展示了在 `isolation.enabled = true` 时，PicoClaw 在容器内运行的结果。

每条要求单独列出，以便后续变更（PID namespace、seccomp 拒绝列表、`--cap-drop ALL`、`--new-session`、可选的 `isolation.block_network`）落地后，文档仍保持准确。

| 环境 | 结果（实验室验证，另有说明除外） |
|---|---|
| **Docker，默认 seccomp，非 root** | `bwrap: No permissions to create a new namespace` — PicoClaw 失败关闭。已验证：`docker run -u bob picoclaw-bwrap-lab bwrap … → EPERM`。 |
| **Docker，默认 seccomp，root** | `bwrap: Creating new namespace failed: Operation not permitted` — PicoClaw 失败关闭。已验证：`docker run -u root picoclaw-bwrap-lab bwrap … → EPERM`。 |
| **Docker + `--security-opt seccomp=docs/isolation/seccomp-bwrap.json`，非 root** | 可用。已在实验室验证（见下方 profile 说明）。 |
| **Docker + `--security-opt seccomp=unconfined`，非 root** | 可用——但会移除容器的所有 syscall 过滤；见下方权衡说明。 |
| **Docker + `--security-opt seccomp=unconfined`，root** | 仍然失败：`bwrap: Creating new namespace failed: Operation not permitted`。root 还需要 `--cap-add SYS_ADMIN`。已在实验室验证。 |
| **Kubernetes `RuntimeDefault` seccomp profile** | 阻止 user namespace 创建；bwrap 失败关闭。需要等效于 `docs/isolation/seccomp-bwrap.json` 的自定义 profile。Kubernetes securityContext 要求见下方（未在此实验室验证——无可用 Kubernetes 集群）。 |
| **Podman rootless** | Podman 默认允许 user namespace 创建（rootless 模式隐含 user namespace）；bwrap 通常无需修改 seccomp 即可运行。此实验室未验证（Podman machine 未运行）。 |
| **普通宿主机（Linux，非 root，内核 ≥ 3.8，启用非特权 userns）** | 无需容器，直接可用。 |

### bubblewrap 对容器运行时的要求

每行是一个独立要求；随各 PR 落地逐条调整：

**User namespace 创建。** `bwrap` 调用 `clone(CLONE_NEWUSER | CLONE_NEWNS | …)`。Docker 默认 seccomp profile 将此操作限定在具有 `CAP_SYS_ADMIN` 的进程，而非 root 容器进程没有该权限。解决方案：使用 `docs/isolation/seccomp-bwrap.json` 自定义 profile（为 bwrap 所需的 namespace 和挂载 syscall 添加无条件 allow），或使用 `seccomp=unconfined`。

**Mount namespace 操作。** 创建 user namespace 后，bwrap 执行 `mount(MS_SLAVE)`、bind mount、`pivot_root` 和 `umount2`。这些操作在 Docker 默认 profile 中同样限定于 `CAP_SYS_ADMIN`。自定义 profile 仅对这些 syscall 解除该限制。

**PID namespace 内的新 `/proc`（未来变更——`--unshare-pid`）。** `bwrap --unshare-pid --proc /proc` 会挂载新的 procfs。Docker 默认会遮蔽 `/proc` 路径；即使使用自定义 seccomp profile，挂载仍以 `EPERM` 失败。需要 `--security-opt systempaths=unconfined`。已验证：不加则 `bwrap: Can't mount proc on /proc: Operation not permitted`；加上则 bwrap 成功并显示全新的 PID 1。

**非 root 无需 `--cap-add SYS_ADMIN`。** 自定义 profile 使非 root 无需该 flag。root 仍需 `--cap-add SYS_ADMIN`（但强烈不建议在容器内以 root 运行 PicoClaw；该配置下沙箱几乎不提供保护）。

### 自定义 Docker seccomp profile（`docs/isolation/seccomp-bwrap.json`）

该 profile 基于 Docker 默认 profile（2026-11-19 从 `moby/profiles` 获取），新增两处修改：

1. 移除 `clone3` 的显式 DENY 规则（moby 完全阻止它；bwrap 在 aarch64 上需要使用它）。
2. 新增无条件 `SCMP_ACT_ALLOW` 规则，覆盖：`clone`、`clone3`、`unshare`、`setns`、`mount`、`mount_setattr`、`move_mount`、`umount`、`umount2`、`pivot_root`。这些是 bwrap 建立 namespace 和文件系统视图所需的 syscall，在 moby 默认 profile 中仅当进程具有 `CAP_SYS_ADMIN` 时才被允许。

**与 Docker 默认 profile 相比，此 profile 拓宽的范围：**
- 非 root 进程可创建 user namespace（`clone(CLONE_NEWUSER)`）。
- 非 root 进程可在 user namespace 内调用 `mount`/`umount2`/`pivot_root`。
- bwrap 外部的 `unshare -U` 也会成功；这是已知的范围拓宽。

**仍被阻止的操作：**
- `bpf`、`perf_event_open`、`ptrace`、`process_vm_readv/writev`、内核模块操作、裸 I/O 等危险 syscall，仍受其对应 capability 条件或默认拒绝动作的约束。

使用方式：

```
docker run --security-opt seccomp=docs/isolation/seccomp-bwrap.json \
           --security-opt systempaths=unconfined \   # 仅在 --unshare-pid 落地后需要
           -u <非 root uid> \
           <image> picoclaw …
```

### Kubernetes 说明（未测试——此实验室无可用集群）

以下字段根据上述 seccomp 要求及 Kubernetes 官方文档推导，未在真实集群验证。请测试后按需调整。

```yaml
# 在 Pod 或容器的 securityContext 中：
securityContext:
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  seccompProfile:
    type: Localhost
    localhostProfile: seccomp-bwrap.json  # 将 docs/isolation/seccomp-bwrap.json 部署到节点
  # procMount: Unmasked    # 仅在 --unshare-pid 落地后需要
```

Kubernetes `RuntimeDefault` 阻止 user namespace 创建。`seccomp=Unconfined` 会移除所有过滤，不推荐使用。请使用自定义 profile。

### 权衡说明

禁用隔离（`isolation.enabled = false`）或使用 `seccomp=unconfined` 会移除 bubblewrap 提供的文件系统视图，增加子进程读取或修改宿主文件、包含密钥的环境变量和 agent 配置的风险。

**建议：** 使用 `docs/isolation/seccomp-bwrap.json` 作为 seccomp profile。它比 `seccomp=unconfined` 范围更窄，已验证可让 bwrap 正常运行。

不要将 `seccomp=unconfined` 作为让 bwrap 工作的捷径。请使用自定义 profile；它只解除了 bwrap 实际需要的 syscall 限制，其余危险 syscall 仍保持阻止。

## 资源限制

bubblewrap 没有内置资源限制选项。`--rlimit-*` 标志不存在；已在实验室通过 `bwrap --help` 确认其缺失。资源限制必须由部署层提供。

### 为何 per-process rlimit 不足以作为沙箱限制

`RLIMIT_NPROC` 计算的是宿主上同一真实 uid 的所有进程数，包括主 PicoClaw 进程和其他并发沙箱命令。在沙箱内通过 `setrlimit` 设置它，会将限制应用于整个 uid，而非仅限于子命令。`RLIMIT_AS`（地址空间）会导致 V8、JVM 和 Go 运行时在启动时因映射大量虚拟地址而崩溃。这两种 rlimit 都无法提供可靠的 per-sandbox 进程上限。

### 部署层限制（推荐）

在部署层应用限制，而不是在 PicoClaw 内部：

**Docker：**

```
docker run --pids-limit 64 --memory 512m --cpus 1.0 …
```

`--pids-limit` 通过 cgroup 强制执行，沙箱内可通过 `/sys/fs/cgroup/pids.max` 看到。已在实验室验证：`docker run --pids-limit 20 … → cat /sys/fs/cgroup/pids.max → 20`。

**Kubernetes：**

```yaml
resources:
  limits:
    memory: "512Mi"
    cpu: "1"
  # pids 限制通过 Pod 级别 spec.os.linux.pidLimit（Kubernetes ≥ 1.27）
```

**systemd（宿主机部署）：**

```
# 在运行 picoclaw 的 unit 文件中：
TasksMax=64
MemoryMax=512M
CPUQuota=100%
```

### Per-command 限制

PicoClaw 直接应用的唯一 per-command 资源限制是 `exec` 工具中的执行超时和进程树终止。所有其他限制必须来自部署层。

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
