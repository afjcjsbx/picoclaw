# Sensitive Data Filtering

PicoClaw can filter sensitive values (API keys, tokens, secrets, passwords) from tool call results before they are sent to the LLM. This prevents the LLM from seeing its own credentials, which could otherwise leak through tool output or cause confusing behavior.

---

## Overview

When the LLM uses a tool that returns its own credentials (e.g., a tool that echoes the API key being used), those values are automatically replaced with `[FILTERED]` in the message sent to the LLM.

Sensitive values are collected from [`.security.yml`](./credential_encryption.md) — the centralized storage for all sensitive configuration (API keys, tokens, secrets stored alongside `config.json`). This includes:

- Model API keys
- Channel tokens (Telegram, Discord, Slack, Matrix, etc.)
- Web tool API keys (Brave, Tavily, Perplexity, etc.)
- Skills tokens (GitHub, ClawHub)

---

## Configuration

Sensitive data filtering is configured in the `tools` section of `config.json`:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `filter_sensitive_data` | bool | `true` | Enable/disable filtering. When `false`, no filtering is performed. |
| `filter_min_length` | int | `8` | Minimum content length to trigger filtering. Short content is skipped for performance. |

```json
{
  "tools": {
    "filter_sensitive_data": true,
    "filter_min_length": 8
  }
}
```

### Environment Variable

| Variable | Description |
|----------|-------------|
| `PICOCLAW_TOOLS_FILTER_SENSITIVE_DATA` | Set to `true` or `false` to override the config value |

---

## How It Works

1. **On startup**: All sensitive values are collected from `.security.yml` using reflection and compiled into a `strings.Replacer` (O(n+m) performance, computed once).

2. **Per tool result**: Before sending any tool result content to the LLM:
   - If `filter_sensitive_data` is `false`, content is passed through unchanged
   - If content length < `filter_min_length`, content is passed through unchanged (fast path)
   - Otherwise, all sensitive values are replaced with `[FILTERED]`

3. **Replacement**: Uses `strings.Replacer` for efficient O(n+m) string substitution, where n = content length and m = total sensitive value length.

---

## Built-in redaction in file reads and search results

`search_files` and `read_file` scrub their own output before the pipeline filter even runs. Rendered result lines and read content are checked against well-known credential formats — Bearer headers, JWTs, `sk-`/`rk-` keys, GitHub tokens, AWS access key IDs, Slack `xox...` tokens, Google `AIza...` keys — and against a high-entropy heuristic that catches long machine-generated tokens, including on content that mentions a secret-like identifier (`key`, `token`, `password`, ...). Matches are replaced with `[FILTERED]`. Heuristics are evaluated per line, so a secret-like word on one line does not change how other lines are treated.

This layer does not depend on `filter_sensitive_data` or `.security.yml`: secrets found in files are masked even when they are not among the configured values. Ordinary code identifiers, long prose words and plain hex hashes (commit ids, checksums) are deliberately left untouched to keep results usable. When `filter_sensitive_data` is enabled, the pipeline filter then runs on top as a second layer.

`read_file` redaction affects only the rendered text: byte offsets and line numbers still refer to the raw file, so the header range and the pagination arguments (`offset`, `start_line`) keep working even when a masked value is shorter than the original.

---

## Logging

Tool call logs (registry, tool loop and agent execution pipeline) record only the argument names and their count, never the argument values. Secrets passed to a tool are therefore not written to log files. This is independent of `filter_sensitive_data`, which only applies to the content sent to the LLM.

---

## Large tool-result artifacts

Large tool results (over `tools.max_inline_result_chars`, default `16384`) are saved to the agent workspace under `<workspace>/.artifacts/tool/`, while a bounded head+tail preview and a pointer stay inline in the model context. See [Large tool results](../reference/tools_configuration.md#large-tool-results) for the full behavior.

The spill happens inside tool-result normalization, **before** `filter_sensitive_data` is applied. The filter runs later and only on the content that is injected into the model context, so the two surfaces diverge:

- **Model context**: still filtered — the preview embedded in the result passes through `filter_sensitive_data` like any other tool content.
- **Artifact on disk**: **not filtered** — it contains the raw tool output, including any secret that the filter would otherwise have replaced with `[FILTERED]`.
- **Model access**: the artifact path is exposed to the model (a structured `[file:...]` tag plus an inline pointer). The model can read it with `read_file` or forward it with `send_file`; `send_file` delivers the raw file without filtering.

This applies to every tool. It was already true for MCP large-text artifacts, but the behavior is now generalized to all tools (for example, a large `exec` result can persist raw credentials to the workspace even when filtering is enabled). The artifact directory is created with `0700` and files with `0600`, but permissions do not redact content.

If your threat model requires that secrets never reach disk unfiltered, disable the spill (`max_inline_result_chars: 0`), avoid running commands that echo credentials, and treat `<workspace>/.artifacts/` as sensitive data. A future improvement would be to apply the filter (or an equivalent redaction) to the artifact text before it is written.

---

## Example

Given the following `.security.yml`:

```yaml
model_list:
  my-model:
    api_keys:
      - sk-secret-key-12345

channels:
  telegram:
    token: "123456:ABC-DEF"
```

And a tool result containing:

```
The model is using API key sk-secret-key-12345 and Telegram bot 123456:ABC-DEF
```

The LLM will receive:

```
The model is using API key [FILTERED] and Telegram bot [FILTERED]
```

---

## Performance

- **Fast path**: Content shorter than `filter_min_length` (default 8) is returned unchanged without any string scanning
- **Efficient replacement**: Uses `strings.Replacer` with O(n+m) complexity instead of regex
- **Lazy initialization**: The replacement map is built once on first access via `sync.Once`

---

## Security Considerations

- **Credential exposure prevention**: Without filtering, tools that echo credentials could cause the LLM to see its own API keys, potentially leading to confusion or credential leakage in logs
- **Defense in depth**: Filtering complements (but does not replace) credential encryption — both features should be used together
- **No false positives**: Only values explicitly stored in `.security.yml` are filtered; the LLM's general knowledge is unaffected
- **Artifacts bypass filtering on disk**: large tool-result artifacts are written before filtering and are not redacted — treat `<workspace>/.artifacts/` as sensitive (see [Large tool-result artifacts](#large-tool-result-artifacts))

---

## Related

- [Credential Encryption](./credential_encryption.md) — encrypting API keys in config
- [Tools Configuration](../reference/tools_configuration.md)
