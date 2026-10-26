# Agent Plugin Installation and Reference

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

For the default plugin directory, install a package like this:

```sh
mkdir -p ~/.picoclaw/plugins
git clone <plugin-repository> ~/.picoclaw/plugins/<plugin-name>
```

You can also copy it to `<workspace>/plugins/<plugin-name>`. Then enable the
plugin in `config.json`, using the `name` from its `plugin.json` as the entry ID:

```json
{
  "plugins": {
    "enabled": true,
    "entries": {
      "plugin-name": { "enabled": true }
    }
  }
}
```

Restart PicoClaw or reload its configuration to activate the plugin. Review
packages before enabling them; local plugin servers run as subprocesses.

The same steps from the command line: `picoclaw plugin add <name> --enable` writes
the entry, `picoclaw plugin list` shows what is configured and what sits on disk, and
`picoclaw plugin test <name>` loads it once to check it. See the
[plugin CLI reference](../reference/plugins-cli.md).

An entry ID follows the plugin name syntax. Without `path`, it selects a
discovered manifest name. With `path`, it is a stable installation alias and may
differ from the manifest name. Ambiguous names require an explicit path; the
same resolved package root cannot be activated under multiple aliases.

Relative `directories` and entry paths resolve against the workspace. Use
absolute paths when installing dependencies or enabling the repository demo.
Paths do not use shell expansion; write an absolute home path instead of `~`.

## Installing a repository that is not a plugin

Many repositories ship skills or MCP servers for some other agent without being
Agent Plugins. PicoClaw only loads a directory that has a `plugin.json` **at its root**
with the Agent Plugins `$schema`. A manifest inside another tool's folder (for example
`.<tool>-plugin/plugin.json`) does not count, even if it is also called `plugin.json`.

You rarely need to port anything. If the repository already keeps skills in
`skills/<name>/SKILL.md` or declares MCP servers, adding a manifest and, at most,
converting an MCP file is enough. Work through the steps below in order; each ends with a
command that tells you whether it worked.

### 1. Recognize the situation

| What you see | What it means |
|--------------|---------------|
| `plugin list` shows nothing for a directory you cloned | There is no root `plugin.json`, or the directory is not an immediate child of a plugin directory |
| `plugin validate <dir>`: `plugin.json: lstat …/plugin.json: no such file or directory` | No manifest in that directory |
| `plugin validate <dir>`: `missing or unsupported $schema ""` | A manifest exists but is for another tool, or has no `$schema` |
| `plugin list` shows a row with an empty `NAME` | The root manifest was rejected; the row is named after the directory. Run `plugin validate` on it |
| `plugin validate` succeeds with `0 skills, 0 MCP servers` | The manifest is fine but the layout does not match; continue with step 2 |

### 2. Take stock of what the repository offers

