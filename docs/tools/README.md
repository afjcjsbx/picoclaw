# Tools

PicoClaw registers its tools dynamically at runtime. Each one can be enabled or disabled under `tools` in `config.json`; the [tool switch table](../reference/tools_configuration.md#tool-switches) lists every tool with its switch, default state, and documentation.

## Tool families

| Family | Tools | Documentation |
|--------|-------|---------------|
| Filesystem | `read_file`, `write_file`, `edit_file`, `append_file`, `list_dir`, `search_files` | [Filesystem tools](filesystem.md) |
| Web | `web_search`, `web_fetch` | [Web tools](../reference/tools_configuration.md#web-tools) |
| Command execution | `exec` | [Exec tool](../reference/tools_configuration.md#exec-tool) |
| Scheduling | `cron` | [Cron tool](../reference/tools_configuration.md#cron-tool), [Scheduled tasks and cron jobs](../reference/cron.md) |
| Messaging | `message`, `reaction` | [Messaging tools](messaging.md) |
| Media | `load_image`, `send_file`, `send_tts`, `image_generate` | [Media tools](media.md), [Image generation](image-generation.md) |
| Subagents | `spawn`, `subagent`, `spawn_status`, `delegate` | [Subagent tools](subagents.md) |
| Skills registry | `find_skills`, `install_skill` | [Skills tool](../reference/tools_configuration.md#skills-tool) |
| MCP | `mcp_<server>_<tool>`, `tool_search_tool_bm25`, `tool_search_tool_regex` | [MCP tool](../reference/tools_configuration.md#mcp-tool) |
| Hardware | `i2c`, `spi`, `serial` | [Hardware tools](hardware.md) |
| Planning | `todo` | [Todo planning tool](../reference/tools_configuration.md#todo-planning-tool) |

## Global settings

These apply to every tool and live in the same `tools` block:

- **Path access** — [`allow_read_paths` and `allow_write_paths`](../reference/tools_configuration.md#path-access) extend the workspace sandbox; protected credential files are always denied.
- **Result filtering** — [`filter_sensitive_data` and `filter_min_length`](../reference/tools_configuration.md#sensitive-data-filtering) redact secrets from tool results before they reach the model.
- **Large results** — [`max_inline_result_chars`](../reference/tools_configuration.md#large-tool-results) spills oversized tool output to a workspace artifact with a bounded inline preview.

Tool visibility can be narrowed per agent and per request, on top of the global switches:

- an `AGENT.md` frontmatter `tools:` list restricts which registered tools an agent can see;
- `turn_profile` can further narrow exposed tools for specific requests (see [Configuration](../guides/configuration.md)).

## Deep dives

- [Image Generation](image-generation.md): configure the `image_generate` tool with OpenAI, Gemini, or OpenRouter models.
