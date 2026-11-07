---
lang: zh
description: "PicoClaw 中文项目说明：超轻量 Go 个人 AI 助手，可在 10 美元的开发板上以 10 到 20 MB 内存运行，支持 RISC-V、ARM、MIPS、LoongArch 和 x86。"
---
<div align="center">

<img src="https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/assets/logo.webp" alt="PicoClaw" width="200">

<h1>PicoClaw</h1>

**轻如鸿毛、随处可跑的 AI 智能体。**

_刻意保持小巧。小巧不等于简陋。小巧，已然足够。_

<p>
  <img src="https://img.shields.io/badge/Go-1.27.3+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go 1.27.3+">
  <img src="https://img.shields.io/badge/Platforms-x86__64%20%7C%20ARM64%20%7C%20MIPS%20%7C%20RISC--V%20%7C%20LoongArch-blue" alt="支持的平台">
  <img src="https://img.shields.io/badge/License-MIT-green" alt="MIT 许可证">
  <img src="https://img.shields.io/github/v/release/afjcjsbx/picoclaw?style=flat&label=release" alt="最新版本">
  <img src="https://img.shields.io/github/stars/afjcjsbx/picoclaw?style=flat&label=stars" alt="GitHub Stars">
  <br>
  <a href="https://afjcjsbx.github.io/picoclaw/"><img src="https://img.shields.io/badge/Docs-Read-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="文档"></a>
  <a href="https://discord.gg/bEuEDnKpf"><img src="https://img.shields.io/badge/Discord-Join%20the%20community-5865F2?style=flat&logo=discord&logoColor=white" alt="Discord"></a>
  <a href="https://deepwiki.com/afjcjsbx/picoclaw"><img src="https://img.shields.io/badge/Wiki-DeepWiki-FFA500?style=flat&logo=wikipedia&logoColor=white" alt="DeepWiki"></a>
</p>

<p>
  <a href="https://github.com/afjcjsbx/picoclaw/blob/main/docs/README.md">English</a> |
  <strong>中文</strong>
</p>

</div>

---

<p align="center">
  <a href="https://discord.gg/bEuEDnKpf">
    <img src="https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/assets/discord-banner.png" alt="PicoClaw Discord —— 结识开发者、分享想法、获取帮助" width="80%">
  </a>
</p>

## PicoClaw 是什么？