| You find | Portable? | What to do |
|----------|-----------|------------|
| `skills/<name>/SKILL.md` at the root | Yes | Nothing; they load as they are |
| Skills in another directory, or nested deeper than `skills/<name>/` | Yes, once moved | Put them directly under `skills/` of the package root. Only immediate children of `skills/` are discovered |
| An MCP server list in another tool's format | After conversion | Write an `mcp.json` (step 5) |
| Hooks written for another agent | No | Leave them out. PicoClaw hooks use a different schema and protocol, see [Optional PicoClaw hooks](#optional-picoclaw-hooks) |
| Commands, sub-agent definitions, themes and other per-tool files | No | Ignored by Agent Plugins 1.0.0; they do no harm |
| Helper scripts and binaries next to a skill | Shipped, never run by the host | The agent can read them as text through the plugin's resource tool. Whether it can execute them depends on your `exec` and workspace settings |
| A plugin inside a subdirectory of a larger repository | Yes | Put the manifest in that subdirectory and point `path` at it (step 7) |

Skill text may name tools that exist only in the agent it was written for. PicoClaw
skills are plain instructions, so the agent maps them to the tools it has. If you want
to steer that, add a skill or `references/` file of your own instead of editing the
upstream ones.

### 3. Decide where the manifest lives

- **In place (recommended).** Clone the repository into a plugin directory and add
  `plugin.json` next to the existing files. Updating is a `git pull`. Keep `git status`
  clean by running `echo plugin.json >> .git/info/exclude` in the clone. If upstream later adds its own
  root `plugin.json`, git refuses to overwrite yours: delete yours and use theirs.
- **Wrapper package.** Create a new directory with your `plugin.json` and *copy* what
  you need into it (`skills/`, `mcp.json`). Use this when you must not touch the clone
  or the useful files are scattered. Updates are manual. A symlink works only if it
  resolves **inside** the package root; one that points out of it is reported as
  `path escapes plugin boundary`.

### 4. Write the manifest

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json",
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "What it provides, in one line.",
  "homepage": "https://example.org/my-plugin",
  "repository": "https://github.com/example/my-plugin",
  "license": "MIT",
  "keywords": ["skills"]
}
```

Only `$schema` and `name` are required. If the repository has a manifest for another tool,
copy its metadata but keep only these fields; anything else becomes a warning
(`ignored unknown field "x"`). `author` is stricter: it accepts only `name`, `email` and
`url`, and any other key rejects the plugin (`unknown author field "x"`).

Pick the **id** deliberately. Skills are published as `<id>:<skill>`, and skill texts
that refer to each other by that prefix only work if your id matches it. With a `path`
the id may differ from the manifest `name`; without one it must equal it.

### 5. Convert MCP servers (if any)

Other tools commonly accept a looser format. The portable one needs the `$schema`, an
explicit `type` and a bare or `./` command:

```json
{ "mcpServers": { "docs": { "command": "npx", "args": ["-y", "some-mcp-server"] } } }
```

becomes `mcp.json`:

```json
{
  "$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json",
  "mcpServers": {
    "docs": { "type": "stdio", "command": "npx", "args": ["-y", "some-mcp-server"] }
  }
}
```

Remote servers use `"type": "streamable-http"` (or `"sse"`) and a `url`; see
[MCP servers](#mcp-servers) for the rules. Do not put keys or tokens in `mcp.json`;
supply them from the host as described in step 8.

### 6. Validate and read the diagnostics

```bash
picoclaw plugin validate <dir> --strict
```

Without `--strict`, only a rejected manifest fails; every other finding is a warning
because the host would still load the rest. The usual ones:

| Diagnostic | Cause and fix |
|------------|---------------|
| `unknown author field "x"` (error) | Remove the extra key from `author` |
| `ignored unknown field "x"` | A manifest field PicoClaw does not know; remove it |
| `skill:<dir>: skill name must match directory and contain 1-64 characters` | `name:` in `SKILL.md` differs from its directory, or uses characters outside `a-z`, `0-9`, `-` (no leading, trailing or doubled hyphen) |
| `skill:<dir>: YAML frontmatter is required` / `invalid skill description` | `SKILL.md` needs frontmatter with a non-empty `description` of at most 1,024 characters |
| `mcp: missing or unsupported MCP $schema` | `mcp.json` is in a foreign format or lacks `$schema`; the whole file is skipped |
| `mcp:<server>: type: unexpected end of JSON input` | That server has no `type` |
| `mcp:<server>: command must be bare or begin with ./` | Use a command found on `PATH`, or a path inside the package starting with `./`; absolute paths are refused |
| `path escapes plugin boundary` | A symlink leaves the package root; copy the files in instead |

Extra frontmatter keys in `SKILL.md` do not fail validation. `allowed-tools`,
`license` and `compatibility` must be strings, and `metadata` a map of strings.

### 7. Add the entry and check it loads

```bash
picoclaw plugin add my-plugin --enable              # found by name under a plugin directory
picoclaw plugin add my-plugin --path /abs/dir --enable   # explicit location
picoclaw plugin show my-plugin
picoclaw plugin test my-plugin
```

`plugin test` loads the plugin the way the gateway will and lists the skills and tools
it publishes. Restart the gateway afterwards.

`plugin "x" matches no single package under the plugin directories` means either that
nothing with that manifest name was found, or that two directories share it. Both are
solved with `--path`. Discovery only looks at the immediate children of the plugin
directories, so a plugin in a subdirectory of a repository always needs `--path`.

### 8. Configure it by hand

The CLI writes `enabled` and `path`. Everything else is edited in `config.json`; open it
with `picoclaw plugin edit`. A fuller entry:

```json
{
  "plugins": {
    "enabled": true,
    "entries": {
      "my-plugin": {
        "enabled": true,
        "path": "/abs/path/to/my-plugin",
        "skill_names": ["review", "debug"],
        "agents": ["main"],
        "config": { "format": "short" },
        "mcp_overrides": {
          "docs": { "type": "streamable-http", "url": "https://example.org/mcp" }
        },
        "mcp_bearer_token_files": { "docs": "api-key" }
      }
    }
  }
}
```

| Field | Use |
|-------|-----|
| `skill_names` | Load only these skills, written **without** the `<id>:` prefix. `null` (what `plugin add` writes) loads all; `[]` loads none |
| `agents` | Agents that may use the plugin. `null` means all; `[]` means none |
| `allow_hooks` | Grant for the plugin's PicoClaw hooks. Off by default; `plugin show` lists hooks as inactive until it is set |
| `config` | String values handed to the plugin's process hooks |
| `mcp_overrides` | Replace or add MCP servers from the host, without editing the package. This is where headers that read host environment variables belong |
| `mcp_bearer_token_files` | Bearer tokens kept in a file under `plugin-data/<id>/`, never in the package |
| `mcp_oauth` | Use saved browser OAuth credentials for named HTTP servers |

After editing, run `picoclaw plugin validate <id> --strict`; validating by id applies the
entry's `mcp_overrides`, so a hook that targets a server defined only there passes.
See [Host configuration](#host-configuration) for the remaining top-level settings.

### 9. Keep it updated

Run `git pull` in the clone, then `plugin validate` and `plugin test` again, because
upstream may rename or restructure skills. Keep the id unchanged: the entry and the
plugin's data directory `plugin-data/<id>` are keyed by it.

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
The host provides a plugin-specific `read_resource` tool, with a `plugin_` name
prefix, for bundled references. Its name is included in the loaded skill
instructions. It accepts only UTF-8 text paths under `skills/` or `references/`,
relative to the plugin root; reads are bounded to 2 MiB and cannot follow links
outside it. Binary assets and files outside those two directories are not
readable. This avoids granting general filesystem access outside the agent's
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
`cwd`. Other placeholders remain literal. Remote header values additionally
support `${VAR}` references to the PicoClaw process environment. No expansion
occurs in commands or remote URLs. Install dependencies into `PLUGIN_DATA`
explicitly.

Remote URLs require HTTPS except for literal loopback addresses and `localhost`.
User information, fragments, invalid headers and duplicate case-insensitive
header names are rejected. Client protocol headers take precedence. Redirects
and legacy SSE endpoint events cannot send requests to another origin.
Legacy `sse` selects the actual HTTP+SSE protocol here; this does not change the
historical interpretation of `sse` in native PicoClaw MCP configuration.

Literal credentials do not belong in portable package `env` or `headers`.
For a remote MCP server, a header in a host `mcp_overrides` entry can refer to
a host environment variable:

```json
"headers": {"Authorization": "Bearer ${REMOTE_API_KEY}"}
```

Set `REMOTE_API_KEY` in the environment of the PicoClaw process. References
are resolved once when the server connects. An unset or empty variable, an
invalid reference, or a resolved header containing invalid HTTP characters
disables that server with a diagnostic that does not contain the secret.

This expansion is host-only. A `${VAR}` reference written in a portable package
`mcp.json` is sent literally and never reads the host environment; only headers
provided through `plugins.entries.<id>.mcp_overrides.<server>` are expanded.
Review the override's header references before enabling a plugin: they select
which host environment values are sent to its MCP endpoint. Stdio processes
still receive only their explicitly configured environment.

Alternatively, for a server that accepts a bearer token, put it alone in a
file under that installation's `PLUGIN_DATA` directory and configure it on
the host:

```json
"mcp_bearer_token_files": {"remote": "api-key"}
```

Add this field to `plugins.entries.<id>`. The map key is the server name in
`mcp.json`; the value is a path relative to `PLUGIN_DATA`. PicoClaw reads the
file at connection time and sends `Authorization: Bearer <token>` only to that
server's origin. The host value replaces a package `Authorization` header.
Missing, empty, invalid or escaping files disable that server with a diagnostic;
the token itself is not logged. This host configuration leaves the portable
Agent Plugins manifest unchanged. OAuth is not provided by this mechanism.

## Optional PicoClaw hooks

Hooks are a PicoClaw extension, not a portable Agent Plugins component. Enable
them explicitly with `entries.<id>.allow_hooks: true`. A plugin may declare
process hooks or direct MCP actions in the same `hooks.json` file. Direct MCP
actions use the plugin's already configured MCP servers and run through
PicoClaw's normal hook lifecycle.

### MCP actions

Each action names a server and tool from `mcp.json` or the entry's
`mcp_overrides`. `intercept` runs the action at a synchronous hook stage;
`observe` runs it for a runtime event. Supported intercept stages are
`before_llm`, `after_llm`, `before_tool`, `after_tool`, and `approve_tool`.
Observe accepts PicoClaw runtime event names such as `agent.turn.end`; the
`turn_completed` alias runs only for successfully completed turns. The
`post_compaction` alias runs after successful history summarization or context
compression, covering both `agent.session.summarize` and
`agent.context.compress` events.

Arguments support the event's available `${user_message}` and
`${assistant_message}` values, `${tool_name}`, `${tool_arguments}`,
`${tool_result}`, `${request}`, `${response}`, `${event}`, and
`${event_payload}`. A placeholder occupying the whole value preserves its JSON
type, so `${tool_arguments}` can be an object. Dotted paths such as
`${event.payload.UserMessage}` select nested fields.

The default result mode is `ignore`, which is useful for capture or other
side-effect tools. `append_to_user_message` is valid with `before_llm`; it adds
the MCP result to the prompt inside an untrusted context block. `approval` is
valid with `approve_tool`; the MCP tool must return `true`/`false` or an object
with an `approved` boolean. Approval errors fail closed. Other MCP hook errors
are logged and leave the turn running.

### Mem0

This example uses Mem0's official Agent Plugin for its manifest and skills,
Mem0's hosted MCP endpoint for tool calls, and PicoClaw's hook mapping for
automatic recall and capture. It does not run Mem0's local MCP server.

Clone the official plugin and the shared plugin core into PicoClaw's plugin
directory. These commands respect `PICOCLAW_HOME` when it is set:

```sh
PICOCLAW_HOME="${PICOCLAW_HOME:-$HOME/.picoclaw}"
PLUGIN_REPO="$PICOCLAW_HOME/plugins/mem0-source"
PLUGIN_ROOT="$PLUGIN_REPO/integrations/mem0-agent-plugin"
mkdir -p "$PICOCLAW_HOME/plugins"
git clone --depth 1 --filter=blob:none --sparse \
  https://github.com/mem0ai/mem0.git "$PLUGIN_REPO"
