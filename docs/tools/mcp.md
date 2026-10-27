# MCP tools

The MCP tool enables integration with external Model Context Protocol servers. Every tool exposed by a connected server becomes an individual agent tool named `mcp_<server>_<tool>`; the components are sanitized into valid identifiers, and a short hash suffix keeps names distinct (and within the 64-character API limit) when sanitization or truncation would collide.

If you prefer not to edit JSON manually, PicoClaw also provides an MCP configuration manager CLI:

- `picoclaw mcp add` — add or update a server (supports `--deferred` / `--no-deferred`)
- `picoclaw mcp list` — list all configured servers with status and deferred state
- `picoclaw mcp show <name>` — show full details and the tool list for one server
- `picoclaw mcp test <name>` — connectivity check for one server
- `picoclaw mcp remove <name>` — remove a server entry
- `picoclaw mcp edit` — open `config.json` in `$EDITOR` for advanced edits

These commands manage the same `tools.mcp.servers` section documented below. See [MCP Server CLI](../reference/mcp-cli.md) for command syntax, examples, and behavior details.

The web dashboard also provides an MCP page with a **Servers** tab for monitoring configured servers and a **Catalog** tab with one-click presets for popular servers. Presets that require credentials prompt for them during installation.

## Tool Discovery (Lazy Loading)

When connecting to multiple MCP servers, exposing hundreds of tools simultaneously can exhaust the LLM's context window
and increase API costs. The **Discovery** feature solves this by keeping MCP tools *hidden* by default.

Instead of loading all tools, the LLM is provided with a lightweight search tool (using BM25 keyword matching or Regex).
When the LLM needs a specific capability, it searches the hidden library. Matching tools are then temporarily "unlocked"
and injected into the context for a configured number of turns (`ttl`).

## Global Config

| Config                 | Type   | Default | Description                                  |
|------------------------|--------|---------|----------------------------------------------|
| `enabled`              | bool   | false   | Enable MCP integration globally              |
| `discovery`            | object | `{}`    | Configuration for Tool Discovery (see below) |
| `max_inline_text_chars`| int    | 16384   | Head+tail budget for MCP text: beyond this, the model keeps the first 40% and the last 60% of the sanitized text (with an omission notice) and the full payload is saved as a workspace artifact |
| `servers`              | object | `{}`    | Map of server name to server config          |

Oversized MCP text results are bounded, not dropped: the model keeps the first 40% and the last 60% of the sanitized text around an `[MCP RESULT TRUNCATED - N chars omitted out of T total]` notice, while the full raw payload is saved to `<workspace>/.artifacts/mcp/` and exposed via a `[file:...]` artifact path. When the text result is a JSON object shaped `{"result": <text>, ...}` with only known keys and a non-empty string `result`, it is unwrapped before the write: the artifact holds the wrapped text with real newlines plus a `<mcp-result-metadata>` block carrying siblings embedded in that text object, so `read_file`/`search_files` can page it instead of reading one escaped JSON line; anything unrecognized stays verbatim. The protocol-level `structuredContent` and `_meta` fields of `CallToolResult` are separate from the text content and are not included by this rewrite. If the artifact write fails, the bounded view stays inline and the payload is handed to the generic large-result spillover, which saves it under `<workspace>/.artifacts/tool/` instead (using the same pageable text for an unwrapped payload).

## Discovery Config (`discovery`)

| Config               | Type | Default | Description                                                                                                                       |
|----------------------|------|---------|-----------------------------------------------------------------------------------------------------------------------------------|
| `enabled`            | bool | false   | Global default: if `true`, all MCP tools are hidden and loaded on-demand via search; if `false`, all tools are loaded into context and discovery is off. Individual servers can only opt *out* with `deferred: false` while this is `true`; a per-server `deferred: true` has no effect when this is `false`. |
| `ttl`                | int  | 5       | Number of conversational turns a discovered tool remains unlocked                                                                 |
| `max_search_results` | int  | 5       | Maximum number of tools returned per search query                                                                                 |
| `use_bm25`           | bool | true    | Enable the natural language/keyword search tool (`tool_search_tool_bm25`). **Warning**: consumes more resources than regex search |
| `use_regex`          | bool | false   | Enable the regex pattern search tool (`tool_search_tool_regex`)                                                                   |

> **Note:** If `discovery.enabled` is `true`, you MUST enable at least one search engine (`use_bm25` or `use_regex`),
> otherwise the application will fail to start.

## Per-Server Config