**PicoClaw** 是一个完全使用 **Go** 编写的超轻量级个人 AI 助手——它不是 OpenClaw、NanoBot 或任何其他智能体框架的分支。受 [NanoBot](https://github.com/HKUDS/nanobot) 启发，它通过“自举”流程从零重建：由智能体自身驱动架构迁移和代码优化。

本仓库是该项目持续维护、由社区驱动的延续，包含持续的修复、依赖更新和新功能。

**可运行在 10 美元的硬件上。占用 10–20 MB 内存。不到一秒即可启动。**

**别让旧硬件在地下室里积灰——用 PicoClaw 让它重获新生！**

## 特性

| | |
|:--|:--|
| 🪶 **超轻量**<br>近期版本仅占用 10–20 MB 内存——小到足以运行在 10 美元的开发板上。 | ⚡ **瞬间启动**<br>即使在 0.6 GHz 单核处理器上，也能在不到一秒内就绪。 |
| 💰 **成本极低**<br>运行它的硬件成本比 Mac mini 低 98%。 | 🌍 **一个二进制文件跑遍所有平台**<br>RISC-V、ARM、MIPS、x86-64 和 LoongArch——同一个可执行文件。 |
| 🔌 **原生支持 MCP**<br>连接任意 [Model Context Protocol](https://modelcontextprotocol.io/) 服务器来扩展智能体。 | 🧩 **Agent Plugins 1.0.0**<br>将技能和 MCP 服务器打包为可移植、可安装的插件。 |
| 👁️ **视觉管线**<br>直接将图片和文件发送给多模态 LLM，自动进行 base64 编码。 | 🧠 **智能路由**<br>基于规则的模型路由将简单查询交给更便宜、更快的模型。 |
| 💬 **19+ 聊天渠道**<br>Telegram、Discord、Slack、WhatsApp、Matrix、微信等。 | 🔗 **30+ Provider**<br>OpenAI、Anthropic、Gemini、OpenRouter、DeepSeek、本地模型等。 |

### 对比

|                                | OpenClaw                                                     | NanoBot                                                                     | **PicoClaw**                                                                                 |
| ------------------------------ | ------------------------------------------------------------ | --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **语言**                       | TypeScript                                                   | Python                                                                      | **Go**                                                                                        |
| **内存参考**                   | [建议主机内存 2 GB](https://docs.openclaw.ai/install/fly)    | [报告约 200–600 MB](https://github.com/HKUDS/nanobot/issues/3410)           | **近期版本为 10–20 MB**                                                                       |
| **单一可执行文件**             | 否（Node.js 应用）                                           | 否（带原生 TUI 组件的 Python 应用）                                         | **[48.1 MiB](https://afjcjsbx.github.io/picoclaw/guides/custom-builds.html#measured-binary-sizes)**                            |
| **启动时间**</br>（0.8 GHz 核心） | >500 秒                                                   | >30 秒                                                                      | **<1 秒**                                                                                     |
| **成本**                       | Mac mini $599                                                | 大多数 Linux 开发板约 $50                                                   | **任意 Linux 开发板**</br>**低至 $10**                                                        |

> 这些是参考数据，并非受控基准测试。OpenClaw 的数据来自部署建议，NanoBot 的数据来自一位 Raspberry Pi 4 用户报告，PicoClaw 的数据来自近期版本的估算。OpenClaw 和 NanoBot 并未将完整智能体打包为单一可执行文件；PicoClaw 的体积是实测的完整默认 macOS ARM64 构建——请参阅[测量方法及其他配置](https://afjcjsbx.github.io/picoclaw/guides/custom-builds.html#measured-binary-sizes)。

### 运行时资源占用

| 工具 | 运行时 | 安装体积 | 首次绘制 / 就绪 | 空闲 RSS | 进程数 |
| --- | --- | ---: | ---: | ---: | :---: |
| **PicoClaw**（核心） | Go 1.27，单一静态二进制文件 | **48.1 MiB** | **≈66 ms** | **37.9 MiB** | **1** |
| Claude Code 2.1.300 | Bun 编译的 TS（原生二进制文件） | 240 MiB | ≈263 ms | ≈184 MiB | 1 |
| pi 1.2.0 | Bun 编译的 TS + pi-tui | 148 MiB | ≈369 ms | ≈169 MiB | 1 |
| OpenCode 2.0.29 | Bun + OpenTUI | 239 MiB | ≈190 ms | ≈200 MiB | 1 |
| Hermes 0.22.1 | Python + TUI | ≈1.9 GiB | ≈3.2 s | ≈214 MiB | 2 |

PicoClaw 的空闲 RSS 为 37.9 MiB，内存占用约为 **≈19 MiB**（`phys_footprint`，即活动监视器显示的值）——这正是上文“10–20 MB”说法的依据。其核心不包含全屏 TUI，因此“首次绘制 / 就绪”是指网关响应 `GET /ready` 所需的时间。更轻量的渠道配置列于[实测二进制体积](https://afjcjsbx.github.io/picoclaw/guides/custom-builds.html#measured-binary-sizes)。

<details>
<summary><b>基准测试方法</b></summary>

在 Apple M1 Max（macOS arm64）上使用 [`scripts/`](https://github.com/afjcjsbx/picoclaw/tree/main/scripts/) 中的脚本测量。PicoClaw 的数据使用 Go 1.27.3，于 2026-10-26 刷新；其他工具于 2026-10-15 测量。**首次绘制 / 就绪**、**空闲 RSS** 和**进程数**来自这些脚本；**安装体积**是各工具自身安装目录的大小，并非由这些脚本测量。**空闲 RSS** 是进程树空闲后各进程常驻内存之和。其他智能体是交互式 TUI，其数据为单机多次运行的中位数，因此不同版本之间可能存在偏差。

使用以下命令复现运行时列：

```bash
python3 scripts/bench-picoclaw-runtime.py --binary ./build/picoclaw --runs 15
python3 scripts/bench-agent-tui.py --cmd opencode --runs 3 --keys '\x03'
```

</details>

> **[硬件兼容列表](https://afjcjsbx.github.io/picoclaw/guides/hardware-compatibility.html)** —— 从 5 美元的 RISC-V 设备到 Raspberry Pi 和 Android 手机，均已测试。没有你的开发板？欢迎提交 PR！

## 安装

### 一行安装（Linux 和 macOS）

```sh
curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | sh
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.ps1 | iex
```

安装程序会检测 CPU 架构、校验发布包校验和、安装两个二进制文件并启动启动器。Web UI 会在 `http://localhost:18800` 打开；首次运行时，请在浏览器中创建仪表盘密码。

### 预编译二进制文件

从 [GitHub Releases](https://github.com/afjcjsbx/picoclaw/releases) 下载适合你平台的二进制文件。

### 从源码构建

前置要求：

- Go 1.27.3+
- Node.js 22+ 和 pnpm 10.33.0+（用于构建 Web UI 和启动器）

```bash
git clone https://github.com/afjcjsbx/picoclaw.git
cd picoclaw

make deps

# 安装前端依赖
(cd web/frontend && pnpm install --frozen-lockfile)

# 为当前平台构建核心二进制文件
make build

# 构建 Web UI Launcher（Web UI 模式必需）
make build-launcher

# 为 Makefile 管理的所有平台构建核心二进制文件
make build-all

# 构建单个目标（供 CI 矩阵使用）
make build-target TARGET=linux/arm64
make build-target TARGET=linux/arm GOARM=7

# 为 Raspberry Pi Zero 2 W 构建
make build-pi-zero

# 构建并安装
make install
```

**Raspberry Pi Zero 2 W：** 请使用与操作系统匹配的二进制文件——32 位 Raspberry Pi OS：`make build-linux-arm`；64 位：`make build-linux-arm64`。或运行 `make build-pi-zero` 同时构建两者。

<details>
<summary><b>自定义渠道构建</b></summary>

默认构建包含所有渠道。自定义渠道选择、可选的 Seahorse 构建、SQLite 注意事项以及实测二进制体积记录在[自定义构建指南](https://afjcjsbx.github.io/picoclaw/guides/custom-builds.html)中。例如，以下命令仅使用 Telegram 构建：

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram'
```

[Seahorse](https://afjcjsbx.github.io/picoclaw/architecture/seahorse-memory.html) 是 PicoClaw 内置的记忆系统：`seahorse` 上下文管理器维护一个本地 SQLite 数据库，并对会话历史建立 FTS5 索引，使智能体能够回忆并压缩较早的上下文。若要进一步缩小体积，可使用 `no_seahorse` 标签构建；当其他已选功能不需要 SQLite 时，这也会移除 SQLite 依赖：

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram,no_seahorse'
```

如果记忆由外部系统提供，例如 [Mem0 插件](https://afjcjsbx.github.io/picoclaw/guides/plugins.html#mem0)，这就是合适的选择。在实测的 macOS ARM64 仅 Telegram 构建中，添加 `no_seahorse` 使二进制文件减小了 4.00 MiB（10.0%）。完整测量细节以及 Matrix 或 WhatsApp Native 仍需要 SQLite 的情况，请参阅该指南。

</details>

## 快速开始

### Web UI Launcher（推荐）

Web UI Launcher 提供基于浏览器的配置和聊天界面。这是最简单的上手方式——无需命令行知识。

**方式一：双击启动（桌面）**

从 [GitHub Releases](https://github.com/afjcjsbx/picoclaw/releases) 下载后，双击 `picoclaw-launcher`（Windows 上为 `picoclaw-launcher.exe`）。浏览器会自动打开 `http://localhost:18800`。

**方式二：命令行**

```bash
picoclaw-launcher
# 在浏览器中打开 http://localhost:18800
```

> [!TIP]
> **远程访问 / Docker / 虚拟机：** 添加 `-public` 参数以监听所有网络接口：
>
> ```bash
> picoclaw-launcher -public
> ```

**开始使用：** 打开 Web UI，然后 **1)** 配置 Provider（填入你的 LLM API Key）→ **2)** 配置渠道（例如 Telegram）→ **3)** 启动网关 → **4)** 开始聊天。

详细文档请参阅 [Web UI 与 Launcher 指南](https://afjcjsbx.github.io/picoclaw/guides/webui.html)。

<details>
<summary><b>Docker（备选方案）</b></summary>

```bash
# 1. 克隆本仓库
git clone https://github.com/afjcjsbx/picoclaw.git
cd picoclaw

# 2. 首次运行——自动生成 docker/data/config.json 后退出
#    （仅在 config.json 和 workspace/ 均不存在时触发）
docker compose -f docker/docker-compose.yml --profile launcher up
# 容器打印 "First-run setup complete." 后停止。

# 3. 填写 API Key
vim docker/data/config.json

# 4. 启动
docker compose -f docker/docker-compose.yml --profile launcher up -d
# 打开 http://localhost:18800
```

> **Docker / 虚拟机用户：** 网关默认监听 `127.0.0.1`。设置 `PICOCLAW_GATEWAY_HOST=0.0.0.0` 或使用 `-public` 参数，以便从宿主机访问。

```bash
# 查看日志
docker compose -f docker/docker-compose.yml logs -f

# 停止
docker compose -f docker/docker-compose.yml --profile launcher down

# 更新
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS：首次启动安全警告</b></summary>

macOS 可能会在首次启动时拦截 `picoclaw-launcher`，因为它从互联网下载，且未经 Mac App Store 公证。

1. 双击 `picoclaw-launcher`。macOS 会显示安全警告：*"picoclaw-launcher" 无法打开——Apple 无法验证 "picoclaw-launcher" 不含可能损害 Mac 或危及隐私的恶意软件。*
2. 打开**系统设置 → 隐私与安全性**，向下滚动到**安全性**部分，点击**仍要打开**，然后在对话框中确认**仍要打开**。

完成这一次性操作后，后续启动 `picoclaw-launcher` 将正常打开。

</details>

## Providers

<details>
<summary><b>30+ 支持的 LLM Provider 与配置</b></summary>

PicoClaw 通过 `model_list` 配置支持 30+ LLM Provider。使用 `protocol/model` 格式：

| Provider | 协议 | API Key | 备注 |
|----------|----------|---------|-------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | 必填 | GPT-5.4、GPT-4o、o3 等 |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | 必填 | Claude Opus 4.6、Sonnet 4.6 等 |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | 必填 | Gemini 3 Flash、2.5 Pro 等 |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | 必填 | 200+ 模型，统一 API |
| [智谱 (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | 必填 | GLM-4.7、GLM-5 等 |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | 必填 | DeepSeek-V3、DeepSeek-R1 |
| [火山引擎](https://console.volcengine.com) | `volcengine/` | 必填 | 豆包、Ark 系列模型 |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | 必填 | Qwen3、Qwen-Max 等 |
| [Groq](https://console.groq.com/keys) | `groq/` | 必填 | 快速推理（Llama、Mixtral） |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | 必填 | Kimi 系列模型 |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | 必填 | MiniMax 系列模型 |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | 必填 | Mistral Large、Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | 必填 | NVIDIA 托管模型 |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | 必填 | 快速推理 |
| [NEAR AI Cloud](https://near.ai/) | `nearai/` | 必填 | TEE 推理，兼容 OpenAI |
| [Novita AI](https://novita.ai/) | `novita/` | 必填 | 多种开源模型 |
| [小米 MiMo](https://platform.xiaomimimo.com/) | `mimo/` | 必填 | MiMo 系列模型 |
| [Ollama](https://ollama.com/) | `ollama/` | 无需 | 本地模型，自托管 |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | 无需 | 本地部署，兼容 OpenAI |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | 视情况 | 100+ Provider 代理 |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | API Key 或 Entra ID** | 企业级 Azure 部署 |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | 设备码登录 |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |
| [AWS Bedrock](https://console.aws.amazon.com/bedrock)* | `bedrock/` | AWS 凭证 | AWS 上的 Claude、Llama、Mistral |

> \* AWS Bedrock 需要构建标签：`go build -tags bedrock`。将 `api_base` 设置为区域名称（例如 `us-east-1`），即可在所有 AWS 分区（aws、aws-cn、aws-us-gov）中自动解析端点。如果改用完整端点 URL，则还必须通过环境变量或 AWS config/profile 配置 `AWS_REGION`。
>
> \*\* Azure OpenAI 在设置了 `api_key` 时使用该值。如果省略 `api_key`，Provider 会通过 `DefaultAzureCredential`（环境变量、工作负载身份、托管身份、Azure CLI 等）回退到 Microsoft Entra ID。Entra ID 路径需要构建标签：`go build -tags azidentity`。

<details>
<summary><b>本地部署（Ollama、vLLM 等）</b></summary>

**Ollama：**

```json
{
  "model_list": [
    {
      "model_name": "local-llama",
      "model": "ollama/llama3.1:8b",
      "api_base": "http://localhost:11434/v1"
    }
  ]
}
```

**vLLM：**

```json
{
  "model_list": [
    {
      "model_name": "local-vllm",
      "model": "vllm/your-model",
      "api_base": "http://localhost:8000/v1"
    }
  ]
}
```

完整的 Provider 配置详情请参阅 [Providers 与模型](https://afjcjsbx.github.io/picoclaw/guides/providers.html)。

</details>

</details>

## Channels

<details>
<summary><b>19+ 聊天平台与配置指南</b></summary>

通过 19+ 消息平台与你的 PicoClaw 对话：

| Channel | 配置难度 | 协议 | 文档 |
|---------|-------|----------|------|
| **Telegram** | 简单（bot token） | 长轮询 | [指南](https://afjcjsbx.github.io/picoclaw/channels/telegram/) |
| **Discord** | 简单（bot token + intents） | WebSocket | [指南](https://afjcjsbx.github.io/picoclaw/channels/discord/) |
| **WhatsApp** | 简单（扫码或 bridge URL） | 原生 / Bridge | [指南](https://afjcjsbx.github.io/picoclaw/guides/chat-apps.html#whatsapp) |
| **微信** | 简单（原生扫码） | iLink API | [指南](https://afjcjsbx.github.io/picoclaw/guides/chat-apps.html#weixin) |
| **QQ** | 简单（AppID + AppSecret） | WebSocket | [指南](https://afjcjsbx.github.io/picoclaw/channels/qq/) |
| **Slack** | 简单（bot + app token） | Socket Mode | [指南](https://afjcjsbx.github.io/picoclaw/channels/slack/) |
| **Matrix** | 中等（homeserver + token） | Sync API | [指南](https://afjcjsbx.github.io/picoclaw/channels/matrix/) |
| **Delta Chat** | 简单（chatmail 或现有账户） | JSON-RPC（email/E2EE） | [指南](https://afjcjsbx.github.io/picoclaw/channels/deltachat/) |
| **钉钉** | 中等（client credentials） | Stream | [指南](https://afjcjsbx.github.io/picoclaw/channels/dingtalk/) |
| **飞书 / Lark** | 中等（App ID + Secret） | WebSocket/SDK | [指南](https://afjcjsbx.github.io/picoclaw/channels/feishu/) |
| **LINE** | 中等（credentials + webhook） | Webhook | [指南](https://afjcjsbx.github.io/picoclaw/channels/line/) |
| **企业微信** | 简单（扫码登录或手动配置） | WebSocket | [指南](https://afjcjsbx.github.io/picoclaw/channels/wecom/) |
| **VK** | 简单（群组 token） | Long Poll | [指南](https://afjcjsbx.github.io/picoclaw/channels/vk/) |
| **IRC** | 中等（server + nick） | IRC 协议 | [指南](https://afjcjsbx.github.io/picoclaw/guides/chat-apps.html#irc) |
| **OneBot** | 中等（WebSocket URL） | OneBot v11 | [指南](https://afjcjsbx.github.io/picoclaw/channels/onebot/) |
| **MQTT** | 简单（broker + agent_id） | MQTT pub/sub | [指南](https://afjcjsbx.github.io/picoclaw/channels/mqtt/) |
| **MaixCam** | 简单（启用即可） | TCP socket | [指南](https://afjcjsbx.github.io/picoclaw/channels/maixcam/) |
| **Pico** | 简单（启用即可） | 原生协议 | 内置 |
| **Pico Client** | 简单（WebSocket URL） | WebSocket | 内置 |

> 所有基于 Webhook 的渠道共用同一个 Gateway HTTP 服务器（`gateway.host`:`gateway.port`，默认 `127.0.0.1:18790`）。飞书使用 WebSocket/SDK 模式，不使用共享 HTTP 服务器。

> 日志详细程度由 `gateway.log_level` 控制（默认：`warn`）。支持的值：`debug`、`info`、`warn`、`error`、`fatal`。也可通过 `PICOCLAW_LOG_LEVEL` 设置。详情请参阅[配置](https://afjcjsbx.github.io/picoclaw/guides/configuration.html#gateway-log-level)。

详细的渠道配置说明请参阅[聊天应用配置](https://afjcjsbx.github.io/picoclaw/guides/chat-apps.html)。

</details>

## Tools

### 网络搜索

<details>
<summary><b>支持的搜索引擎与免费额度</b></summary>

PicoClaw 可以搜索网络以提供最新信息。在 `tools.web` 中配置：

| 搜索引擎 | API Key | 免费额度 | 链接 |
|--------------|---------|-----------|------|
| DuckDuckGo | 无需 | 无限制 | 内置备用 |
| [Gemini Google Search](https://aistudio.google.com/apikey) | 必填 | 视情况 | Gemini + Google Search grounding |
| [百度搜索](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | 必填 | 1500 次/月（按天发放） | AI 驱动，针对中国优化 |
| [Tavily](https://tavily.com) | 必填 | 1000 次查询/月 | 专为 AI 智能体优化 |
| [Brave Search](https://brave.com/search/api) | 必填 | 2000 次查询/月 | 快速且注重隐私 |
| [Kagi Search](https://help.kagi.com/kagi/api/search.html) | 必填 | 付费/受 API 配置限制 | 优质搜索结果 |
| [Keenable](https://keenable.ai) | 可选 | 免 Key：10 次请求/秒，每 IP 1000 次/小时 | 无需 Key 即可使用；提供 Key 可提高速率限制 |
| [Perplexity](https://www.perplexity.ai) | 必填 | 付费 | AI 驱动搜索 |
| [SearXNG](https://github.com/searxng/searxng) | 无需 | 自托管 | 免费元搜索引擎 |
| [GLM Search](https://open.bigmodel.cn/) | 必填 | 视情况 | 智谱网络搜索 |

</details>

### 其他工具

PicoClaw 内置文件操作、代码执行、定时任务等工具。详情请参阅 [Tools](https://afjcjsbx.github.io/picoclaw/tools/)。

## Skills

Skills 是扩展智能体能力的模块化功能。它们从工作区中的 `SKILL.md` 文件加载。

**从 ClawHub 安装 Skills：**

```bash
picoclaw skills search "web scraping"
picoclaw skills install <skill-name>
```

**配置 Skills 仓库源**，位于 `tools.skills.registries`：

```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "enabled": true,
          "base_url": "https://clawhub.ai",
          "auth_token": "your-clawhub-token"
        },
        "github": {
          "enabled": true,
          "base_url": "https://github.com",
          "auth_token": "your-github-token",
          "proxy": ""
        }
      }
    }
  }
}
```

ClawHub token 是可选的，仅用于提高速率限制。GitHub token 用于搜索和私有仓库。请通过 `PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_AUTH_TOKEN` 和 `PICOCLAW_SKILLS_REGISTRIES_GITHUB_AUTH_TOKEN` 传入。

Skills 和 MCP 服务器也可以一起打包为可移植的 **Agent Plugins 1.0.0**。请参阅[插件指南](https://afjcjsbx.github.io/picoclaw/guides/plugins.html)和[可用的演示插件](https://github.com/afjcjsbx/picoclaw/tree/main/examples/plugins/demo-plugin/)。Mem0 发布了自己的 [Agent Plugin](https://github.com/mem0ai/mem0/tree/main/integrations/mem0-agent-plugin)；启用前请阅读[兼容性说明](https://afjcjsbx.github.io/picoclaw/guides/plugins.html#mem0)。

更多详情请参阅 [Skills 工具](https://afjcjsbx.github.io/picoclaw/tools/skills.html)。

## MCP（Model Context Protocol）

PicoClaw 原生支持 [MCP](https://modelcontextprotocol.io/)——连接任意 MCP 服务器，通过外部工具和数据源扩展智能体能力。

PicoClaw 是轻量级助手，因此远程 MCP 服务器是最佳选择。对于本地服务器，请优先使用原生编译的服务器二进制文件，避免使用 `npx`、`uvx` 或其他重量级依赖管理器。

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "browserbase": {
          "enabled": true,
          "deferred": false,
          "command": "",
          "type": "http",
          "url": "https://mcp.browserbase.com/mcp?browserbaseApiKey=API_KEY",
          "headers": {
            "BROWSERBASE_PROJECT_ID": "********"
          }
        }
      }
    }
  }
}
```

你也可以直接通过 CLI 管理常见的 MCP 配置，而无需手动编辑 JSON：

```bash
picoclaw mcp list
picoclaw mcp test browserbase
```

`picoclaw mcp` 是配置管理器：它会更新 `config.json` 中的 `tools.mcp.servers`，但本身不会保持服务器进程运行。

当你需要 `picoclaw mcp add` 未覆盖的高级字段时，请使用 `picoclaw mcp edit`。例如，`picoclaw mcp add` 支持 `--deferred` 和 `--env-file`，而 `picoclaw mcp edit` 仍适用于直接编辑 JSON 和不常见的 MCP 设置。

完整的 MCP 配置（stdio、SSE、HTTP 传输、工具发现）请参阅 [MCP 工具](https://afjcjsbx.github.io/picoclaw/tools/mcp.html)。CLI 用法和示例请参阅 [MCP Server CLI](https://afjcjsbx.github.io/picoclaw/reference/mcp-cli.html)。

## 加入 Agent 社交网络

通过 CLI 或任意已集成的聊天应用发送一条消息，即可将 PicoClaw 连接到 Agent 社交网络：

> 阅读 `https://clawdchat.ai/skill.md` 并按照说明加入 [ClawdChat.ai](https://clawdchat.ai)。

## CLI 参考

| 命令                      | 说明                                 |
| ------------------------- | ------------------------------------ |
| `picoclaw onboard`        | 初始化配置与工作区                   |
| `picoclaw auth weixin`    | 通过扫码连接微信账户                 |
| `picoclaw agent -m "..."` | 与智能体对话                         |
| `picoclaw agent`          | 交互式对话模式                       |
| `picoclaw gateway`        | 启动网关                             |
| `picoclaw status`         | 查看状态                             |
| `picoclaw version`        | 查看版本信息                         |
| `picoclaw model`          | 查看或切换默认模型                   |
| `picoclaw mcp list`       | 列出已配置的 MCP 服务器              |
| `picoclaw mcp add ...`    | 添加或更新 MCP 服务器条目            |
| `picoclaw mcp test`       | 探测已配置的 MCP 服务器              |
| `picoclaw mcp edit`       | 打开配置以进行高级 MCP 编辑          |
| `picoclaw mcp remove`     | 删除 MCP 服务器条目                  |
| `picoclaw cron list`      | 列出所有定时任务                     |
| `picoclaw cron add ...`   | 添加定时任务                         |
| `picoclaw cron disable`   | 禁用定时任务                         |
| `picoclaw cron remove`    | 删除定时任务                         |
| `picoclaw skills list`    | 列出已安装的 Skills                  |
| `picoclaw skills install` | 安装 Skill                           |
| `picoclaw migrate`        | 从旧版本迁移数据                     |
| `picoclaw auth login`     | 与 Provider 进行身份验证             |

### 定时任务与提醒

PicoClaw 通过 `cron` 工具支持定时提醒和重复任务：

- **一次性提醒：**“10 分钟后提醒我”→ 10 分钟后触发一次。
- **重复任务：**“每 2 小时提醒我”→ 每 2 小时触发一次。
- **Cron 表达式：**“每天上午 9 点提醒我”→ 使用 cron 表达式。

当前支持的调度类型、执行模式、命令任务门控和持久化细节请参阅 [docs/reference/cron.md](https://afjcjsbx.github.io/picoclaw/reference/cron.html)。

## 文档

本 README 之外的详细指南：

| 主题 | 说明 |
|-------|-------------|
| [Docker 与快速开始](https://afjcjsbx.github.io/picoclaw/guides/docker.html) | Docker Compose 配置、launcher/agent 模式 |
| [聊天应用](https://afjcjsbx.github.io/picoclaw/guides/chat-apps.html) | 全部 19+ 渠道配置指南 |
| [配置](https://afjcjsbx.github.io/picoclaw/guides/configuration.html) | 环境变量、工作区布局、安全沙箱 |
| [插件安装](https://afjcjsbx.github.io/picoclaw/guides/plugins.html) | 安装并激活 Agent Plugin 包 |
| [编写插件](https://afjcjsbx.github.io/picoclaw/guides/writing-a-plugin.html) | 脚手架、验证、测试并分享 Agent Plugin 包 |
| [插件 CLI](https://afjcjsbx.github.io/picoclaw/reference/plugins-cli.html) | 添加、列出、验证、搭建、测试和删除插件条目 |
| [MCP Server CLI](https://afjcjsbx.github.io/picoclaw/reference/mcp-cli.html) | 添加、列出、测试、编辑和删除 MCP 服务器条目 |
| [定时任务与 Cron 作业](https://afjcjsbx.github.io/picoclaw/reference/cron.html) | Cron 调度类型、投递模式、命令门控、任务存储 |
| [Providers 与模型](https://afjcjsbx.github.io/picoclaw/guides/providers.html) | 30+ LLM Provider、模型路由、`model_list` 配置 |
| [Spawn 与异步任务](https://afjcjsbx.github.io/picoclaw/guides/spawn-tasks.html) | 快速任务、使用 spawn 的长任务、异步子智能体编排 |
| [Hooks](https://afjcjsbx.github.io/picoclaw/architecture/hooks/) | 事件驱动 Hook 系统：观察者、拦截器、审批 Hook |
| [Steering](https://afjcjsbx.github.io/picoclaw/architecture/steering.html) | 在工具调用之间向运行中的智能体循环注入消息 |
| [SubTurn](https://afjcjsbx.github.io/picoclaw/architecture/subturn.html) | 子智能体协调、并发控制、生命周期 |
| [疑难解答](https://afjcjsbx.github.io/picoclaw/operations/troubleshooting.html) | 常见问题与解决方案 |
| [工具配置](https://afjcjsbx.github.io/picoclaw/reference/tools_configuration.html) | 工具开关、执行策略、路径策略、MCP、Skills |
| [硬件兼容性](https://afjcjsbx.github.io/picoclaw/guides/hardware-compatibility.html) | 已测试的开发板、最低要求 |

## 贡献

欢迎提交 PR！代码库刻意保持小巧和可读。

请参阅 [CONTRIBUTING.md](https://github.com/afjcjsbx/picoclaw/blob/main/CONTRIBUTING.md) 了解指南。

**PicoClaw 在开放中构建。来和我们一起构建它吧。**

[⭐ 给 PicoClaw 点星](https://github.com/afjcjsbx/picoclaw) · [💬 加入 Discord](https://discord.gg/bEuEDnKpf) · [🐛 报告问题](https://github.com/afjcjsbx/picoclaw/issues)
