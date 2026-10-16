# CLI Reference

`picoclaw` is the main binary: chat with the agent, start the Gateway, and manage models, credentials, channels, skills, MCP servers, and scheduled tasks from the command line. The browser-based Web UI is served by a separate binary — see the [Web UI & Launcher Guide](../guides/webui.md).

```text
picoclaw [command]
```

Every command accepts the global `--no-color` flag, which disables ANSI colors in the output. Run `picoclaw <command> --help` to see the authoritative flags for your build.

| Group | Purpose |
| --- | --- |
| [`onboard`](#onboard) | Initialize configuration and workspace |
| [`config reset`](#config-reset) | Reset configuration to factory defaults |
| [`status`](#status--version--update) / [`version`](#status--version--update) / [`update`](#status--version--update) | Inspect and update the installation |
| [`agent`](#agent) | Interact with the agent directly |
| [`gateway`](#gateway) | Start the Gateway service |
| [`model`](#model) / [`model add`](#model-add) | Show, set, and add models |
| [`auth`](#auth) | OAuth login, credentials, and QR channel setup |
| [`skills`](#skills) | Install and manage skills |
| [`cron`](#cron) | Manage scheduled tasks |
| [`mcp`](#mcp) | Manage MCP servers |
| [`migrate`](#migrate) | Migrate from OpenClaw |

## Onboard

Initializes a fresh configuration file and workspace. On first install, run this before anything else. Alias: `o`.

| Flag | Description |
| --- | --- |
| `--enc` | Enable credential encryption (generates an SSH key and prompts for a passphrase) |

```bash
picoclaw onboard
picoclaw onboard --enc
```

## Config reset

Resets the configuration to factory defaults.

| Flag | Description |
| --- | --- |
| `-f, --force` | Skip the confirmation prompt |

```bash
picoclaw config reset --force
```

## Status / version / update

```bash
picoclaw status    # Show configuration, model, and runtime status (alias: s)
picoclaw version   # Show version information (alias: v)
picoclaw update    # Check and apply updates from GitHub releases
```

`update` flags:

| Flag | Description |
| --- | --- |
| `--platform` | Target platform (default: current `GOOS`) |
| `--arch` | Target architecture (default: current `GOARCH`) |
| `-u, --url` | Direct URL to a release asset or release page |

## Agent

Interact with the agent directly. Without `-m`, it starts an interactive terminal session.

| Flag | Description |
| --- | --- |
| `-m, --message` | Send a single message (non-interactive mode) |
| `--model` | Model to use for this run |
| `-s, --session` | Session key (default `cli:default`) |
| `-d, --debug` | Enable debug logging |

```bash
picoclaw agent
picoclaw agent -m "Summarize today's cron output" --model claude-sonnet-4.6
picoclaw agent -s my-project -m "What changed in the workspace?"
```

## Gateway

Starts the long-running service that owns channels, tools, cron, and the HTTP endpoints. Alias: `g`. See the [Gateway HTTP API](gateway-api.md) for endpoints and the [Debugging guide](../operations/debug.md) for log details.

| Flag | Description |
| --- | --- |
| `-E, --allow-empty` | Continue starting even when no default model is configured |
| `-d, --debug` | Enable debug logging |
| `-T, --no-truncate` | Disable string truncation in debug logs |
| `--host` | Host address for gateway binding (overrides `gateway.host` for this run) |

```bash
picoclaw gateway
picoclaw gateway --debug
picoclaw gateway -E --host 0.0.0.0
```

## Model

Shows or changes the default model.

- `picoclaw model` — show the current default model.
- `picoclaw model <model_name>` — set the default model.
- `picoclaw model local-model` — special value for a local vLLM server (default `localhost:8000`, no API key required).

```bash
picoclaw model                      # Show current default
picoclaw model gpt-5.2              # Set default model
picoclaw model local-model          # Use a local vLLM server
```

### Model add

Adds a model entry by querying an OpenAI-compatible endpoint (`GET <api-base>/models`) and sets it as default. If `--model` is omitted, available models are listed and you pick one interactively; when provided, the entry is written without contacting the server.

| Flag | Description |
| --- | --- |
| `-b, --api-base` | API base URL (required), e.g. `https://api.openai.com/v1` |
| `-k, --api-key` | API key (required) |
| `-m, --model` | Model id; skips the interactive picker and the network call |
| `-n, --name` | Local alias written to `model_list` and used as the default model name (default `custom-prefer`) |
| `--type` | Endpoint type; only `openai-compatible` is supported today |

```bash
picoclaw model add --api-base https://api.openai.com/v1 --api-key sk-...
picoclaw model add -b http://localhost:8000/v1 -k dummy -m my-model -n local
```

## Auth

Manages credentials and channel QR flows. Providers and `model_list` setup are covered in [Providers & Model Configuration](../guides/providers.md).

| Subcommand | Purpose |
| --- | --- |
| `auth login` | Login via OAuth or paste a token |
| `auth logout` | Remove stored credentials (`-p, --provider` for one provider; empty = all) |
| `auth status` | Show current auth status |
| `auth models` | Show available models for the authenticated providers |
| `auth wecom` | Scan a WeCom QR code and configure `channels.wecom` (`--timeout`, default 5m) |
| `auth weixin` | Connect a WeChat personal account via QR code |

`auth login` flags:

| Flag | Description |
| --- | --- |
| `-p, --provider` | Provider to login with (`openai`, `anthropic`, `google-antigravity`, `antigravity`) |
| `--device-code` | Use the device code flow (for headless environments) |
| `--no-browser` | Do not auto-open a browser during OAuth login |
| `--setup-token` | Use the setup-token flow for Anthropic (from `claude setup-token`) |

```bash
picoclaw auth login --provider anthropic
picoclaw auth login --provider openai --device-code
picoclaw auth wecom
picoclaw auth weixin --timeout 300
```

## Skills

Manages installed skills, builtin skills, and registry installs. See the [Skills Authoring Guide](../guides/skills-authoring.md) for the `SKILL.md` format and [Tools Configuration](tools_configuration.md#skills-tool) for registry settings.

| Subcommand | Purpose |
| --- | --- |
| `skills list` | List installed skills |
| `skills show <name>` | Show skill details |
| `skills search [query]` | Search available skills |
| `skills install <owner>/<repo>/<path>` | Install a skill from GitHub, or `--registry <name> <slug>` from a configured registry |
| `skills install-builtin` | Install all builtin skills to the workspace |
| `skills list-builtin` | List available builtin skills |
| `skills remove <name>` | Remove an installed skill (aliases: `rm`, `uninstall`) |

```bash
picoclaw skills list
picoclaw skills install sipeed/picoclaw-skills/weather
picoclaw skills install --registry clawhub github
```

## Cron

Manages scheduled tasks that deliver agent-generated messages to a channel. Job behavior, delivery modes, and storage are documented in [Scheduled Tasks and Cron Jobs](cron.md).

| Subcommand | Purpose |
| --- | --- |
| `cron add` | Add a new scheduled job |
| `cron list` | List all scheduled jobs |
| `cron enable <id>` / `cron disable <id>` | Enable or disable a job |
| `cron remove <id>` | Remove a job by ID |

`cron add` flags:

| Flag | Description |
| --- | --- |
| `-n, --name` | Job name |
| `-m, --message` | Message for the agent |
| `-c, --cron` | Cron expression (e.g. `'0 9 * * *'`) |
| `-e, --every` | Run every N seconds |
| `--channel` | Channel for delivery |
| `--to` | Recipient for delivery |

```bash
picoclaw cron add --name "Morning brief" --message "Summarize my calendar" --cron "0 9 * * *" --channel telegram --to 123456789
picoclaw cron list
picoclaw cron disable 1
```

## MCP

Manages MCP server entries in `config.json`. The dedicated workflow with examples lives in [MCP Server CLI](mcp-cli.md).

| Subcommand | Purpose |
| --- | --- |
| `mcp add <name> <command-or-url> [args...]` | Add or update an MCP server |
| `mcp list` | List configured servers (`--status` pings enabled servers) |
| `mcp show <name>` | Show details and tools for a server |
| `mcp test <name>` | Test connectivity |
| `mcp edit` | Open the PicoClaw config in `$EDITOR` |
| `mcp remove <name>` | Remove a server from config |
| `mcp login <name>` / `mcp logout <name>` | Manage browser OAuth credentials for a server |

`mcp add` highlights: `-t, --transport` (`stdio`, `http`/`streamable-http`, `sse`; default `stdio`), `-e, --env KEY=value` (repeatable), `--env-file`, `-H, --header`, `--inherit-env`, `--deferred`/`--no-deferred`, `-f, --force`.

```bash
picoclaw mcp add filesystem npx -y @modelcontextprotocol/server-filesystem ~/notes
picoclaw mcp add remote https://example.com/mcp --transport http
picoclaw mcp list --status
```

## Migrate

Migrates configuration and workspace files from OpenClaw into PicoClaw.

| Flag | Description |
| --- | --- |
| `--from` | Source to migrate from (default `openclaw`) |
| `--source-home` | Override source home directory (default: `~/.openclaw`) |
| `--target-home` | Override target home directory (default: `~/.picoclaw`) |
| `--dry-run` | Show what would be migrated without making changes |
| `--config-only` / `--workspace-only` | Migrate only config or only workspace files |
| `--refresh` | Re-sync workspace files from OpenClaw (repeatable) |
| `--force` | Skip confirmation prompts |

```bash
picoclaw migrate --dry-run
picoclaw migrate --from openclaw
```
