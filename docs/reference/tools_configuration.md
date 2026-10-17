# Tools Configuration

PicoClaw's tool settings live under the `tools` field of `config.json`. This page is the reference for global tool behavior, path policy, and the master tool-switch table. Every tool family has a dedicated page under [docs/tools](../tools/README.md).

## Tool switches

Every built-in tool is enabled or disabled individually through `tools.<key>.enabled` unless noted otherwise. Most switches also accept an environment override following the pattern `PICOCLAW_TOOLS_<KEY>_ENABLED` (for example `PICOCLAW_TOOLS_EXEC_ENABLED=false`); the exception is `read_file`, whose settings are JSON-only.

| Tool | Switch | Default | Notes |
|------|--------|---------|-------|
| `read_file` | `tools.read_file.enabled` | `true` | Mode and size limit under `tools.read_file`; see [Filesystem tools](../tools/filesystem.md) |
| `write_file` | `tools.write_file.enabled` | `true` | [Filesystem tools](../tools/filesystem.md) |
| `edit_file` | `tools.edit_file.enabled` | `true` | [Filesystem tools](../tools/filesystem.md) |
| `append_file` | `tools.append_file.enabled` | `true` | [Filesystem tools](../tools/filesystem.md) |
| `list_dir` | `tools.list_dir.enabled` | `true` | [Filesystem tools](../tools/filesystem.md) |
| `search_files` | `tools.search_files.enabled` | `true` | [Filesystem tools](../tools/filesystem.md) |
| `load_image` | `tools.load_image.enabled` | `true` | [Media tools](../tools/media.md) |
| `send_file` | `tools.send_file.enabled` | `true` | [Media tools](../tools/media.md) |
| `send_tts` | `tools.send_tts.enabled` | `false` | Registered only when a TTS provider is configured; see [Media tools](../tools/media.md) |
| `image_generate` | `tools.image_generate.enabled` | `false` | Needs an image model under `tools.image_generate.model_name`; see [Image generation](../tools/image-generation.md) |
| `web_search` | `tools.web.enabled` | `true` | Search providers and settings under `tools.web`; see [Web tools](../tools/web.md) |
| `web_fetch` | `tools.web_fetch.enabled` | `true` | Fetch settings under `tools.web`; see [Web tools](../tools/web.md) |
| `exec` | `tools.exec.enabled` | `true` | See [Exec tool](../tools/exec.md) |
| `cron` | `tools.cron.enabled` | `true` | See [Cron tool](../tools/cron.md) |
| `todo` | `tools.todo.enabled` | `true` | See [Todo planning tool](../tools/todo.md) |
| `message` | `tools.message.enabled` | `true` | Media attachments gated by `tools.message.media_enabled` (`false`); see [Messaging tools](../tools/messaging.md) |
| `reaction` | — | always on | No switch; fails gracefully on channels without reaction support |
| `spawn` | `tools.spawn.enabled` | `true` | Requires `tools.subagent.enabled`; see [Subagent tools](../tools/subagents.md) |
| `subagent` | `tools.subagent.enabled` | `true` | The sync `subagent` tool also requires `spawn` or `spawn_status` to be enabled |
| `spawn_status` | `tools.spawn_status.enabled` | `false` | Requires `tools.subagent.enabled` |
| `delegate` | — | multi-agent setups | Registered automatically when more than one agent exists |
| `find_skills` | `tools.find_skills.enabled` | `true` | Requires `tools.skills.enabled`; see [Skills tools](../tools/skills.md) |
| `install_skill` | `tools.install_skill.enabled` | `true` | Requires `tools.skills.enabled`; see [Skills tools](../tools/skills.md) |
| `i2c`, `spi`, `serial` | `tools.i2c.enabled`, `tools.spi.enabled`, `tools.serial.enabled` | `false` | Platform-specific; see [Hardware tools](../tools/hardware.md) |
| MCP tools | `tools.mcp.enabled` | `false` | Servers under `tools.mcp.servers`; see [MCP tools](../tools/mcp.md) |
| MCP discovery | `tools.mcp.discovery.enabled` | `false` | Registers `tool_search_tool_bm25` / `tool_search_tool_regex`; see [Tool discovery](../tools/mcp.md#tool-discovery-lazy-loading) |

Two related settings sit outside the pattern:

- `tools.media_cleanup.*` prunes stored media and uses the `PICOCLAW_MEDIA_CLEANUP_` environment prefix; see [Media tools](../tools/media.md#media-cleanup).
- `tools.skills.registries` configures the registries used by `find_skills` and `install_skill`; see [Skills tools](../tools/skills.md).

Agents can see a narrower tool set than the global switches allow: an `AGENT.md` frontmatter `tools:` list acts as an allowlist per agent, and `turn_profile` can narrow the exposed tools further for specific requests (see [Configuration](../guides/configuration.md)).

## Path access

All filesystem-facing tools share one sandbox model:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `allow_read_paths` | string[] | `[]` | Regular expressions of absolute paths that read tools may access even when the workspace restriction would deny them (for example `^/srv/data(?:/|$)`) |
| `allow_write_paths` | string[] | `[]` | Regular expressions of absolute paths that write tools (`write_file`, `edit_file`, `append_file`) may modify outside the workspace |

These allowlists extend the workspace boundary defined by `agents.defaults.restrict_to_workspace` (default `true`) and `agents.defaults.allow_read_outside_workspace` (default `false`, read-only). They never override the [protected local files](#protected-local-files) policy, so credentials and system files stay denied. The agent's own media directory is always appended to the read allowlist automatically.

## Directory Structure

```json
{
  "tools": {
    "web": {
      ...
    },
    "mcp": {
      ...
    },
    "exec": {
      ...
    },
    "cron": {
      ...
    },
    "skills": {
      ...
    }
  }
}
```

## Sensitive Data Filtering

Before tool results are sent to the LLM, PicoClaw can filter sensitive values (API keys, tokens, secrets) from the output. This prevents the LLM from seeing its own credentials.

See [Sensitive Data Filtering](../security/sensitive_data_filtering.md) for full documentation.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `filter_sensitive_data` | bool | `true` | Enable/disable filtering |
| `filter_min_length` | int | `8` | Minimum content length to trigger filtering |

Tool call logs record only the argument **keys** and count, never the argument values, so secrets passed to a tool are not written to log files. The filter above applies to the content exchanged with the LLM, not to logs.

## Large tool results

Tool results larger than `max_inline_result_chars` (counted in Unicode characters, not bytes) are not injected into the model context in full. PicoClaw saves the full text as a local artifact in the agent workspace (`<workspace>/.artifacts/tool/`) and keeps a bounded **head+tail preview** inline: an honest, front-loaded truncation notice with the real number of omitted characters, then the first and last few kilobytes of the output, then a pointer to the full artifact. Keeping useful content inline means the model can usually answer without re-reading the artifact — important for content tools such as `read_file`, where re-reading a spilled artifact would spill again and loop. The artifact path is also exposed as a structured `[file:...]` tag so the model can read it (via `read_file` with `offset`/`length`) or forward it with `send_file`. The preview never removes a tool's own truncation guidance (for example `read_file`'s "call again with offset=…" hint stays in the head). This mirrors the large-text handling already used for MCP tools and applies to every tool, including results produced or replaced by an `AfterTool` hook; artifacts are pruned automatically (older than 24 hours, or beyond the newest 128 files per directory).

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `max_inline_result_chars` | int | `16384` | Maximum tool-result text kept inline, in Go runes. Larger results are spilled to a workspace artifact with a bounded head+tail preview. Set to `0` or a negative value to disable. |

Truncation is separate from spillover. Some tools bound their own raw output before it reaches this layer: `exec` keeps the first 40% and the last 60% of foreground output beyond `tools.exec.max_output_chars` (default `50000`) around an `[OUTPUT TRUNCATED - N chars omitted out of T total]` notice, line-mode `read_file` clamps `max_lines` requests to `tools.read_file.max_lines` while cutting lines longer than `tools.read_file.max_line_length` with a ` ... [truncated]` marker, and MCP text results keep the first 40% and the last 60% of the sanitized text beyond `tools.mcp.max_inline_text_chars` around an `[MCP RESULT TRUNCATED - N chars omitted out of T total]` notice while persisting the full raw payload to their own `.artifacts/mcp` artifact.

When a tool cut its own output (`exec`, `web_fetch`, or an MCP text result whose own artifact write failed), the spillover saves the **pre-truncation text** rather than the truncated view, and the inline preview shows the real head and tail of the full output — so the omitted middle stays readable from the artifact. Results under `max_inline_result_chars` are not spilled, so a deliberately small per-call cap (for example a small `maxChars`) keeps the tool's own truncated view.

> **Security note — artifacts are not filtered.** The artifact file is written from the raw tool result inside result normalization, which runs **before** `filter_sensitive_data` is applied. The filter only redacts the content that is later injected into the model context, so the artifact on disk keeps the **unfiltered** payload (this was already true for MCP large-text artifacts; it now applies to every tool). A large `exec` result, for example, can persist raw secrets to the workspace even when filtering is enabled. The directory (`0700`) and files (`0600`) are permission-restricted, but their contents are not redacted, and the artifact path is exposed to the model — which can read it or forward it with `send_file`, delivering the raw file. Treat workspace artifacts as sensitive data. See [Sensitive Data Filtering](../security/sensitive_data_filtering.md#large-tool-result-artifacts).

## Protected local files

Filesystem read tools deny `.env`, `.env.*`, `.envrc`, `.security.yml` (including its backups), `.netrc`, `.npmrc`, `.pypirc`, `.pgpass`, `.git-credentials`, and files inside `.ssh`, `.aws`, `.kube`, `.gnupg`, `.docker`, `.azure`, `.config/gh`, or `.config/gcloud` directories. Under `PICOCLAW_HOME`, reads are also denied for `auth.json`, `auth/mcp`, and `channels/weixin/context-tokens`; the default `plugin-data` root is hidden because it may contain MCP bearer-token files. Reads of `CODEX_HOME/auth.json` (default `~/.codex/auth.json`) are denied as well. Windows NT/device-namespace paths are rejected before path resolution. The check follows symlinks, hides protected entries from directory listings and file searches, and applies to local files attached through `send_file`, `load_image`, or `message`. Read allow paths do not override it.

Filesystem writes are denied for those same credential paths, except that `write_file` may write `.env.example`; `edit_file` and `append_file` still cannot read it. Writes to `/etc/sudoers`, `/etc/passwd`, `/etc/shadow`, `/etc/sudoers.d`, `/etc/systemd`, and the workspace `sessions` directory are also denied. The write check follows symlinks and runs for `write_file`, `edit_file`, and `append_file`; write allow paths do not override it. The default `plugin-data` directory is protected, but a custom `plugins.data_dir` is not discovered by this policy.

This is a guard for PicoClaw's filesystem tools. Shell commands and external MCP tools run outside this policy; use process or container isolation when they must not access workspace secrets.

## Image Generation

The optional `image_generate` tool uses an image-capable OpenAI, Gemini, or OpenRouter model configured in `model_list` and sends generated images to the current chat. See the [image generation guide](../tools/image-generation.md) for configuration and supported arguments.

## Environment Variables

All configuration options can be overridden via environment variables with the format `PICOCLAW_TOOLS_<SECTION>_<KEY>`:

For example:

- `PICOCLAW_TOOLS_WEB_BRAVE_ENABLED=true`
- `PICOCLAW_TOOLS_EXEC_ENABLED=false`
- `PICOCLAW_TOOLS_EXEC_ENABLE_DENY_PATTERNS=false`
- `PICOCLAW_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10`
- `PICOCLAW_TOOLS_MCP_ENABLED=true`
- `PICOCLAW_TOOLS_MCP_MAX_INLINE_TEXT_CHARS=16384`
- `PICOCLAW_TOOLS_MAX_INLINE_RESULT_CHARS=16384`

Note: Nested map-style config (for example `tools.mcp.servers.<name>.*`) is configured in `config.json` rather than
environment variables.

For MCP tools, `tools.mcp.max_inline_text_chars` controls how much text result is kept inline in model context. The threshold is counted in Unicode characters (Go runes), not bytes. For example, `16384` means up to 16,384 characters inline, which may occupy more than 16 KB for multibyte text such as CJK. Above this threshold, PicoClaw keeps the first 40% and the last 60% of the sanitized MCP text around an `[MCP RESULT TRUNCATED - N chars omitted out of T total]` notice and saves the full raw payload as a local artifact in the agent workspace, with a short note plus a structured `[file:...]` artifact path pointing at it. If the artifact write fails, the bounded view stays inline and the full payload is handed to the spillover above, which can still save it under a different artifact directory.