git -C "$PLUGIN_REPO" sparse-checkout set \
  integrations/mem0-agent-plugin integrations/agent-plugin-core
mkdir -p "$PLUGIN_ROOT/com.sipeed.picoclaw"
cat > "$PLUGIN_ROOT/com.sipeed.picoclaw/hooks.json" <<'JSON'
{
  "hooks": [
    {
      "name": "memory-recall",
      "intercept": ["before_llm"],
      "mcp": {
        "server": "mem0",
        "tool": "search_memories",
        "arguments": {
          "query": "${user_message}",
          "filters": {"AND": [{"user_id": "your-user-id"}, {"app_id": "your-project-id"}]},
          "top_k": 5
        },
        "result": "append_to_user_message"
      }
    },
    {
      "name": "memory-capture",
      "observe": ["turn_completed"],
      "mcp": {
        "server": "mem0",
        "tool": "add_memory",
        "arguments": {
          "text": "User: ${user_message}\nAssistant: ${assistant_message}",
          "user_id": "your-user-id",
          "app_id": "your-project-id"
        }
      }
    }
  ]
}
JSON
```

Store the Mem0 API key in the plugin's private data directory. The prompt hides
the key while you paste it; the file is created with mode `0600`:

```sh
python3 - <<'PY'
import os
from getpass import getpass
from pathlib import Path

