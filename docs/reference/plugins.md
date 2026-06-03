# Plugins

PicoClaw plugins are opt-in directories that extend the runtime without changing core code. A plugin can contribute command-backed tools, slash commands, process hooks, MCP servers, and bundled skills.

## Discovery

PicoClaw scans these plugin roots in order. Later roots override earlier roots when the same plugin key is found.

1. Bundled: `plugins/` from the current working directory, or `PICOCLAW_BUILTIN_PLUGINS`.
2. User: `$PICOCLAW_HOME/plugins/`.
3. Project: `.picoclaw/plugins/` under the workspace and current directory, only when `plugins.project_enabled` or `PICOCLAW_ENABLE_PROJECT_PLUGINS=1` is set.
4. Extra roots: `plugins.directories` and `PICOCLAW_PLUGIN_DIRS`.

Each plugin is either `enabled`, `disabled`, or `not enabled`. Third-party plugins are discovered but not loaded until enabled.

```json
{
  "plugins": {
    "enabled": ["weather"],
    "disabled": ["old-plugin"],
    "project_enabled": false,
    "directories": ["/opt/picoclaw/plugins"]
  }
}
```

Manage state with:

```sh
picoclaw plugins list
picoclaw plugins show weather
picoclaw plugins enable weather
picoclaw plugins disable weather
```

Inside a chat session, `/plugins` lists discovered plugins and `/plugins show <name>` shows one plugin.

## Layout

```text
~/.picoclaw/plugins/weather/
├── plugin.yaml
├── tools/weather-tool
├── commands/status
└── skills/forecast/SKILL.md
```

Nested plugin keys are supported one level deep, for example `plugins/category/name/plugin.yaml`; the key is `category/name`.

## Manifest

```yaml
name: weather
version: "1.0"
description: Weather tools and workflow helpers

tools:
  - name: get_weather
    description: Get weather for a city.
    command: ["tools/weather-tool"]
    timeout_ms: 30000
    parameters:
      type: object
      properties:
        city:
          type: string
          description: City name
      required: ["city"]

commands:
  - name: weatherstatus
    description: Show plugin status
    command: ["commands/status"]

hooks:
  processes:
    audit:
      command: ["hooks/audit-hook"]
      intercept: ["before_tool", "after_tool"]

mcp_servers:
  weather-mcp:
    command: "bin/weather-mcp"
    args: ["serve"]

skills:
  - name: forecast
    path: skills/forecast/SKILL.md
```

Relative paths in `command`, `dir`, `env_file`, and `skills.path` are resolved from the plugin directory. Plugin-provided hook and MCP names are namespaced as `<plugin-key>/<name>`.

## Command Payloads

Command-backed tools receive JSON on stdin:

```json
{
  "plugin": "weather",
  "plugin_key": "weather",
  "tool": "get_weather",
  "arguments": {"city": "Rome"},
  "channel": "telegram",
  "chat_id": "123"
}
```

Slash commands receive:

```json
{
  "plugin": "weather",
  "plugin_key": "weather",
  "command": "weatherstatus",
  "raw_args": "verbose",
  "channel": "telegram",
  "chat_id": "123"
}
```

Tools can return a PicoClaw `ToolResult` JSON object:

```json
{
  "for_llm": "Rome: sunny, 24 C",
  "for_user": "",
  "silent": true,
  "is_error": false
}
```

Plain stdout is also accepted and is returned silently to the model. Slash commands may return plain text or JSON with `text`, `message`, `for_user`, or `for_llm`.

## Bundled Skills

Plugin skills are exposed as `<plugin-name>:<skill-name>`, for example `weather:forecast`. They are loaded from the plugin directory and are not copied into the user's workspace.
