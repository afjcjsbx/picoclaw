<div align="center">

<img src="assets/logo.webp" alt="PicoClaw" width="200">

<h1>PicoClaw</h1>

**The featherweight AI agent that fits anywhere.**

_Small on purpose. Small is not lesser. Small is enough._

<p>
  <img src="https://img.shields.io/badge/Go-1.27.4+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go 1.27.4+">
  <img src="https://img.shields.io/badge/Platforms-x86__64%20%7C%20ARM64%20%7C%20MIPS%20%7C%20RISC--V%20%7C%20LoongArch-blue" alt="Supported platforms">
  <img src="https://img.shields.io/badge/License-MIT-green" alt="MIT License">
  <img src="https://img.shields.io/github/v/release/afjcjsbx/picoclaw?style=flat&label=release" alt="Latest release">
  <img src="https://img.shields.io/github/stars/afjcjsbx/picoclaw?style=flat&label=stars" alt="GitHub stars">
  <br>
  <a href="https://afjcjsbx.github.io/picoclaw/"><img src="https://img.shields.io/badge/Docs-Read-007acc?style=flat&logo=read-the-docs&logoColor=white" alt="Documentation"></a>
  <a href="https://discord.gg/XTt5Y6cJfY"><img src="https://img.shields.io/badge/Discord-Join%20the%20community-5865F2?style=flat&logo=discord&logoColor=white" alt="Discord"></a>
  <a href="https://deepwiki.com/afjcjsbx/picoclaw"><img src="https://img.shields.io/badge/Wiki-DeepWiki-FFA500?style=flat&logo=wikipedia&logoColor=white" alt="DeepWiki"></a>
</p>

<p>
  <strong>English</strong> |
  <a href="docs/project/README.zh.md">中文</a>
</p>

</div>

---

<p align="center">
  <a href="https://discord.gg/XTt5Y6cJfY">
    <img src="assets/discord-banner.png" alt="PicoClaw on Discord — meet builders, share ideas, get help" width="80%">
  </a>
</p>

## What is PicoClaw?

