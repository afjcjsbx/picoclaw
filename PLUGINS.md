# PicoClaw plugins

PicoClaw supports **Agent Plugins 1.0.0**, including skills, stdio MCP,
Streamable HTTP MCP and legacy HTTP+SSE MCP. The 1.1.0 draft is not enabled.
The portable contract is the [published specification](https://github.com/agentplugins/agent-plugins-spec/blob/main/spec/1.0.0.md).

## Quick start

Build the included demo from the PicoClaw repository root:

```sh
mkdir -p examples/plugins/demo-plugin/bin
go build -o examples/plugins/demo-plugin/bin/demo-plugin ./examples/plugins/demo-plugin
```

On Windows, build `bin/demo-plugin.exe` and change the demo's `mcp.json` command
to `./bin/demo-plugin.exe`.

Add this section to your PicoClaw configuration, replacing the example path:

```json
{
  "plugins": {
    "enabled": true,
    "entries": {
      "demo-plugin": {
        "enabled": true,
        "path": "/absolute/path/to/picoclaw/examples/plugins/demo-plugin"
      }
    }
  }
}
```

Start PicoClaw or reload its configuration. Ask it to use `demo-plugin:greet`
to greet Ada. The MCP `greet` tool returns `Hello, Ada!`.

The demo uses the Go MCP SDK already present in this repository. No additional
package manager, API key or service is required. Compile before enabling it;
the host never runs installation scripts or downloads dependencies implicitly.

## Package format

```text
my-plugin/
├── plugin.json
├── mcp.json                         # optional
├── skills/                          # optional
│   └── greet/
│       ├── SKILL.md
│       └── references/example.md
├── bin/server                       # optional bundled executable
└── com.sipeed.picoclaw/              # optional PicoClaw extension
    └── hooks.json
```

Minimal `plugin.json`:

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "my-plugin"
}
```

The manifest supports `version`, `description`, `author`, `homepage`,
`repository`, `license`, `keywords`, and `extensions`. Names use lowercase
ASCII letters, digits, periods and hyphens, are 1–64 characters long, start
and end with an alphanumeric character, and contain neither `--` nor `..`.

Validation is implemented locally in Go against the versioned schema rules and
the specification's semantic requirements. Reference schemas and their Apache
license are included in `pkg/plugins/schemas`. Schema URLs select supported
versions; they are never fetched at runtime. Unknown top-level manifest fields
and a non-object `extensions` field produce warnings and are ignored. Other
manifest schema violations reject the plugin. Unknown extension namespaces are
ignored without inspecting their values. Metadata such as `version` is not
subjected to additional SemVer or URL constraints absent from the specification.

## Discovery, installation and activation

By default, discovery searches immediate child directories of:

1. `<workspace>/plugins`
2. `<PICOCLAW_HOME>/plugins` (normally `~/.picoclaw/plugins`)

Copy or clone a reviewed package into one of those directories, or provide an
explicit `entries.<id>.path`. Discovery alone never activates a package. Both
`plugins.enabled` and the entry's `enabled` must be true.

An entry ID follows the plugin name syntax. Without `path`, it selects a
discovered manifest name. With `path`, it is a stable installation alias and may
differ from the manifest name. Ambiguous names require an explicit path; the
same resolved package root cannot be activated under multiple aliases.

Relative `directories` and entry paths resolve against the workspace. Use
absolute paths when installing dependencies or enabling the repository demo.
Paths do not use shell expansion; write an absolute home path instead of `~`.

## Host configuration

```json
{
  "plugins": {
    "enabled": true,
    "directories": ["plugins"],
    "data_dir": "/absolute/path/to/plugin-data",
    "concurrency": 4,
    "init_timeout_ms": 15000,
    "call_timeout_ms": 60000,
    "startup_wait_ms": 15000,
    "entries": {
      "my-plugin": {
        "enabled": true,
        "agents": ["main"],
        "allow_hooks": false,
        "config": {"format": "short"}
      }
    }
  }
}
```

`data_dir` defaults to `<PICOCLAW_HOME>/plugin-data`; a relative override resolves
against the workspace. Each installation receives a persistent subdirectory
named by its entry ID. Keep the ID unchanged when updating the package.

`concurrency` limits simultaneous plugin initializations (1–32; default 4).
`init_timeout_ms` applies to each MCP connection or hook handshake.
`call_timeout_ms` bounds MCP tool calls. Positive duration overrides are used;
zero or negative durations select the documented defaults.

Omitting `agents` grants access to all configured agents. `"agents": []` grants
none. Agent `tools` allowlists still apply. Agent `mcpServers` declarations use
`<installation-id>:<server-name>`, for example `demo-plugin:greeter`.

Plugin MCP activation is controlled by `plugins`, independently of the native
`tools.mcp.enabled` server list. Existing MCP discovery settings are reused when
a search method is enabled. Runtime tool names have an `mcp_plugin_` prefix and
a stable hash suffix to fit provider limits and avoid ambiguous normalization.
Use the emitted tool definitions to obtain exact names for a tool allowlist.
Plugin tools never overwrite built-ins or existing tools.

## Skills and bundled resources

Each immediate child of `skills/` containing a regular `SKILL.md` is a candidate.
Nested descendants are not independently discovered. Files must follow
[Agent Skills](https://agentskills.io/specification), including YAML frontmatter,
a matching directory/name and a nonempty description.

Skills appear as `<installation-id>:<skill-name>`, preserving the original
frontmatter. Validated skill bodies are snapshots until configuration reload.
Loading or removing plugin skills invalidates the prompt cache.

The host provides a plugin-specific `read_resource` tool, with a `plugin_` name prefix, for bundled references
and assets. Its name is included in the loaded skill instructions. Paths are
relative to the plugin root; reads are bounded to 2 MiB and cannot follow links
outside it. This avoids granting general filesystem access outside the agent's
workspace. The tool is subject to the agent's normal tool allowlist.

## MCP servers

Put portable MCP configuration in root `mcp.json`:

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "local": {
      "type": "stdio",
      "command": "./bin/server",
      "args": ["--data", "${PLUGIN_DATA}"],
      "cwd": "${PLUGIN_ROOT}",
      "env": {"CONFIG_FILE": "${PLUGIN_ROOT}/config.json"}
    },
    "remote": {
      "type": "streamable-http",
      "url": "https://example.org/mcp",
      "headers": {"X-Tenant": "public"}
    }
  }
}
```

