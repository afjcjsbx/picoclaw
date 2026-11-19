---
description: "PicoClaw 子进程隔离：为 exec、CLI provider、hooks、MCP server 和插件启用操作系统级沙箱，并在 Linux、macOS 和 Windows 上配置 expose_paths 与 block_network。"
---
# 子进程隔离

PicoClaw 可以把由它启动的子进程放进操作系统级沙箱中运行。该功能是可选的，通过 `config.json` 顶层的 `isolation` 配置块控制。

隔离只作用于子进程。`picoclaw` 主进程仍以宿主用户权限运行。

## 生效范围

- `exec` 工具执行的命令
- `claude-cli`、`codex-cli` 等 CLI provider
- 进程型 hooks
- MCP `stdio` server
- 插件子进程
- Delta Chat RPC 服务端
- Weixin SILK 语音解码器

已知例外：Linux USB 设备监视器会直接启动 `udevadm`，因为它只观察宿主设备，不在子进程隔离的生效范围内。

## 快速开始

在 Linux 上先安装 bubblewrap：

```bash
sudo apt install bubblewrap   # Debian/Ubuntu
sudo dnf install bubblewrap   # Fedora
sudo pacman -S bubblewrap     # Arch
sudo apk add bubblewrap       # Alpine
```

然后在 `config.json` 中启用隔离：

```json
{
  "isolation": {
    "enabled": true
  }
}
```

修改配置后重启 PicoClaw。平台后端缺失或不可用时不会自动回退：启动会直接失败，而不是在无隔离状态下静默运行。

## 配置

```json
{
  "isolation": {
    "enabled": false,
    "expose_paths": [],
    "block_network": false
  }
}
```

| 配置键 | 类型 | 默认值 | 描述 |
|--------|------|--------|------|
| `enabled` | bool | `false` | 是否启用子进程隔离 |
| `expose_paths` | array | `[]` | 显式带入隔离环境的宿主路径；仅在 `enabled` 为 `true` 时生效 |
| `block_network` | bool | `false` | 将子进程放入独立网络命名空间。仅支持 Linux；macOS 和 Windows 在预检阶段会拒绝 |

### `expose_paths`

每个条目的字段如下：

| 字段 | 描述 |
|------|------|
| `source` | 宿主机路径 |
| `target` | 隔离环境内的目标路径；为空时默认等于 `source` |
| `mode` | `ro` 或 `rw` |

规则：

- 同一个 `target` 最终只能保留一条规则。
- 后加载的配置会覆盖先加载的同目标规则。
- Linux 支持真实的 `source -> target` 路径重映射。
- macOS 要求 `target` 与 `source` 相同，因为 Seatbelt 不支持路径重映射。
- Windows 当前不支持 `expose_paths`；配置该字段会直接失败，而不是假装路径已被暴露。

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

### 网络隔离

默认情况下（`block_network: false`），子进程共享宿主网络命名空间。宿主机的本地回环服务、云实例元数据端点（如 `http://169.254.169.254/`）、旁路服务和本地数据库都可以从沙箱内访问。具备网络能力的运行时（`curl`、`python`、`node` 等）均可用于数据外泄；exec 工具的命令级防护不能充当网络边界。

设置 `block_network: true` 后，bubblewrap 会添加 `--unshare-net`，将子进程放入仅含回环接口（`lo`）的全新网络命名空间。对宿主服务或公网的连接会以“连接被拒绝”或“网络不可达”失败。

启用 `block_network: true` 后会受影响的功能：

- 沙箱内的 `curl`、`wget` 及其他 HTTP 客户端
- `git fetch`、`git clone`、`git push` 等网络操作
- 下载软件包的包管理器（`pip`、`npm`、`apt` 等）
- 自身需要访问互联网的 MCP server
- 所有依赖子进程出站网络调用的 agent 工具

如果 agent 需要经过过滤的公网出站访问，但不能访问宿主本地服务，应让流量经过出站代理，而不是使用 `block_network`。

## 平台支持

| 平台 | 后端 | 说明 |
|------|------|------|
| Linux | `bwrap`（bubblewrap） | 必需。PID namespace 隔离为强制项。可选网络命名空间。 |
| macOS | 通过 `/usr/bin/sandbox-exec` 使用 Seatbelt | 仅支持同路径访问。网络保持可用。 |
| Windows | 受限 token、低完整性级别、`Job Object` | 进程级限制。不支持 `expose_paths`。 |
| 其他 | 无 | 启用隔离会失败，而不是假装隔离成功。 |

### Linux

Linux 后端依赖 `bwrap`（bubblewrap），提供：