home = Path(os.environ.get("PICOCLAW_HOME", str(Path.home() / ".picoclaw"))).expanduser()
key_file = home / "plugin-data" / "mem0" / "mem0-api-key"
key_file.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
key_file.parent.chmod(0o700)
key_file.write_text(getpass("Mem0 API key: ").strip())
key_file.chmod(0o600)
print(f"Saved key to {key_file}")
PY
```

Merge these settings into `config.json`. Set `path` to the absolute path of the
cloned plugin. Keep any existing `tools.mcp.servers` entries; the discovery
settings shown here leave discovery enabled globally, while `deferred: false`
exposes Mem0's tools directly on every model call:

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "use_bm25": true,
        "use_regex": false
      }
    }
  },
  "plugins": {
    "enabled": true,
    "entries": {
      "mem0": {
        "enabled": true,
        "path": "/absolute/path/to/mem0-source/integrations/mem0-agent-plugin",
        "allow_hooks": true,
        "mcp_overrides": {
          "mem0": {
            "type": "streamable-http",
            "url": "https://mcp.mem0.ai/mcp",
            "deferred": false
          }
        },
        "mcp_bearer_token_files": {
          "mem0": "mem0-api-key"
        }
      }
    }
  }
}
```

Since `skill_names` and `agents` are omitted, PicoClaw loads all plugin skills
and enables the plugin for all configured agents. The token script assumes the
default plugin data directory; if `plugins.data_dir` is configured, save the key
under `<plugins.data_dir>/mem0/mem0-api-key` instead.