`command` is a single executable name resolved through the platform search path,
or a bundled executable path beginning with `./`. It is never evaluated as a
shell command. Arguments are passed separately. The default working directory is
the plugin root; explicit directories must remain inside the plugin root or the
plugin's data directory.

PicoClaw supplies `PLUGIN_ROOT` and writable `PLUGIN_DATA`. The base environment
contains only available executable-search and temporary-directory variables;
provider keys and other ambient variables are not inherited. Configured `env`
values overlay this base. The two reserved variables cannot be set by packages.
Only their exact placeholders are expanded, once, in `args`, `env` values and
`cwd`. Other placeholders remain literal. No expansion occurs in commands,
remote URLs or headers. Install dependencies into `PLUGIN_DATA` explicitly.

Remote URLs require HTTPS except for literal loopback addresses and `localhost`.
User information, fragments, invalid headers and duplicate case-insensitive
header names are rejected. Client protocol headers take precedence. Redirects
and legacy SSE endpoint events cannot send requests to another origin.
Legacy `sse` selects the actual HTTP+SSE protocol here; this does not change the
historical interpretation of `sse` in native PicoClaw MCP configuration.

Credentials do not belong in portable package `env` or `headers`. There is no
plugin-specific OAuth setup or credential-reference format in this implementation.
Servers requiring unsupported authorization fail independently with diagnostics.