| Config     | Type    | Required | Description                                                                                                                                                     |
|------------|---------|----------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | bool    | yes      | Enable this MCP server                                                                                                                                          |
| `deferred` | bool    | no       | Per-server override, effective only when `discovery.enabled` is `true`. `true` = tools are hidden and discoverable via search; `false` = tools are always visible in context. When omitted, the global `discovery.enabled` value applies. Ignored (tools always visible) when `discovery.enabled` is `false`, because the discovery search tools are not registered in that case. |
| `type`     | string  | no       | Transport type: `stdio`, `sse`, `http` (alias: `streamable-http`)                                                                                               |
| `command`  | string  | stdio    | Executable command for stdio transport                                                                                                                          |
| `args`     | array   | no       | Command arguments for stdio transport                                                                                                                           |
| `env`      | object  | no       | Environment variables for stdio process                                                                                                                         |
| `env_file` | string  | no       | Path to environment file for stdio process                                                                                                                      |
| `inherit_env` | bool | no | Pass the full PicoClaw process environment to this stdio server. Defaults to `false`. |
| `url`      | string  | sse/http | Endpoint URL for `sse`/`http` transport                                                                                                                         |
| `headers`  | object  | no       | HTTP headers for `sse`/`http` transport                                                                                                                         |
| `oauth`    | object  | no       | Browser OAuth for remote servers; run `picoclaw mcp login <name>` before connecting. Cannot be combined with an `Authorization` header.                         |

## Transport Behavior

- If `type` is omitted, transport is auto-detected:
    - `url` is set → `sse`
    - `command` is set → `stdio`
- `http` and `sse` both use `url` + optional `headers`. `streamable-http` is accepted as an alias for `http`.
- Remote requests are bound to the configured origin. A redirect must keep the same host and port; an `http` to `https` upgrade on the same host is allowed, and anything else fails the connection instead of being followed. Configured headers are never replayed to another origin.
- Headers generated by the client for the protocol (`Accept`, `Content-Type`, `Mcp-Session-Id`, and the OAuth authorization) take precedence over entries of the same name in `headers`. Every other configured header is applied to each request.
- `env`, `env_file`, and `inherit_env` are only applied to `stdio` servers.
- By default, stdio servers inherit only `PATH`, `HOME`, `USER`, `LANG`, `LC_ALL`, `TERM`, `SHELL`, `TMPDIR`, and `XDG_*` (plus basic Windows process variables on Windows). `env_file` adds variables, and `env` overrides both the file and inherited values. Set `inherit_env: true` only if the server needs the entire parent environment.
- `oauth: {}` enables browser login with dynamic client registration. Optional
  fields are `client_id`, `issuer`, `scopes`, and `callback_port`. See
  [MCP Server CLI](../reference/mcp-cli.md#picoclaw-mcp-login-and-logout).

## Configuration Examples

### 1) Stdio MCP server

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-filesystem",
            "/tmp"
          ]
        }
      }
    }
  }
}
```

### 2) Remote SSE/HTTP MCP server

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "remote-mcp": {
          "enabled": true,
          "type": "sse",
          "url": "https://example.com/mcp",
          "headers": {
            "Authorization": "Bearer YOUR_TOKEN"
          }
        }
      }
    }
  }
}
```

### 3) Massive MCP setup with Tool Discovery enabled

*In this example, the LLM will only see the `tool_search_tool_bm25`. It will search and unlock Github or Postgres tools
dynamically only when requested by the user.*

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true,
        "use_regex": false
      },
      "servers": {
        "github": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-github"
          ],
          "env": {
            "GITHUB_PERSONAL_ACCESS_TOKEN": "YOUR_GITHUB_TOKEN"
          }
        },
        "postgres": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-postgres",
            "postgresql://user:password@localhost/dbname"
          ]
        },
        "slack": {
          "enabled": true,
          "type": "slack",
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-slack"
          ],
          "env": {
            "SLACK_BOT_TOKEN": "YOUR_SLACK_BOT_TOKEN",
            "SLACK_TEAM_ID": "YOUR_SLACK_TEAM_ID"
          }
        }
      }
    }
  }
}
```

### 4) Mixed setup: per-server deferred override

*Discovery is enabled globally, but `filesystem` is pinned as always-visible while `context7` follows the global
default (deferred). `aws` explicitly opts in to deferred mode even though it is the same as the global default.*

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true
      },
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
          "deferred": false
        },
        "context7": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@upstash/context7-mcp"]
        },
        "aws": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "aws-mcp-server"],
          "deferred": true
        }
      }
    }
  }
}
```

> **Note:** A per-server `deferred` value only takes effect when `discovery.enabled` is `true`.
> It can pin a server's tools as always-visible (`"deferred": false`) or hidden (`"deferred": true`),
> but it cannot enable discovery on its own: with `discovery.enabled: false` the search tools are
> not registered, so `"deferred": true` is ignored and the server's tools stay visible.
