# PicoClaw threat model

PicoClaw is a self-hosted personal AI assistant written in Go: chat channels feed messages into an
agent loop that calls LLM providers and tools (shell, files, Web, MCP, plugins, cron, subagents), and
the Web launcher manages configuration. It is a single-user assistant, not a multi-tenant service.

The boundary that matters is between untrusted conversation and Web input and the host the agent runs
on. PicoClaw is explicitly not a sandbox: `SECURITY.md` says the `exec` deny list cannot inspect
commands started by an allowed script, and `pkg/isolation/README.md` says process isolation is
optional, off by default, and covers child processes only. Findings should respect that advertised
model instead of restating it.

## What this project does and where untrusted input enters

- Chat channels (`pkg/channels/*`: Telegram, Discord, Slack, Matrix, WhatsApp, IRC, QQ, Feishu,
  DingTalk, WeCom, LINE, OneBot, Delta Chat, pico, and others): message text, attachments, media,
  reactions and sender identity. `allow_from` selects accepted senders; an empty list accepts every
  sender, as documented.
- Channel webhook listeners and the pico client/server transport, where the platform supports them.
- The Web launcher and dashboard (`web/backend/*`): REST handlers under `/api`, the dashboard login
  and cookie flow, WebSocket/SSE streams, and the config, model and skill endpoints that can rewrite
  configuration and install code.
- LLM providers (`pkg/providers/*`): model output is untrusted as tool input. Prompt injection also
  arrives through messages, attachments, fetched pages, MCP results, skills and tool output.
- Web tools (`pkg/tools/integration/web.go`): fetched pages, redirects, and any configured proxy. The
  SSRF guard is `utils.NewSafeDialContext` at connect time, re-applied on redirects.
- Third-party execution surfaces: MCP servers over stdio/SSE/HTTP (`pkg/mcp`), Agent Plugins 1.0.0
  (`pkg/plugins`), skills downloaded from ClawHub or GitHub (`pkg/skills`), process hooks, cron jobs
  and heartbeats.
- Durable input: `config.json`, `.security.yml`, the workspace (sessions, memory, skills, cron,
  artifacts) and environment variables.

## Components that matter most / least

Most important:

- `pkg/tools` path policy and the `exec` guard: `restrict_to_workspace` (default true),
  `allow_read_paths` / `allow_write_paths`, protected local files, deny and allow patterns, and
  `allow_remote`.
- `pkg/isolation`: `bwrap` on Linux, `sandbox-exec` on macOS, restricted token plus Job Object on
  Windows. It decides whether a child process can reach host files and the network.
- `pkg/credential` and `pkg/config`: `.security.yml` loading, `enc://` key decryption (HKDF-SHA256
  with an SSH key and a passphrase), and redaction of secret values from tool results.
- `pkg/agent`, `pkg/routing` and `pkg/session`: per-chat, per-agent and subagent isolation, turn
  profiles, and what reaches the model.
- `web/backend`: dashboard authentication and the endpoints that persist configuration or install
  code.
- `pkg/channels/*`: `allow_from` enforcement, webhook signature checks, attachment limits.
- `pkg/mcp`, `pkg/plugins`, `pkg/skills`, `pkg/cron`, `pkg/heartbeat`, `pkg/evolution`: code and
  instructions that run with the assistant's privileges.

Less important: the hardware tools (`i2c`, `spi`, `serial`) and the per-architecture build helpers;
they matter only when enabled on real devices.

## How to exercise it

- Build: `go build -tags goolm,stdjson ./...` (the image already did this).
- Unit tests: `go test -tags goolm,stdjson ./...`. Run them as the unprivileged `scanner` user:
  the failure-injection tests rely on permission errors that root does not get.
  `runuser -u scanner -- env HOME=/home/scanner go test -tags goolm,stdjson ./pkg/tools/...` is the
  shortest useful subset; `/src` and `/go` are owned by `scanner`.
- The installed binary is not on `PATH`; `go run ./cmd/picoclaw --help` works offline.
- The default home is `~/.picoclaw` (`config.json`, `.security.yml`, `workspace/`). Running the
  gateway or channels needs credentials and network, so unit tests and focused `go test` runs are the
  expected way to reach the code.
- `integration/` suites need Docker and are not expected to run offline.

## How you rate severity

- Critical: unauthenticated remote code execution or sandbox escape reachable from a network channel
  or an HTTP endpoint; reading `.security.yml` secrets without local access.
- High: authenticated remote code execution; arbitrary file read or write outside the workspace;
  bypassing `allow_from`, dashboard authentication, or the SSRF dial guard to reach internal services;
  path traversal in a Web API.
- Medium: denial of service with a crafted message, config, or attachment; secret values leaking into
  logs, session files, or model context; a weakened default that needs one documented setting to
  mitigate.
- Low: crashes without persistence, error messages that disclose paths, resource leaks.
- Prompt injection alone is informational unless it crosses one of the boundaries above, for example
  reaching files the path policy denies or sending data to an unintended destination.

## Anything to leave alone

- `tests/`, `docs/`, `examples/` and `assets/` are not production surfaces. Report them only when
  they demonstrate a defect in shipped code.
- Third-party services (LLM providers, chat platforms, ClawHub, GitHub) and the model's own safety
  behavior are out of scope.
- Documented limitations are not findings: the deny list is not a sandbox, isolation is off by
  default, an empty `allow_from` is open by design, and this is a single-user deployment.
- Generated test residue under `pkg/cron/` and build tags for embedded/hardware targets are not
  interesting on their own.