## Optional PicoClaw hooks

Hooks are a PicoClaw extension, not a portable Agent Plugins component. Enable
them explicitly with `entries.<id>.allow_hooks: true`.

Configure `plugin.json` as follows, or put the namespace value (the object
containing `hooks`) in `com.sipeed.picoclaw/hooks.json`. Using both is an error
limited to the hook extension.

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "my-plugin",
  "extensions": {
    "com.sipeed.picoclaw": {
      "hooks": [{
        "name": "observer",
        "command": "python3",
        "args": ["${PLUGIN_ROOT}/com.sipeed.picoclaw/observer.py"],
        "observe": ["agent.turn.end"],
        "intercept": ["after_llm"]
      }]
    }
  }
}
```

Hook launch fields follow the same validation, environment and containment rules
as stdio MCP. The process implements PicoClaw's existing JSON-RPC hook protocol:
`hook.hello`, `hook.runtime_event`, and the requested interception methods.
`entries.<id>.config` is delivered as `config` in the hello payload. Available
interceptions are `before_llm`, `after_llm`, `before_tool`, `after_tool`, and
`approve_tool`; only declared stages and allowed agents are forwarded.

See [the hook protocol](docs/architecture/hooks/README.md). Hook failures use the
existing hook manager's timeout and error policies. In particular, approval
errors can deny a tool call; they do not crash the runtime. Trusted interception
hooks can alter or short-circuit execution, so `allow_hooks` is a separate grant.
Portable MCP tools always use the normal registry/approval execution path.

## Runtime, failures and isolation

Gateway loading runs asynchronously. Direct requests and heartbeats wait up to
`startup_wait_ms`; unfinished plugins continue loading in the background.
Skills are published before MCP initialization. A bad manifest rejects its
plugin; a bad MCP document disables that component type; a bad server or skill
is skipped independently. Other plugins and components continue loading.

`AgentLoop.PluginStatuses()` exposes initialization state and diagnostics to host
integrations. States include discovered, loading, ready, degraded, failed and
closed. Runtime tool failures are returned as tool errors and logged. Failed
calls are not automatically replayed; reload explicitly to reconnect a crashed
plugin. Shutdown and reload cancel calls, join loaders, remove registrations
and close subprocesses. Data directories survive reload and package updates.

Plugin subprocesses use `pkg/isolation` just like existing MCP servers and
process hooks. Linux and Windows use the configured platform backends. Other
platforms, including macOS, do not gain a new OS sandbox from this feature.
When isolation is disabled, enabled executables run with PicoClaw's OS user
permissions. Path containment protects package discovery and resource access;
it is not a sandbox for arbitrary subprocess code. Enable only trusted packages.

For isolated installations outside the instance filesystem, configure explicit
read-only package and writable data/toolchain exposure as described in
[isolation documentation](pkg/isolation/README.md). The plugin loader never
silently broadens those OS permissions.

To disable a plugin, set its entry's `enabled` to false and reload. To uninstall,
disable it first, then remove its package directory. Remove its dedicated data
directory separately only if you want to discard persisted state.

## Development and validation

Implement tools with an MCP SDK in any language. Keep stdout exclusively for the
stdio protocol and log to stderr. Declare accurate input schemas and return MCP
tool errors for expected failures. Keep the portable manifest free of custom
`tools`, `hooks` or runtime fields; use MCP and the extension namespace instead.

Run the focused Go tests from the repository root:

```sh
go test -tags stdjson ./pkg/plugins ./pkg/mcp ./pkg/skills ./pkg/config ./pkg/agent ./pkg/tools
go test -race -tags stdjson ./pkg/plugins ./pkg/mcp ./pkg/skills ./pkg/config ./pkg/agent ./pkg/tools
```

HTTP integration tests require local loopback listeners. Tests exercise a real
stdio child process, HTTP and legacy SSE, manifest and component failure
boundaries, environment expansion, paths, timeouts, crash isolation and reload.
