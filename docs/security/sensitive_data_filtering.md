---
description: "PicoClaw can filter sensitive values (API keys, tokens, secrets, passwords) from tool call results before they are sent to the LLM."
---
# Sensitive Data Filtering

PicoClaw can filter sensitive values (API keys, tokens, secrets, passwords) from tool call results before they are sent to the LLM. This prevents the LLM from seeing its own credentials, which could otherwise leak through tool output or cause confusing behavior.

---

## Overview

When the LLM uses a tool that returns its own credentials (e.g., a tool that echoes the API key being used), those values are automatically replaced with `[FILTERED]` in the message sent to the LLM and in large tool-result artifacts written to disk.

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

## Built-in redaction in search results, file reads, and edit diffs

`search_files`, `read_file`, and the model-facing context lines of `edit_file` diffs scrub their own output before the pipeline filter even runs. Rendered result lines are checked against well-known credential formats — Bearer headers, JWTs, `sk-`/`rk-` keys, GitHub tokens, AWS access key IDs, Slack `xox...` tokens, Google `AIza...` keys — and against a high-entropy heuristic that catches long machine-generated tokens, including on lines that mention a secret-like identifier (`key`, `token`, `password`, ...). Matches are replaced with `[FILTERED]`. Redaction runs before the 240-byte output window is applied, so a token crossing the cut is masked in full instead of leaving a fragment behind.

For `edit_file`, only the unchanged context lines are scrubbed: added and removed lines stay verbatim because the model supplied them, and the chat preview keeps the raw diff. Redaction runs before the diff is bounded, so a credential that would straddle the head-and-tail truncation cut is masked in full rather than left as a fragment.

`read_file` redaction affects only the rendered text: byte offsets and line numbers still refer to the raw file, so the header range and the pagination arguments (`offset`, `start_line`) keep working even when a masked value is shorter than the original. Heuristics are per line, so a secret-like word on one line does not change how other lines are treated.

`read_file` output is exempt from the generic large-base64 omission: a long run of base64 characters in a file (hex, DNA, low-entropy blobs; random base64 is masked above) is ordinary content, not an inline media payload, and the read limits plus the large-result spill already bound it. Every other tool keeps the omission guard, and data URLs and invisible Unicode are still stripped from `read_file` results.

This layer does not depend on `filter_sensitive_data` or `.security.yml`: secrets found in files are masked even when they are not among the configured values. Ordinary code identifiers, long prose words and plain hex hashes (commit ids, checksums) are deliberately left untouched to keep results usable. When `filter_sensitive_data` is enabled, the pipeline filter then runs on top as a second layer.

---

## Logging

Tool call logs (registry, tool loop and agent execution pipeline) record only the argument names and their count, never the argument values. Secrets passed to a tool are therefore not written to log files. This is independent of `filter_sensitive_data`, which only applies to the content sent to the LLM.

---

## Large tool-result artifacts

Large tool results (over `tools.max_inline_result_chars`, default `16384`) are saved to the agent workspace under `<workspace>/.artifacts/tool/`, while a bounded head+tail preview and a pointer stay inline in the model context. `web_fetch` defaults `maxChars` to that same threshold and may only lower it per call; a smaller preview still writes an artifact even when the page is under the threshold (capped at `2,000,000` characters), so the full page remains recoverable. See [Large tool results](../reference/tools_configuration.md#large-tool-results) for the full behavior.

Artifacts are redacted **while they are written**. Every artifact path — the generic tool-result spill, exec's streamed `exec_*.log`, and MCP's `.artifacts/mcp` files — goes through the same exact-value redaction layer used for logs and outbound messages. Configured credentials from `.security.yml` are replaced with `[FILTERED]` as bytes are streamed to the file, before they reach disk. A credential split across write chunks or across a head+tail truncation boundary is detected too: the stream writer holds back at most one byte less than the longest configured value, and truncation cuts are moved so a secret is never split into two fragments.

- **Model context**: still filtered — the preview embedded in the result passes through `filter_sensitive_data` like any other tool content.
- **Artifact on disk**: configured credentials are already masked with `[FILTERED]`, both in the full-payload artifact and in the exec stream. Redaction is independent of `filter_sensitive_data`: disabling the model-facing filter does not cause credentials to be written in clear.
- **Memory**: the artifact is never materialized as a redacted copy. Redaction streams through a small carry buffer (one byte less than the longest registered credential, typically a few dozen bytes), so peak memory stays bounded by the I/O chunk and the existing head/tail window.
- **Residual risk**: values that are **not** registered in `.security.yml` are not detectable by exact-value redaction. Treat `<workspace>/.artifacts/` as sensitive data when commands can emit credentials that PicoClaw does not know about, and do not forward artifacts with `send_file` to untrusted destinations.

The artifact directory is created with `0700` and files with `0600`. Permissions still complement, but do not replace, content redaction.

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
- **Streaming artifacts**: Artifact redaction is O(n) and holds back only a carry buffer no larger than the longest registered credential, so persisting a multi-megabyte spill does not create a second in-memory copy

---

## Security Considerations

- **Credential exposure prevention**: Without filtering, tools that echo credentials could cause the LLM to see its own API keys, potentially leading to confusion or credential leakage in logs
- **Defense in depth**: Filtering complements (but does not replace) credential encryption — both features should be used together
- **No false positives**: Only values explicitly stored in `.security.yml` are filtered; the LLM's general knowledge is unaffected
- **Artifacts are redacted on disk**: large tool-result artifacts and exec spill files stream through the same exact-value redaction, independent of `filter_sensitive_data` (see [Large tool-result artifacts](#large-tool-result-artifacts)). Credentials not present in `.security.yml` still require treating `<workspace>/.artifacts/` as sensitive

---

## Related

- [Credential Encryption](./credential_encryption.md) — encrypting API keys in config
- [Tools Configuration](../reference/tools_configuration.md)
