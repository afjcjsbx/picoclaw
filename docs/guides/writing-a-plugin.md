# Writing a Plugin

This guide takes a plugin from an empty directory to a working install. A PicoClaw plugin is
an [Agent Plugins 1.0.0](plugins.md) package: a directory of JSON and Markdown that
bundles skills, MCP servers and, optionally, PicoClaw hooks. There is no SDK to import and
nothing to compile into PicoClaw. The host reads your files and mounts what they declare; it
never loads native code into its own process.

The [plugin guide](plugins.md) is the reference for every field. The
[plugin CLI reference](../reference/plugins-cli.md) documents each command used here.

Starting from an existing repository rather than an empty directory? See
[Installing a repository that is not a plugin](plugins.md#installing-a-repository-that-is-not-a-plugin).

## 1. Scaffold

```bash
picoclaw plugin init weather --dir ./plugins
```

This creates:

```text
plugins/weather/
├── plugin.json
├── README.md
└── skills/weather/SKILL.md
```

The generated `plugin.json` is the smallest useful manifest:

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "weather",
  "version": "0.1.0",
  "description": "weather plugin for PicoClaw."
}
```

`$schema` is required and must be exactly that URL. `name` is 1-64 characters from
`a-z`, `0-9`, `.` and `-`, starts and ends alphanumeric, with no `--` or `..`. Add `author`,
`homepage`, `repository`, `license` and `keywords` as you like. A misspelled or unknown field
does not break the plugin; it is ignored and reported as a warning.

## 2. Write the skill

Edit `skills/weather/SKILL.md`. The frontmatter `name` must equal the directory name and the
`description` must not be empty:

```markdown
---
name: weather
description: Look up the weather for a city. Use when the user asks about forecasts.
---

# Weather

Call the `get_forecast` tool with a city name and summarize the result in one sentence.
```

Each immediate child of `skills/` with a `SKILL.md` is one skill, published as
`<id>:<skill>`. Files you put under `references/` or `skills/<name>/references/` can be read
by the agent through the plugin's resource tool.

## 3. Declare an MCP server (optional)

Tools come from MCP servers. Put them in `mcp.json` at the package root. A server you ship
yourself, as a prebuilt executable inside the package:

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "weather": {
      "type": "stdio",
      "command": "./bin/weather-server"
    }
  }
}
```

Or a remote server:

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "weather": { "type": "streamable-http", "url": "https://example.org/mcp" }
  }
}
```

Keep secrets out of the package. Users supply credentials from the host with
`mcp_overrides` or `mcp_bearer_token_files` in their own `config.json`
(see [MCP servers](plugins.md#mcp-servers)). Your server must write only protocol
messages to stdout and log to stderr.

## 4. Add hooks (optional)

Hooks are a PicoClaw extension. They let the plugin act on the agent loop without the model
asking, for example to inject context before each LLM call. Put them in
`com.sipeed.picoclaw/hooks.json`, or inline under
`extensions["com.sipeed.picoclaw"]` in `plugin.json` (never both):

```json
{
  "hooks": [
    {
      "name": "inject-context",
      "intercept": ["before_llm"],
      "mcp": {
        "server": "weather",
        "tool": "get_context",
        "arguments": { "query": "${user_message}" },
        "result": "append_to_user_message"
      }
    },
    {
      "name": "log-turn",
      "observe": ["turn_completed"],
      "mcp": { "server": "weather", "tool": "record", "arguments": { "text": "${assistant_message}" } }
    }
  ]
}
```

Rules the host enforces, and `validate` checks:

- `intercept` takes `before_llm`, `after_llm`, `before_tool`, `after_tool` or `approve_tool`.
  `observe` takes runtime event names such as `agent.turn.end`, `turn_completed` or
  `post_compaction` (history summarization or context compression).
- `result` is `ignore` (default), `append_to_user_message` or `approval`.
  `append_to_user_message` is valid only with `"intercept": ["before_llm"]` and no `observe`.
  `approval` is valid only with `"intercept": ["approve_tool"]`.
- An `mcp` action cannot be combined with `command`, `args`, `env` or `cwd`, and its `server`
  must exist in `mcp.json` or in the user's `mcp_overrides`.
- Hooks run only after the user sets `allow_hooks: true` on the entry. Treat that as a
  separate trust grant and say in your README why you need it.

## 5. Validate

```bash
picoclaw plugin validate ./plugins/weather
```

`validate` reads the package the way the host does but starts nothing. It prints one line per
problem, for example:

```text
Validating /home/me/plugins/weather
  ! skill:weather: skill name must match directory and contain 1-64 characters
✓ weather 0.1.0: 0 skills, 1 MCP servers, 0 hooks, 1 warnings
```

The command exits `1` only when the manifest is rejected, since the host would load the rest
of a plugin with a broken skill or hook. In CI, fail on any finding:

```bash
picoclaw plugin validate . --strict
```

## 6. Enable and test

```bash
picoclaw plugin add weather --path ./plugins/weather --enable
picoclaw plugin test weather
```

`test` loads the plugin the way the gateway will, with a timeout, and prints its state, the
skills and MCP tools it published, and diagnostics. It starts your MCP server, so build
the executable first. A `failed` plugin exits `1`; a `degraded` one means some component was
skipped, and the diagnostics say which.

Then restart the gateway and ask the agent something the skill covers. If hooks do not seem
to run, check `picoclaw plugin show weather`; it lists hooks as inactive until
`allow_hooks` is set. Use `picoclaw plugin edit` to set it in `config.json`.

## 7. Share it

There is no central registry. A plugin is a directory, so distribute it as a Git repository or an
archive, with any executables prebuilt for the platforms you support (the host never runs
install scripts or downloads dependencies).

Users install it by placing the directory under `~/.picoclaw/plugins/` or
`<workspace>/plugins/`, or anywhere with `--path`, then running `picoclaw plugin add <name> --enable`.
Reviewing a plugin before enabling it matters: its MCP servers and hooks run with the
user's permissions.

Keep the plugin `name` stable across releases. The user's data directory
(`plugin-data/<id>`) and `entries.<id>` are keyed by it.