- 最小文件系统视图
- PID namespace 隔离（必需；沙箱拥有独立 PID 表，`/proc` 只描述自身进程）
- IPC namespace 隔离
- 子进程用户环境重定向
- `source -> target` 只读或读写挂载
- 丢弃全部 Linux capabilities（`--cap-drop ALL`）
- 新会话（`--new-session`），不持有控制终端，阻止基于 TIOCSTI 的终端注入（CVE-2017-5226）
- 每个沙箱独立的临时 `/tmp` tmpfs；不暴露宿主 `/tmp`
- 可选网络命名空间隔离（`block_network: true`）

#### 实例根与默认挂载

实例根遵循 `config.GetHome()`：设置了 `PICOCLAW_HOME` 时使用该值，否则使用 `~/.picoclaw`。

实例根内的默认挂载范围刻意收窄：

| 路径 | 权限 | 用途 |
|------|------|------|
| `<root>/workspace` | rw | Agent 工作区 |
| `<root>/runtime-user-env` | rw | 重定向后的 `HOME`、`TMPDIR` 和 XDG 目录 |
| `<root>/skills` | ro | 沙箱可执行的 skill 脚本 |

`config.json`（含 API 密钥）、`.security.yml`、`logs`、`cache`、`state` 及实例根下的其他文件**不会被挂载**。沙箱内的命令无法读取或修改宿主配置。

最小运行时系统路径（`/usr`、`/bin`、`/lib`、`/lib64`、`/etc/resolv.conf` 等）以只读方式挂载。

运行时还会按需补充可执行文件本身、其所在目录、生效后的工作目录，以及命令行中的绝对路径参数。工作目录仅在已被可写规则覆盖时（`workspace`、`runtime-user-env` 或显式 `rw` 的 `expose_paths`）才以读写方式挂载，否则以只读方式挂载，避免已记住的 `cwd` 扩大可写边界。符号链接形式的工作目录会先解析为规范路径再挂载，以兼容 bubblewrap 0.12 及更高版本。

命令行绝对路径参数的规则如下：

