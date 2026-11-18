---
description: "Fixes for common PicoClaw problems: model_list errors, OpenRouter free-tier routing, channels that fail to connect, and how to read the logs."
---
# Troubleshooting

## "model ... not found in model_list" or OpenRouter "free is not a valid model ID"

**Symptom:** You see either:

- `Error creating provider: model "openrouter/free" not found in model_list`
- OpenRouter returns 400: `"free is not a valid model ID"`

**Cause:** PicoClaw now resolves provider/model in two steps:

- If `provider` is set, the `model` field is sent to that provider unchanged.
- If `provider` is omitted, PicoClaw infers the provider from the first `/` segment and sends everything after that first `/` as the runtime model ID.

For OpenRouter free-tier routing, the preferred config is explicit `provider`.

- **Wrong:** `"model": "free"` → no OpenRouter provider is selected, so `free` is not a valid OpenRouter model route.
- **Right:** `"provider": "openrouter", "model": "free"` → OpenRouter receives `free`.
- **Also supported:** `"model": "openrouter/free"` → provider resolves to `openrouter`, runtime model ID resolves to `free`.

**Fix:** In `~/.picoclaw/config.json` (or your config path):

1. **agents.defaults.model_name** must match a `model_name` in `model_list` (e.g. `"openrouter-free"`).
2. That entry should preferably set **provider** to `openrouter`, and **model** should be a valid OpenRouter model ID, for example:
   - `"free"` – auto free-tier
   - `"google/gemini-2.0-flash-exp:free"`
   - `"meta-llama/llama-3.1-8b-instruct:free"`

Example snippet:

```json
{
  "agents": {
    "defaults": {
      "model_name": "openrouter-free"
    }
  },
  "model_list": [
    {
      "model_name": "openrouter-free",
      "provider": "openrouter",
      "model": "free",
      "api_keys": ["sk-or-v1-YOUR_OPENROUTER_KEY"],
      "api_base": "https://openrouter.ai/api/v1"
    }
  ]
}
```

Get your key at [OpenRouter Keys](https://openrouter.ai/keys).

## OpenCode Go returns 400 "Model is unavailable"

**Symptom:** A model entry sets `provider` explicitly and repeats the provider
name in `model`:

```json
{"provider": "opencode-go", "model": "opencode-go/glm-5.3-flash"}
```

OpenCode Go answers `400 Upstream request failed: Model is unavailable`.

**Cause:** OpenCode Go model IDs are flat. Repeating the provider name breaks
both PicoClaw's endpoint routing (the vendor prefix is used to pick the wire
protocol) and the upstream model lookup.

**Fix:** Use the bare model ID:

```json
{"provider": "opencode-go", "model": "glm-5.3-flash"}
```

Current builds drop a redundant `opencode-go/` prefix before routing, so the
prefixed form also works. The misleading upstream error remains possible for a
genuinely unknown model ID.
