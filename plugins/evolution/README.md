# Evolution plugin (opt-in)

`evolution` used to be compiled into PicoClaw and ran at the end of every turn.
It is now an optional out-of-process plugin: the default PicoClaw binary contains
no evolution code, no config block, and no background goroutine. This directory
is included in the repository but is **disabled unless you explicitly install,
enable, and allow its hook**.

## What it does

- Listens for `agent.turn.end` runtime events.
- Writes a slim learning record when a turn completes.
- In `draft` mode, groups repeated successful patterns and prepares reviewable
  skill drafts.
- In `apply` mode, applies drafts that pass review to the workspace skills.

State lives in `PLUGIN_DATA`, not in the agent workspace.

## Build

The hook is a normal Go program in this repository:

```bash
make build-evolution-plugin
# -> plugins/evolution/bin/evolution-hook
```

The build is separate from the default PicoClaw build. `go build ./cmd/picoclaw`
never compiles this package.

## Enable

1. Put this directory under `~/.picoclaw/plugins/evolution` (or point
   `plugins.directories` at it).
2. Add an entry to `~/.picoclaw/config.json`:

```json
{
  "plugins": {
    "enabled": true,
    "entries": {
      "evolution": {
        "enabled": true,
        "allow_hooks": true,
        "config": {
          "enabled": "true",
          "mode": "observe",
          "min_task_count": "2",
          "min_success_ratio": "0.7",
          "cold_path_trigger": "after_turn",
          "llm_base_url": "https://api.openai.com/v1",
          "llm_api_key": "sk-...",
          "llm_model": "gpt-4o-mini"
        }
      }
    }
  }
}
```

3. Restart the gateway.

`allow_hooks` is a separate trust grant. Without it the plugin is loaded but
its hook is never mounted.

## Configuration keys

All values in `plugins.entries.evolution.config` are strings; quote numbers and
booleans in `config.json`. Unknown keys are ignored.

| Key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | Record turn cases. Set to `false` to keep the hook loaded but idle. |
| `mode` | `observe` | `observe`, `draft`, or `apply`. |
| `min_task_count` | `2` | Related task records required before a pattern is eligible. |
| `min_success_ratio` | `0.7` | Minimum cluster success ratio, between 0 and 1. |
| `cold_path_trigger` | `after_turn` | `after_turn`, `scheduled`, or `manual`. |
| `cold_path_times` | empty | Comma-separated `HH:MM` times for `scheduled`. |
| `state_dir` | plugin data | Optional state directory override. Relative paths resolve under `PLUGIN_DATA`. When set, all workspaces share it; leave empty for per-workspace state. |
| `llm_base_url` | empty | OpenAI-compatible base URL, for example `https://api.openai.com/v1`. |
| `llm_api_key` | empty | Bearer token. Leave empty for local servers that do not check it. |
| `llm_api_key_file` | empty | Read the bearer token from a file instead of config. Relative paths resolve under `PLUGIN_DATA`. |
| `llm_model` | empty | Model used for clustering and drafts. |
| `llm_timeout_seconds` | `45` | HTTP timeout for cold-path LLM requests. |

The LLM is optional. Without `llm_base_url` and `llm_model`, the plugin uses the
heuristic clusterer, heuristic success judge, and template draft generator. With
an OpenAI-compatible endpoint it can produce better drafts. The plugin never
reads the main agent's provider credentials; configuration is explicit.

## Manual cold path

For `cold_path_trigger: manual` (or for testing), run one pass:

```bash
PLUGIN_DATA=~/.picoclaw/plugin-data/evolution \
  plugins/evolution/bin/evolution-hook \
  --config ~/.picoclaw/evolution-plugin.json \
  --cold-path /path/to/workspace
```

## Resource trade-offs

- **Default (disabled):** zero CPU, zero allocations, zero bytes in the agent
  binary. Removing `pkg/evolution` takes about 5.9k non-test lines out of the
  default build; a stripped darwin/arm64 core build is roughly 0.36 MB smaller.
- **Enabled:** one long-lived child process (about 7 MB stripped on
  darwin/arm64, about 10 MiB idle RSS in a local measurement). The hook read
  loop only parses JSON and enqueues; work happens in a single worker goroutine.
  The queue is bounded at 32 turns, event text is capped at 4 KiB, tool
  executions at 128, and skill snapshots at 32, so a slow disk or LLM cannot
  grow memory without bound. Cold-path runs are one goroutine per workspace and
  use a timer, not a poll loop.
- **LLM calls:** only cold-path work uses the LLM, and only when explicitly
  configured. Observe mode makes no LLM calls at all.

## Security notes

- The plugin runs out of process and receives only hook events, not the agent's
  provider credentials.
- State is outside the workspace, which removes it from the agent's writable
  prompt-injection surface.
- Drafts are still untrusted text. Keep `apply` off unless you review the
  drafts; the underlying runtime keeps the same draft review and quarantine
  rules as before.