- 只有不含空白字符、控制字符或 Shell 元字符（`;`、`&`、`|`、`<`、`>`、`(`、`)`、`$`、反引号、引号、`\`、`*`、`?`、`[`、`]`、`{`、`}`）的纯绝对路径 token 才会被当作路径。传给 `sh -c` 的命令字符串因此永远不会被当作路径。
- `/proc`、`/sys`、`/dev` 下的路径永远不会从宿主自动挂载。沙箱会提供自己的 `/proc` 和 `/dev`。
- 已存在的路径以**只读**方式暴露。命令行传入的目录不会成为可写漏洞；可写位置来自 `expose_paths` 或生效后的工作目录。
- 不存在的路径会将其**父目录**以**读写**方式暴露，使命令可以创建输出文件。这支持 Weixin SILK 语音解码器等写入尚不存在输出文件的工具。
- 过于宽泛的路径会被**拒绝**：`/`、`/` 的直接子目录（如 `/home`、`/etc`、`/tmp`、`/proc`）、当前用户的家目录及其所有祖先目录。每个不同的被拒绝路径只记录一条警告日志。如需访问这些路径，请在 `isolation.expose_paths` 中显式配置。
- 如果挂载计划中已有覆盖该路径的规则（精确匹配或其祖先目录），则不再追加新规则，避免冲突的模式叠加。

#### 重新暴露实例根

如果运维人员确实需要让沙箱看到完整的实例根，可以添加一条 target 等于实例根的 `expose_paths` 规则：

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

该规则会在默认规则之后合并，恢复完整根目录视图。Agent 系统提示中的沙箱边界通知也会同步更新。

#### 需要额外路径的 spawn site

部分频道和 hooks 会在实例根下存储数据，启用隔离时需要显式配置 `expose_paths`：

| Spawn site | 所需路径 | 建议规则 |
|------------|----------|----------|
| Delta Chat RPC 服务 | `~/.picoclaw/deltachat/<channel>`（rw） | 将该目录加入 `expose_paths` |
| 脚本位于实例根下的进程 hook | hook 脚本目录（按需 ro 或 rw） | 将 hook 目录加入 `expose_paths` |
| 工作目录位于实例根下的插件 MCP stdio server | 插件目录（ro） | 将其加入 `expose_paths` |

Weixin SILK 语音解码器使用操作系统临时目录（`/tmp`），不在实例根下，因此无需额外规则。

#### `PICOCLAW_CONFIG`

Linux 隔离启用时，`PICOCLAW_CONFIG` 不会传给 `exec` 子进程，因为 `config.json` 未被挂载，该路径在沙箱内不存在。`PICOCLAW_HOME` 仍会传入。

#### bubblewrap 要求

缺少 `bwrap` 时不会自动回退。PicoClaw 会在启动时探测一次 bubblewrap 的命名空间支持情况。

PID namespace（`--unshare-pid`）为必需项。如果探测失败，启动会失败并给出明确的修复提示：

- Docker：添加 `--security-opt systempaths=unconfined`
- Kubernetes：设置 `procMount: Unmasked`

如需完全关闭隔离，设置 `"isolation.enabled": false`（风险：无文件系统隔离）。可选 flag（如 `--unshare-ipc`）若被内核以 `Creating new namespace failed`（EINVAL）拒绝，会以警告形式丢弃；挂载命名空间、文件系统视图和用户环境仍然有效。

支持的 bubblewrap 版本：

| 版本范围 | 支持状态 |
|----------|----------|
| < 0.3.3 | **启动时拒绝。** 0.1.x 和 0.2.x 缺少 PicoClaw 始终传递的 `--` 参数分隔符，会以 `Unknown option --` 失败。 |
| >= 0.3.3，< 0.12.0 | 允许运行，但会记录一次性警告。CVE-2026-87766（沙箱搭建期间的符号链接遍历可在沙箱外创建文件）尚未修复。发行版软件包可能携带反向移植补丁；建议升级或使用包含该补丁的构建。 |
| >= 0.12.0 | 完全支持，推荐使用。CVE-2026-87766 已修复。 |

无论版本如何，setuid 模式的 bubblewrap 二进制文件**始终被拒绝**。setuid 执行模型是特权提升类 CVE（如 CVE-2026-41163）的攻击面。bubblewrap 0.12+ 已不再使用 setuid；请移除 setuid 位，或安装 0.12+ 版本的软件包。

Linux 还会拒绝 `expose_paths` 中 `target` 路径经过位于可写挂载点内符号链接的规则。这可防止沙箱内的 agent 在工作区植入符号链接，并把挂载目标重定向到宿主机任意路径（bwrap < 0.12.0 的 CVE-2026-87766）。系统级符号链接（如 Debian 的 `/bin -> usr/bin`）位于只读挂载点下，不受影响。如遇拒绝，请删除该符号链接或选择不经过它的目标路径。

#### 不要以 root 运行

当有效 uid 为 0 时，bwrap 不会自动创建 user namespace，沙箱进程在 `--cap-drop` 之后仍会保留完整的宿主 capabilities。PicoClaw 会在 root 模式下额外传入 `--unshare-user`，在新的 user namespace 中将 capabilities 清零。以 root 运行的风险面仍大于使用专用非特权用户。

### macOS

macOS 后端通过 `/usr/bin/sandbox-exec` 使用默认拒绝的 Seatbelt 配置文件。它允许在 PicoClaw 实例根和显式 `rw` 路径下写入，并按需开放系统文件、命令、配置的工作目录和绝对路径参数。

- 实例根下的只读 `expose_paths` 规则会被拒绝：Seatbelt 规则是叠加的，后加的只读允许无法撤销根目录的写权限。
- 不支持 `source -> target` 重映射；`target` 必须等于 `source`。
- 网络访问保持开启，与 Linux 的 `block_network: false` 行为一致。
- Apple 已弃用 `sandbox-exec`；该命令缺失时，隔离会失败关闭。

### Windows

Windows 隔离施加的是进程级限制：

- 受限 primary token
- 低完整性级别
- `Job Object`
- 重定向后的子进程用户环境目录（`USERPROFILE`、`HOME`、`TEMP`、`TMP`、`APPDATA`、`LOCALAPPDATA`）

它不实现真正的 `source -> target` 文件系统重映射，也不支持 `expose_paths`。Windows 上 agent 提示中的沙箱边界通知没有路径列表。

### 其他平台

在未支持的平台上显式启用隔离时，PicoClaw 会将其报告为不支持的配置，而不是假装隔离成功。

## 实例根与目录

实例根遵循 `config.GetHome()`：

- 如果设置了 `PICOCLAW_HOME`，使用该值。
- 否则默认使用用户目录下的 `~/.picoclaw`。

如果隔离开启时 `config.GetHome()` 回退到 `.`，启动会失败。

默认实例目录包括：

- 实例根本身
- `skills`
- `logs`
- `cache`
- `state`
- `runtime-user-env`

`workspace` 优先使用 `cfg.WorkspacePath()` 的结果，未显式配置时才按默认规则派生。

Windows 还会额外准备 `runtime-user-env/AppData/Roaming` 和 `runtime-user-env/AppData/Local`。

隔离开启后，子进程会收到重定向到实例目录下的独立用户环境：

- Linux 和 macOS：`HOME`、`TMPDIR`、`XDG_CONFIG_HOME`、`XDG_CACHE_HOME`、`XDG_STATE_HOME`
- Windows：`USERPROFILE`、`HOME`、`TEMP`、`TMP`、`APPDATA`、`LOCALAPPDATA`

这些路径都指向实例根下的 `runtime-user-env`。

## 环境变量继承

隔离**开启**时，子进程只会从父进程继承一组范围较小的基础环境变量。父 shell 中设置的其他所有变量（包括各类 API 密钥和机密）均会被排除。

基础允许集涵盖运行时必需项：`PATH`、`HOME`、`USER`、`LANG`、`LC_ALL`、`TERM`、`SHELL`、`TMPDIR`，以及所有以 `XDG_` 开头的变量。Windows 上还会额外包含一组 Windows 专属变量。

各启动位置可声明额外变量或厂商命名空间前缀：

- **`claude-cli` provider**：`ANTHROPIC_*`、`CLAUDE_*`、代理变量（`HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` 及其小写形式）、`SSL_CERT_FILE`、`NODE_EXTRA_CA_CERTS`
- **`codex-cli` provider**：`OPENAI_*`、`CODEX_*`，以及相同的代理与 TLS 变量
- **MCP `stdio` server**：仅基础集，除非 server 配置中设置了 `inherit_env: true`（此时传入完整父环境）
- **进程 hooks**：基础集加上 hook 配置中显式声明的变量（`Env` 字段）
- **Delta Chat RPC 服务端**：基础集加上 `DC_ACCOUNTS_PATH`
- **Weixin 语音解码器**（ffmpeg / silk）：仅基础集

CLI provider 没有自己的环境变量配置项：启用隔离时，上述列表之外的变量（例如使用 Bedrock 的 `claude` CLI 所需的 `AWS_*`）不会被传递。

隔离**关闭**时，所有子进程均完整继承父进程环境，行为与之前一致。

## 提示中的沙箱边界

隔离开启且受支持时，agent 系统提示会包含 `Sandbox Boundaries` 小节。该内容在创建 agent 时由 `DescribeBoundary` 生成，列出：

- workspace 和 PicoClaw 实例根
- 临时目录（`$TMPDIR`）
- 用户配置的 `expose_paths`，分为可写和只读目标
- 子进程是否仍可访问网络

该通知会告诉模型：边界处出现 `Read-only file system`、路径不存在或权限错误，并不代表宿主损坏。模型应报告该问题，并请用户把路径加入 `isolation.expose_paths`，而不是改用 `sudo`、另一个绝对路径或其他工具重试。

该通知只描述子进程隔离。文件工具仍独立遵循 `restrict_to_workspace` 和 `allow_read_outside_workspace`。配置重载会重建 agent registry，因此修改后的隔离配置会反映在下一次系统提示中。

## 日志与排障

隔离开启后，PicoClaw 会记录生成后的隔离计划：

- Linux：`linux isolation mount plan`
- Windows：`windows isolation access rules`
- macOS：调试日志 `macOS Seatbelt profile prepared`

如果怀疑隔离未生效，请检查这些日志中是否出现了不应暴露的宿主路径。

## 与 `restrict_to_workspace` 的关系

- `restrict_to_workspace` 限制 agent 默认可访问的路径。
- 子进程隔离限制子进程能看到什么文件系统，以及它的用户环境指向哪里。

两者互补，不互相替代。

## 当前限制

- Linux 基于 `bwrap` 实现，而不是纯内建隔离运行时。
- Linux 默认启用独立的 PID namespace；Docker 容器必须添加 `--security-opt systempaths=unconfined`。
- Windows 还没有对所有允许/拒绝路径做完整的宿主 ACL 落地。
- macOS Seatbelt 不支持 `source -> target` 路径重映射，并依赖 Apple 已弃用的 `sandbox-exec`。
- 隔离作用于子进程，不是 `picoclaw` 主进程自身。
- `block_network` 仅支持 Linux。

## 实现参考

包级实现说明（包含启动链路和代码阅读顺序）位于 [`pkg/isolation/README.md`](https://github.com/afjcjsbx/picoclaw/blob/main/pkg/isolation/README.zh.md)。

相关文档：

- [配置指南](../guides/configuration.zh.md)：所有 `config.json` 配置块，包括 `isolation`
- [Exec 工具](../tools/exec.md)：命令防护、风险分类和 exec 限制
- [工具配置](../reference/tools_configuration.md)：工具开关和文件系统访问策略