The [official Mem0 Agent Plugin](https://github.com/mem0ai/mem0/tree/main/integrations/mem0-agent-plugin)
ships a local MCP server; this configuration replaces it with Mem0's
[hosted MCP server](https://docs.mem0.ai/platform/mem0-mcp). The host-side
`deferred: false` option does not modify the portable plugin's `mcp.json`.
Other MCP servers continue to follow the global discovery setting unless they
have their own per-server override. Never put the API key literally in
`config.json`, `mcp.json`, or `hooks.json`.

Restart PicoClaw after saving the files. Ask it to remember a harmless fact,
then ask about that fact in a later turn. The completed turn triggers
`add_memory`; the following turn's `before_llm` hook calls `search_memories` and
adds the result to the model context. With tool feedback enabled, these
hook-triggered MCP calls are also shown in the conversation.

#### Tuning recall

`search_memories` on Mem0's hosted MCP server accepts `top_k`, the maximum
number of memories returned. Its default is 10, so set it explicitly to bound
how much memory context each turn injects. The hook above uses `top_k: 5`;
values of 2–3 work well for short, focused turns:

```json
"arguments": {
  "query": "${user_message}",
  "filters": {"AND": [{"user_id": "your-user-id"}, {"app_id": "your-project-id"}]},
  "top_k": 3
}
```

Do not use `limit`: the hosted server ignores it and returns its default of 10.
The Mem0 Platform API reference also lists `threshold`, but the hosted v3 search
endpoint ignores it, so it does not filter results. To reduce low-relevance
noise, lower `top_k` rather than relying on `threshold`; use the plugin's local
MCP server or filter in the agent when you need score-based filtering.

Filters accept a `created_at` range, for example
`{"AND": [{"user_id": "your-user-id"}, {"created_at": {"gte": "2026-01-01"}}]}`,
to prefer recent memories.

The same hook declarations work with other MCP providers by changing the
server, tool names and arguments. Supermemory, for example, uses
`search_memory(query, containerTag)` and
`add_memory(content, action, containerTag)`. Its hosted endpoint supports
OAuth with `picoclaw mcp login <plugin-id>:<server-name>`; API-key bearer
authentication also works. A real API test confirmed recall and capture with
Supermemory on 5 October 2026.

MCP arguments are filtered for configured secrets and the target server's
authentication values. The `${user_message}` and `${assistant_message}` shortcuts
are bounded to 6,000 bytes each; context added to the prompt is bounded to
12,000 bytes. Each agent's MCP server allowlist still applies.

### Process hooks

Process hook entries can share `hooks.json` with MCP actions. Put the `hooks`
object either inline in `plugin.json` or in `com.sipeed.picoclaw/hooks.json`;
using both is an error limited to the hook extension.

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

Process launch fields follow the same validation, environment and containment
rules as stdio MCP. The process implements PicoClaw's existing JSON-RPC hook
protocol: `hook.hello`, `hook.runtime_event`, and the requested interception
methods. `entries.<id>.config` is delivered as `config` in the hello payload.
Only declared stages and allowed agents are forwarded.

See [the hook protocol](../architecture/hooks/README.md). Hook failures use the
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
read-only package and writable data/toolchain exposure as described in the
[isolation documentation](https://github.com/afjcjsbx/picoclaw/blob/main/pkg/isolation/README.md).
The plugin loader never
silently broadens those OS permissions.

To disable a plugin, set its entry's `enabled` to false and reload. To uninstall,
disable it first, then remove its package directory. Remove its dedicated data
directory separately only if you want to discard persisted state.

## Development and validation

To write a package, follow [Writing a Plugin](writing-a-plugin.md) and lint it with
`picoclaw plugin validate`.

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