**PicoClaw** is an ultra-lightweight personal AI assistant written entirely in **Go** — not a fork of OpenClaw, NanoBot, or any other agent framework. Inspired by [NanoBot](https://github.com/HKUDS/nanobot), it was rebuilt from the ground up through a "self-bootstrapping" process: the agent itself drove the architecture migration and code optimization.

This repository is the actively maintained, community-driven continuation of [`sipeed/picoclaw`](https://github.com/sipeed/picoclaw), with ongoing fixes, dependency updates, and new features.

### Maintenance status

| | Upstream `sipeed/picoclaw` | This fork |
| --- | --- | --- |
| Latest release | [![upstream release](https://img.shields.io/github/v/release/sipeed/picoclaw?label=release&color=blue)](https://github.com/sipeed/picoclaw/releases) | [![fork release](https://img.shields.io/github/v/release/afjcjsbx/picoclaw?label=release&color=green)](https://github.com/afjcjsbx/picoclaw/releases) |
| Last commit on `main` | [![upstream last commit](https://img.shields.io/github/last-commit/sipeed/picoclaw/main?label=last%20commit)](https://github.com/sipeed/picoclaw/commits/main) | [![fork last commit](https://img.shields.io/github/last-commit/afjcjsbx/picoclaw/main?label=last%20commit)](https://github.com/afjcjsbx/picoclaw/commits/main) |

The badges are rendered by shields.io from GitHub, so the table updates automatically. Upstream still receives issue and pull-request activity; the badges describe release and `main`-branch activity, not whether the upstream project is archived.

See [maintenance status](docs/project/maintenance-status.md) for the current comparison and [migration notes](docs/migration/from-sipeed-picoclaw.md) if you are switching from upstream.

**Runs on $10 hardware. Uses 10–20 MB of RAM. Boots in under a second.**

**Don't let your old hardware gather dust in the basement — give it a second life with PicoClaw!**

## Features

| | |
|:--|:--|
| 🪶 **Featherweight**<br>10–20 MB of RAM in recent builds — small enough for a $10 board. | ⚡ **Instant startup**<br>Ready in under a second, even on a 0.6 GHz single-core processor. |
| 💰 **Minimal cost**<br>Runs on hardware that costs 98% less than a Mac mini. | 🌍 **One binary everywhere**<br>RISC-V, ARM, MIPS, x86-64, and LoongArch — the same single executable. |
| 🔌 **MCP native**<br>Connect any [Model Context Protocol](https://modelcontextprotocol.io/) server to extend the agent. | 🧩 **Agent Plugins 1.0.0**<br>Package skills and MCP servers into portable, installable plugins. |
| 👁️ **Vision pipeline**<br>Send images and files directly to multimodal LLMs with automatic base64 encoding. | 🧠 **Smart routing**<br>Rule-based model routing sends simple queries to cheaper, faster models. |
| 💬 **19+ chat channels**<br>Telegram, Discord, Slack, WhatsApp, Matrix, WeChat, and many more. | 🔗 **30+ providers**<br>OpenAI, Anthropic, Gemini, OpenRouter, DeepSeek, local models, and more. |

### How it compares

|                                | OpenClaw                                                     | NanoBot                                                                     | **PicoClaw**                                                                                 |
| ------------------------------ | ------------------------------------------------------------ | --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **Language**                   | TypeScript                                                   | Python                                                                      | **Go**                                                                                        |
| **RAM reference**              | [2 GB host RAM recommended](https://docs.openclaw.ai/install/fly) | [~200–600 MB reported](https://github.com/HKUDS/nanobot/issues/3410)        | **10–20 MB in recent builds**                                                                 |
| **Single executable**          | No (Node.js app)                                             | No (Python app with native TUI component)                                   | **[48.1 MiB](docs/guides/custom-builds.md#measured-binary-sizes)**                            |
| **Boot time**</br>(0.8 GHz core) | >500 s                                                       | >30 s                                                                       | **<1 s**                                                                                      |
| **Cost**                       | Mac mini $599                                                | Most Linux boards ~$50                                                      | **Any Linux board**</br>**from $10**                                                          |

> These are reference figures, not a controlled benchmark. OpenClaw's value is a deployment recommendation, NanoBot's is a single Raspberry Pi 4 user report, and PicoClaw's is the estimate for recent builds. OpenClaw and NanoBot do not ship the complete agent as one executable; PicoClaw's size is the measured full default macOS ARM64 build — see the [measurement method and other profiles](docs/guides/custom-builds.md#measured-binary-sizes).

### Runtime footprint

| Tool | Runtime | Install size | First paint / ready | Idle RSS | Processes |
| --- | --- | ---: | ---: | ---: | :---: |
| **PicoClaw** (core) | Go 1.27, single static binary | **48.1 MiB** | **≈66 ms** | **37.9 MiB** | **1** |
| Claude Code 2.1.300 | Bun-compiled TS (native binary) | 240 MiB | ≈263 ms | ≈184 MiB | 1 |
| pi 1.2.0 | Bun-compiled TS + pi-tui | 148 MiB | ≈369 ms | ≈169 MiB | 1 |
| OpenCode 2.0.29 | Bun + OpenTUI | 239 MiB | ≈190 ms | ≈200 MiB | 1 |
| Hermes 0.22.1 | Python + TUI | ≈1.9 GiB | ≈3.2 s | ≈214 MiB | 2 |

PicoClaw's idle RSS is 37.9 MiB, with a memory footprint of **≈19 MiB** (`phys_footprint`, what Activity Monitor reports) — the metric behind the "10–20 MB" figure above. Its core ships no full-screen TUI, so "first paint / ready" is the time for the gateway to answer `GET /ready`. Lighter channel profiles are listed in [measured binary sizes](docs/guides/custom-builds.md#measured-binary-sizes).

<details>
<summary><b>Benchmark methodology</b></summary>

Measured on an Apple M1 Max (macOS arm64) with the scripts in [`scripts/`](scripts/). PicoClaw's figures use Go 1.27.3 and were refreshed on 2026-10-26; the other tools were measured on 2026-10-15. **First paint / ready**, **idle RSS**, and **processes** come from the scripts; **install size** is the size of each tool's own installation and is not measured by these scripts. **Idle RSS** is the resident set summed across the process tree once it is idle. The other agents are interactive TUIs and their figures are single-machine medians of a few runs, so expect drift between releases.

Reproduce the runtime columns with:

```bash
python3 scripts/bench-picoclaw-runtime.py --binary ./build/picoclaw --runs 15
python3 scripts/bench-agent-tui.py --cmd opencode --runs 3 --keys '\x03'
```

</details>

> **[Hardware Compatibility List](docs/guides/hardware-compatibility.md)** — Tested boards from $5 RISC-V devices to Raspberry Pi and Android phones. Your board is missing? Submit a PR!

## Install

### One-line install (Linux & macOS)

```sh
curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | sh
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.ps1 | iex
```

The installer detects the CPU architecture, verifies the release checksum, installs both binaries, and starts the launcher. The Web UI opens at `http://localhost:18800`; on first run, create the dashboard password in the browser.

### Precompiled binaries

Download the binary for your platform from [GitHub Releases](https://github.com/afjcjsbx/picoclaw/releases).

### Build from source

Prerequisites:

- Go 1.27.4+
- Node.js 22+ and pnpm 10.33.0+ for Web UI and launcher builds

```bash
git clone https://github.com/afjcjsbx/picoclaw.git
cd picoclaw

make deps

# Install frontend dependencies
(cd web/frontend && pnpm install --frozen-lockfile)

# Build the core binary for the current platform
make build

# Build the Web UI Launcher (required for Web UI mode)
make build-launcher

# Build core binaries for all Makefile-managed platforms
make build-all

# Build a single target (used by the CI matrix)
make build-target TARGET=linux/arm64
make build-target TARGET=linux/arm GOARM=7

# Build for Raspberry Pi Zero 2 W
make build-pi-zero

# Build and install
make install
```

**Raspberry Pi Zero 2 W:** use the binary that matches your OS — 32-bit Raspberry Pi OS: `make build-linux-arm`; 64-bit: `make build-linux-arm64`. Or run `make build-pi-zero` to build both.

<details>
<summary><b>Custom channel builds</b></summary>

The default build includes every channel. Custom channel selection, optional Seahorse builds, SQLite caveats, and measured binary sizes are documented in the [Custom Builds Guide](docs/guides/custom-builds.md). For example, this builds with Telegram only:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram'
```

[Seahorse](docs/architecture/seahorse-memory.md) is PicoClaw's built-in memory system: the `seahorse` context manager keeps a local SQLite database with an FTS5 index over the session history, so the agent can recall and compact older context. To shrink the footprint further, build without it using the `no_seahorse` tag, which also drops the SQLite dependency when no other selected feature needs it:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram,no_seahorse'
```

That is the right choice when an external system provides memory instead, for example the [Mem0 plugin](docs/guides/plugins.md#mem0). In the measured macOS ARM64 Telegram-only build, adding `no_seahorse` reduced the binary by 4.00 MiB (10.0%). See the guide for full measurement details and the cases where Matrix or WhatsApp Native still require SQLite.

</details>

## Quick start

### Web UI Launcher (recommended)

The Web UI Launcher provides a browser-based interface for configuration and chat. It is the easiest way to get started — no command-line knowledge required.

**Option 1: double-click (desktop)**

After downloading from [GitHub Releases](https://github.com/afjcjsbx/picoclaw/releases), double-click `picoclaw-launcher` (or `picoclaw-launcher.exe` on Windows). Your browser opens automatically at `http://localhost:18800`.

**Option 2: command line**

```bash
picoclaw-launcher
# Open http://localhost:18800 in your browser
```

> [!TIP]
> **Remote access / Docker / VM:** add the `-public` flag to listen on all interfaces:
>
> ```bash
> picoclaw-launcher -public
> ```

**Getting started:** open the Web UI, then **1)** configure a provider (add your LLM API key) → **2)** configure a channel (e.g. Telegram) → **3)** start the gateway → **4)** chat.

For detailed documentation, see the [Web UI & Launcher Guide](docs/guides/webui.md).

<details>
<summary><b>Docker (alternative)</b></summary>

```bash
# 1. Clone this repo
git clone https://github.com/afjcjsbx/picoclaw.git
cd picoclaw

# 2. First run — auto-generates docker/data/config.json then exits
#    (only triggers when both config.json and workspace/ are missing)
docker compose -f docker/docker-compose.yml --profile launcher up
# The container prints "First-run setup complete." and stops.

# 3. Set your API keys
vim docker/data/config.json

# 4. Start
docker compose -f docker/docker-compose.yml --profile launcher up -d
# Open http://localhost:18800
```

> **Docker / VM users:** the gateway listens on `127.0.0.1` by default. Set `PICOCLAW_GATEWAY_HOST=0.0.0.0` or use the `-public` flag to make it accessible from the host.

```bash
# Check logs
docker compose -f docker/docker-compose.yml logs -f

# Stop
docker compose -f docker/docker-compose.yml --profile launcher down

# Update
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml --profile launcher up -d
```

</details>

<details>
<summary><b>macOS: first-launch security warning</b></summary>

macOS may block `picoclaw-launcher` on first launch because it is downloaded from the internet and not notarized through the Mac App Store.

1. Double-click `picoclaw-launcher`. macOS shows a security warning: *"picoclaw-launcher" Not Opened — Apple could not verify "picoclaw-launcher" is free of malware that may harm your Mac or compromise your privacy.*
2. Open **System Settings → Privacy & Security**, scroll to the **Security** section, click **Open Anyway**, then confirm **Open Anyway** in the dialog.

After this one-time step, `picoclaw-launcher` opens normally on subsequent launches.

</details>

## Providers

<details>
<summary><b>30+ supported LLM providers and configuration</b></summary>

PicoClaw supports 30+ LLM providers through the `model_list` configuration. Use the `protocol/model` format:

| Provider | Protocol | API Key | Notes |
|----------|----------|---------|-------|
| [OpenAI](https://platform.openai.com/api-keys) | `openai/` | Required | GPT-5.4, GPT-4o, o3, etc. |
| [Anthropic](https://console.anthropic.com/settings/keys) | `anthropic/` | Required | Claude Opus 4.6, Sonnet 4.6, etc. |
| [Google Gemini](https://aistudio.google.com/apikey) | `gemini/` | Required | Gemini 3 Flash, 2.5 Pro, etc. |
| [OpenRouter](https://openrouter.ai/keys) | `openrouter/` | Required | 200+ models, unified API |
| [Zhipu (GLM)](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) | `zhipu/` | Required | GLM-4.7, GLM-5, etc. |
| [DeepSeek](https://platform.deepseek.com/api_keys) | `deepseek/` | Required | DeepSeek-V3, DeepSeek-R1 |
| [Volcengine](https://console.volcengine.com) | `volcengine/` | Required | Doubao, Ark models |
| [Qwen](https://dashscope.console.aliyun.com/apiKey) | `qwen/` | Required | Qwen3, Qwen-Max, etc. |
| [Groq](https://console.groq.com/keys) | `groq/` | Required | Fast inference (Llama, Mixtral) |
| [Moonshot (Kimi)](https://platform.moonshot.cn/console/api-keys) | `moonshot/` | Required | Kimi models |
| [Minimax](https://platform.minimaxi.com/user-center/basic-information/interface-key) | `minimax/` | Required | MiniMax models |
| [Mistral](https://console.mistral.ai/api-keys) | `mistral/` | Required | Mistral Large, Codestral |
| [NVIDIA NIM](https://build.nvidia.com/) | `nvidia/` | Required | NVIDIA hosted models |
| [Cerebras](https://cloud.cerebras.ai/) | `cerebras/` | Required | Fast inference |
| [NEAR AI Cloud](https://near.ai/) | `nearai/` | Required | TEE inference, OpenAI-compatible |
| [Novita AI](https://novita.ai/) | `novita/` | Required | Various open models |
| [Xiaomi MiMo](https://platform.xiaomimimo.com/) | `mimo/` | Required | MiMo models |
| [Ollama](https://ollama.com/) | `ollama/` | Not needed | Local models, self-hosted |
| [vLLM](https://docs.vllm.ai/) | `vllm/` | Not needed | Local deployment, OpenAI-compatible |
| [LiteLLM](https://docs.litellm.ai/) | `litellm/` | Varies | Proxy for 100+ providers |
| [Azure OpenAI](https://portal.azure.com/) | `azure/` | API key or Entra ID** | Enterprise Azure deployment |
| [GitHub Copilot](https://github.com/features/copilot) | `github-copilot/` | OAuth | Device code login |
| [Antigravity](https://console.cloud.google.com/) | `antigravity/` | OAuth | Google Cloud AI |
| [AWS Bedrock](https://console.aws.amazon.com/bedrock)* | `bedrock/` | AWS credentials | Claude, Llama, Mistral on AWS |

> \* AWS Bedrock requires build tag: `go build -tags bedrock`. Set `api_base` to a region name (e.g. `us-east-1`) for automatic endpoint resolution across all AWS partitions (aws, aws-cn, aws-us-gov). When using a full endpoint URL instead, you must also configure `AWS_REGION` via environment variable or AWS config/profile.
>
> \*\* Azure OpenAI uses `api_key` when set. If `api_key` is omitted, the provider falls back to Microsoft Entra ID via `DefaultAzureCredential` (env vars, workload identity, managed identity, Azure CLI, etc.). The Entra ID path requires build tag: `go build -tags azidentity`.

<details>
<summary><b>Local deployment (Ollama, vLLM, etc.)</b></summary>

**Ollama:**

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

**vLLM:**

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

For full provider configuration details, see [Providers & Models](docs/guides/providers.md).

</details>

</details>

## Channels

<details>
<summary><b>19+ chat platforms and setup guides</b></summary>

Talk to your PicoClaw through 19+ messaging platforms:

| Channel | Setup | Protocol | Docs |
|---------|-------|----------|------|
| **Telegram** | Easy (bot token) | Long polling | [Guide](docs/channels/telegram/README.md) |
| **Discord** | Easy (bot token + intents) | WebSocket | [Guide](docs/channels/discord/README.md) |
| **WhatsApp** | Easy (QR scan or bridge URL) | Native / Bridge | [Guide](docs/guides/chat-apps.md#whatsapp) |
| **Weixin** | Easy (Native QR scan) | iLink API | [Guide](docs/guides/chat-apps.md#weixin) |
| **QQ** | Easy (AppID + AppSecret) | WebSocket | [Guide](docs/channels/qq/README.md) |
| **Slack** | Easy (bot + app token) | Socket Mode | [Guide](docs/channels/slack/README.md) |
| **Matrix** | Medium (homeserver + token) | Sync API | [Guide](docs/channels/matrix/README.md) |
| **Delta Chat** | Easy (chatmail or existing account) | JSON-RPC (email/E2EE) | [Guide](docs/channels/deltachat/README.md) |
| **DingTalk** | Medium (client credentials) | Stream | [Guide](docs/channels/dingtalk/README.md) |
| **Feishu / Lark** | Medium (App ID + Secret) | WebSocket/SDK | [Guide](docs/channels/feishu/README.md) |
| **LINE** | Medium (credentials + webhook) | Webhook | [Guide](docs/channels/line/README.md) |
| **WeCom** | Easy (QR login or manual) | WebSocket | [Guide](docs/channels/wecom/README.md) |
| **VK** | Easy (group token) | Long Poll | [Guide](docs/channels/vk/README.md) |
| **IRC** | Medium (server + nick) | IRC protocol | [Guide](docs/guides/chat-apps.md#irc) |
| **OneBot** | Medium (WebSocket URL) | OneBot v11 | [Guide](docs/channels/onebot/README.md) |
| **MQTT** | Easy (broker + agent_id) | MQTT pub/sub | [Guide](docs/channels/mqtt/README.md) |
| **MaixCam** | Easy (enable) | TCP socket | [Guide](docs/channels/maixcam/README.md) |
| **Pico** | Easy (enable) | Native protocol | Built-in |
| **Pico Client** | Easy (WebSocket URL) | WebSocket | Built-in |

> All webhook-based channels share a single Gateway HTTP server (`gateway.host`:`gateway.port`, default `127.0.0.1:18790`). Feishu uses WebSocket/SDK mode and does not use the shared HTTP server.

> Log verbosity is controlled by `gateway.log_level` (default: `warn`). Supported values: `debug`, `info`, `warn`, `error`, `fatal`. It can also be set via `PICOCLAW_LOG_LEVEL`. See [Configuration](docs/guides/configuration.md#gateway-log-level) for details.

For detailed channel setup instructions, see [Chat Apps Configuration](docs/guides/chat-apps.md).

</details>

## Tools

### Web search

<details>
<summary><b>Supported search engines and free tiers</b></summary>

PicoClaw can search the web to provide up-to-date information. Configure it in `tools.web`:

| Search Engine | API Key | Free Tier | Link |
|--------------|---------|-----------|------|
| DuckDuckGo | Not needed | Unlimited | Built-in fallback |
| [Gemini Google Search](https://aistudio.google.com/apikey) | Required | Varies | Gemini with Google Search grounding |
| [Baidu Search](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5) | Required | 1500/month (daily allocation) | AI-powered, China-optimized |
| [Tavily](https://tavily.com) | Required | 1000 queries/month | Optimized for AI agents |
| [Brave Search](https://brave.com/search/api) | Required | 2000 queries/month | Fast and private |
| [Kagi Search](https://help.kagi.com/kagi/api/search.html) | Required | Paid/limited by API setup | Premium search results |
| [Keenable](https://keenable.ai) | Optional | Keyless: 10 req/s, 1000/hour per IP | Works without a key; a key lifts the rate limits |
| [Perplexity](https://www.perplexity.ai) | Required | Paid | AI-powered search |
| [SearXNG](https://github.com/searxng/searxng) | Not needed | Self-hosted | Free metasearch engine |
| [GLM Search](https://open.bigmodel.cn/) | Required | Varies | Zhipu web search |

</details>

### Other tools

PicoClaw includes built-in tools for file operations, code execution, scheduling, and more. See [Tools](docs/tools/README.md) for details.

## Skills

Skills are modular capabilities that extend your agent. They are loaded from `SKILL.md` files in your workspace.

**Install skills from ClawHub:**

```bash
picoclaw skills search "web scraping"
picoclaw skills install <skill-name>
```

**Configure skill registries** under `tools.skills.registries`:

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

The ClawHub token is optional and only raises the rate limit. The GitHub token is needed for searches and for private repositories. Pass them via `PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_AUTH_TOKEN` and `PICOCLAW_SKILLS_REGISTRIES_GITHUB_AUTH_TOKEN`.

Skills and MCP servers can also be packaged together as portable **Agent Plugins 1.0.0**. See the [plugin guide](docs/guides/plugins.md) and the [working demo plugin](examples/plugins/demo-plugin). Mem0 publishes its own [Agent Plugin](https://github.com/mem0ai/mem0/tree/main/integrations/mem0-agent-plugin); read the [compatibility notes](docs/guides/plugins.md#mem0) before enabling it.

For more details, see [Skills tools](docs/tools/skills.md).

## MCP (Model Context Protocol)

PicoClaw natively supports [MCP](https://modelcontextprotocol.io/) — connect any MCP server to extend your agent's capabilities with external tools and data sources.

PicoClaw is a lightweight assistant, so remote MCP servers are the best choice. For local servers, prefer natively compiled server binaries and avoid `npx`, `uvx`, or other heavyweight dependency managers.

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

You can manage common MCP setups directly from the CLI instead of editing JSON by hand:

```bash
picoclaw mcp list
picoclaw mcp test browserbase
```

`picoclaw mcp` is a configuration manager: it updates `config.json` under `tools.mcp.servers`, but it does not keep the server process running itself.

Use `picoclaw mcp edit` when you need advanced fields that are not covered by `picoclaw mcp add`. For example, `picoclaw mcp add` supports `--deferred` and `--env-file`, while `picoclaw mcp edit` is still useful for direct JSON editing and uncommon MCP settings.

For full MCP configuration (stdio, SSE, HTTP transports, tool discovery), see [MCP tools](docs/tools/mcp.md). For CLI usage and examples, see [MCP Server CLI](docs/reference/mcp-cli.md).

## Join the Agent Social Network

Connect PicoClaw to the Agent Social Network by sending a single message via the CLI or any integrated chat app:

> Read `https://clawdchat.ai/skill.md` and follow the instructions to join [ClawdChat.ai](https://clawdchat.ai).

## CLI reference

| Command                   | Description                          |
| ------------------------- | ------------------------------------ |
| `picoclaw onboard`        | Initialize config and workspace      |
| `picoclaw auth weixin`    | Connect a WeChat account via QR      |
| `picoclaw agent -m "..."` | Chat with the agent                  |
| `picoclaw agent`          | Interactive chat mode                |
| `picoclaw gateway`        | Start the gateway                    |
| `picoclaw status`         | Show status                          |
| `picoclaw usage`          | Show token usage and cost            |
| `picoclaw version`        | Show version info                    |
| `picoclaw model`          | View or switch the default model     |
| `picoclaw mcp list`       | List configured MCP servers          |
| `picoclaw mcp add ...`    | Add or update an MCP server entry    |
| `picoclaw mcp test`       | Probe a configured MCP server        |
| `picoclaw mcp edit`       | Open config for advanced MCP editing |
| `picoclaw mcp remove`     | Remove an MCP server entry           |
| `picoclaw cron list`      | List all scheduled jobs              |
| `picoclaw cron add ...`   | Add a scheduled job                  |
| `picoclaw cron disable`   | Disable a scheduled job              |
| `picoclaw cron remove`    | Remove a scheduled job               |
| `picoclaw skills list`    | List installed skills                |
| `picoclaw skills install` | Install a skill                      |
| `picoclaw migrate`        | Migrate data from older versions     |
| `picoclaw auth login`     | Authenticate with providers          |

### Chat commands

Slash commands work in interactive CLI mode and in connected chat channels:

| Command | Description |
| --- | --- |
| `/start` | Start the bot. |
| `/help [command]` | Show all commands or detailed help for one command. |
| `/stop` | Stop the current task. |
| `/show [model\|channel\|agents\|mcp <server>]` | Show current configuration. |
| `/list [model\|models\|channels\|agents\|skills\|mcp\|plugins]` | List available options. |
| `/switch model <index\|name>` | Switch the active model. |
| `/check channel <name>` | Check whether a channel is available. |
| `/context` | Show session context and token usage. |
| `/usage` (alias `/cost`) | Show token usage and cost for this session, split by model and cache category. |
| `/compact [--force] [focus]` | Compact conversation context, optionally focusing the summary. |
| `/clear` (alias `/new`) | Clear the chat history. |
| `/subagents` | Show running tasks and subagents in this chat. |
| `/use <skill> [message]` | Force an installed skill for one request. |
| `/voice [off\|on\|tts\|status]` | Set automatic text-to-speech mode. |
| `/reload` | Reload the configuration file. |
| `/<name> [arguments]` | Run a [custom prompt command](docs/guides/custom-commands.md) from the workspace or global `commands/` directory. |

Use `/help <command>` for usage details. Availability depends on the channel; unsupported commands return an explicit error.

### Custom prompt commands

You can build your own slash commands as Markdown files. Workspace commands live in `<workspace>/commands/`; global commands live in `~/.picoclaw/commands/`. Nested directories become nested command names, so `commands/team/review.md` becomes `/team/review`.

```md
---
description: Review code for correctness
---

Review $ARGUMENTS for bugs and missing tests.
```

Invoke it like any other command:

```text
/review src/auth.ts
```

| Template feature | Behavior |
| --- | --- |
| `$ARGUMENTS` | Inserts the complete argument string as entered, including quotes. |
| `$1`, `$2`, ... | Inserts positional arguments; quotes group words, and the highest placeholder consumes the rest. |
| No placeholder | Appends non-empty arguments after a blank line. |
| `description` frontmatter | Shown in `/help` and the Web UI command palette. |

Workspace commands override global commands with the same name, while built-in commands always win. Custom commands appear in `/help` on every channel; the Web UI, Telegram, and Discord also list compatible names in their command menus. See [Custom Prompt Commands](docs/guides/custom-commands.md) for precedence, examples, and limitations.

### Scheduled tasks and reminders

PicoClaw supports scheduled reminders and recurring tasks through the `cron` tool:

- **One-time reminders:** "Remind me in 10 minutes" → triggers once after 10 minutes.
- **Recurring tasks:** "Remind me every 2 hours" → triggers every 2 hours.
- **Cron expressions:** "Remind me at 9am daily" → uses a cron expression.

See [docs/reference/cron.md](docs/reference/cron.md) for current schedule types, execution modes, command-job gates, and persistence details.

## Documentation

For detailed guides beyond this README:

| Topic | Description |
|-------|-------------|
| [Docker & Quick Start](docs/guides/docker.md) | Docker Compose setup, launcher/agent modes |
| [Web UI & Launcher](docs/guides/webui.md) | Browser-based configuration, chat, remote access, tray, and logs |
| [Chat Apps](docs/guides/chat-apps.md) | All 19+ channel setup guides |
| [Configuration](docs/guides/configuration.md) | Environment variables, workspace layout, security sandbox |
| [Plugin Installation](docs/guides/plugins.md) | Install and activate Agent Plugin packages |
| [Writing a Plugin](docs/guides/writing-a-plugin.md) | Scaffold, validate, test, and share an Agent Plugin package |
| [Plugin CLI](docs/reference/plugins-cli.md) | Add, list, validate, scaffold, test, and remove plugin entries |
| [MCP Server CLI](docs/reference/mcp-cli.md) | Add, list, test, edit, and remove MCP server entries |
| [Scheduled Tasks and Cron Jobs](docs/reference/cron.md) | Cron schedule types, deliver modes, command gates, job storage |
| [Providers & Models](docs/guides/providers.md) | 30+ LLM providers, model routing, `model_list` configuration |
| [Spawn & Async Tasks](docs/guides/spawn-tasks.md) | Quick tasks, long tasks with spawn, async sub-agent orchestration |
| [Hooks](docs/architecture/hooks/) | Event-driven hook system: observers, interceptors, approval hooks |
| [Steering](docs/architecture/steering.md) | Inject messages into a running agent loop between tool calls |
| [SubTurn](docs/architecture/subturn.md) | Subagent coordination, concurrency control, lifecycle |
| [Troubleshooting](docs/operations/troubleshooting.md) | Common issues and solutions |
| [Tools Configuration](docs/reference/tools_configuration.md) | Tool switches, exec policies, path policies, MCP, skills |
| [Hardware Compatibility](docs/guides/hardware-compatibility.md) | Tested boards, minimum requirements |
| [Maintenance Status](docs/project/maintenance-status.md) | Upstream vs fork release dates and migration path |
| [Migrating from sipeed/picoclaw](docs/migration/from-sipeed-picoclaw.md) | Back up, install, and verify the fork |

## Contributing

PRs are welcome! The codebase is intentionally small and readable.

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

**PicoClaw is built in the open. Come build it with us.**

[⭐ Star PicoClaw](https://github.com/afjcjsbx/picoclaw) · [💬 Join Discord](https://discord.gg/XTt5Y6cJfY) · [🐛 Report an issue](https://github.com/afjcjsbx/picoclaw/issues)
