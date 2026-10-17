# Web tools

Web tools are used for web search and fetching. They are two tools with separate switches:

- `web_search` is enabled by `tools.web.enabled` (default `true`).
- `web_fetch` is enabled by `tools.web_fetch.enabled` (default `true`).

Provider settings and fetch settings live under `tools.web.*`.

## Provider selection

| Config              | Type   | Default  | Description                                                                                   |
|---------------------|--------|----------|-----------------------------------------------------------------------------------------------|
| `provider`          | string | `auto`   | Search provider to use: `auto`, `brave`, `duckduckgo`, `gemini`, `glm_search`, `baidu_search`, `kagi`, `keenable`, `perplexity`, `searxng`, `sogou`, or `tavily`. Unknown values fall back to `auto` |
| `prefer_native`     | bool   | `true`   | Prefer the active LLM's built-in web search (for example OpenAI's native search) over the configured search engines. The client-side `web_search` tool is hidden while native search is available, and PicoClaw falls back to it automatically |

With `auto`, PicoClaw picks the first *ready* provider (enabled and fully configured for its type). Order: `perplexity`, `brave`, `kagi`, `searxng`, `tavily`, `gemini`, `keenable`; then `sogou`/`duckduckgo` (choosing between the two based on the query when both are ready); finally `baidu_search` and `glm_search`. Sogou, DuckDuckGo, and Keenable work without a key; SearXNG needs a `base_url`; the other providers need an API key. A ready provider configured explicitly in `provider` wins over the order.

## Web Fetcher
General settings for fetching and processing webpage content.

| Config              | Type   | Default       | Description                                                                                   |
|---------------------|--------|---------------|-----------------------------------------------------------------------------------------------|
| `tools.web_fetch.enabled` | bool | `true`   | Register the `web_fetch` tool to fetch and process webpage content                             |
| `tools.web.fetch_limit_bytes` | int | 10485760 | Maximum size of the webpage payload to fetch, in bytes (default is 10MB).                  |
| `tools.web.format`  | string | "plaintext"   | Output format of the fetched content. Options: `plaintext` or `markdown` (recommended).       |
| `tools.web.proxy`   | string | ""            | Optional proxy URL for web tools (`http`, `https`, `socks5`, `socks5h`). For authenticated proxies, prefer the `HTTP_PROXY`/`HTTPS_PROXY` environment variables instead of embedding credentials in config |

## Brave

| Config        | Type     | Default | Description                                    |
|---------------|----------|---------|------------------------------------------------|
| `enabled`     | bool     | false   | Enable Brave search                            |
| `api_key`     | string   | -       | Brave Search API key                           |
| `api_keys`    | string[] | -       | Multiple API keys for rotation (takes priority over `api_key`) |
| `max_results` | int      | 5       | Maximum number of results                      |

## DuckDuckGo

| Config        | Type | Default | Description               |
|---------------|------|---------|---------------------------|
| `enabled`     | bool | false   | Enable DuckDuckGo search  |
| `max_results` | int  | 5       | Maximum number of results |

## Sogou

| Config        | Type | Default | Description               |
|---------------|------|---------|---------------------------|
| `enabled`     | bool | true    | Enable Sogou search       |
| `max_results` | int  | 5       | Maximum number of results |

Sogou and DuckDuckGo are keyless engines; when both are ready, `auto` picks between them based on the query language.

## Gemini Google Search

Gemini search uses Gemini with Google Search grounding. It returns an AI-synthesized answer with citations from Google Search.

| Config        | Type   | Default              | Description                       |
|---------------|--------|----------------------|-----------------------------------|
| `enabled`     | bool   | false                | Enable Gemini Google Search       |
| `api_key`     | string | -                    | Google Gemini API key             |
| `model`       | string | `gemini-2.5-flash`   | Gemini model used for search      |
| `max_results` | int    | 5                    | Maximum number of citations       |

```json
{
  "tools": {
    "web": {
      "gemini": {
        "enabled": true,
        "api_key": "YOUR_GEMINI_API_KEY",
        "model": "gemini-2.5-flash",
        "max_results": 5
      }
    }
  }
}
```

## Baidu Search

Baidu Search uses the [Qianfan AI Search API](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5), which is AI-powered and optimized for Chinese-language queries.

| Config        | Type   | Default                                                | Description               |
|---------------|--------|--------------------------------------------------------|---------------------------|
| `enabled`     | bool   | false                                                  | Enable Baidu Search       |
| `api_key`     | string | -                                                      | Qianfan API key           |
| `base_url`    | string | `https://qianfan.baidubce.com/v2/ai_search/web_search` | Baidu Search API URL      |
| `max_results` | int    | 5                                                      | Maximum number of results |

```json
{
  "tools": {
    "web": {
      "baidu_search": {
        "enabled": true,
        "api_key": "YOUR_BAIDU_QIANFAN_API_KEY",
        "max_results": 10
      }
    }
  }
}
```

## Perplexity

| Config        | Type     | Default | Description                                    |
|---------------|----------|---------|------------------------------------------------|
| `enabled`     | bool     | false   | Enable Perplexity search                       |
| `api_key`     | string   | -       | Perplexity API key                             |
| `api_keys`    | string[] | -       | Multiple API keys for rotation (takes priority over `api_key`) |
| `max_results` | int      | 5       | Maximum number of results                      |

## Kagi Search

Kagi Search uses the official Kagi OpenAPI client for `POST /search` and returns normal web results from `data.search`.

| Config        | Type     | Default                               | Description                                    |
|---------------|----------|---------------------------------------|------------------------------------------------|
| `enabled`     | bool     | false                                 | Enable Kagi Search                             |
| `api_key`     | string   | -                                     | Kagi API key                                   |
| `api_keys`    | string[] | -                                     | Multiple API keys for rotation (takes priority over `api_key`) |
| `base_url`    | string   | `https://kagi.com/api/v1/search`      | Kagi Search API endpoint                       |
| `max_results` | int      | 5                                     | Maximum number of results                      |

```json
{
  "tools": {
    "web": {
      "provider": "kagi",
      "kagi": {
        "enabled": true,
        "max_results": 5,
        "base_url": "https://kagi.com/api/v1/search"
      }
    }
  }
}
```

Store Kagi API keys in `.security.yml`:

```yaml
web:
  kagi:
    api_keys:
      - "YOUR_KAGI_API_KEY"
```

Kagi API usage may be billed or limited separately from a normal Kagi subscription, depending on your account and API setup.

## Keenable

Keenable works without an API key. With no key configured, requests go to the public endpoint (`POST /v1/search/public`), which is rate limited per IP (10 requests/second, 1000 per hour). Setting a key switches to `POST /v1/search` and lifts those limits; nothing else changes.

| Config        | Type     | Default                   | Description                                                    |
|---------------|----------|---------------------------|----------------------------------------------------------------|
| `enabled`     | bool     | false                     | Enable Keenable search                                         |
| `api_keys`    | string[] | -                         | Optional API keys; rotated like other providers                |
| `base_url`    | string   | `https://api.keenable.ai` | Keenable API base URL                                          |
| `max_results` | int      | 5                         | Maximum number of results                                      |

```json
{
  "tools": {
    "web": {
      "provider": "keenable",
      "keenable": {
        "enabled": true,
        "max_results": 5
      }
    }
  }
}
```

To lift the keyless rate limits, store a key in `.security.yml`:

```yaml
web:
  keenable:
    api_keys:
      - "YOUR_KEENABLE_API_KEY"
```

The `range` parameter maps to `published_after` (`d`, `w`, `m`, `y` become a date 1 day, 7 days, 1 month, or 1 year before today).

## Tavily

| Config        | Type   | Default | Description               |
|---------------|--------|---------|---------------------------|
| `enabled`     | bool   | false   | Enable Tavily search      |
| `api_key`     | string | -       | Tavily API key            |
| `base_url`    | string | -       | Custom Tavily API base URL |
| `max_results` | int    | 5       | Maximum number of results |

## SearXNG

| Config        | Type   | Default                 | Description               |
|---------------|--------|-------------------------|---------------------------|
| `enabled`     | bool   | false                   | Enable SearXNG search     |
| `base_url`    | string | `http://localhost:8888` | SearXNG instance URL      |
| `max_results` | int    | 5                       | Maximum number of results |

## GLM Search

| Config          | Type   | Default                                           | Description               |
|-----------------|--------|---------------------------------------------------|---------------------------|
| `enabled`       | bool   | false                                             | Enable GLM Search         |
| `api_key`       | string | -                                                 | GLM API key               |
| `base_url`      | string | `https://open.bigmodel.cn/api/paas/v4/web_search` | GLM Search API URL        |
| `search_engine` | string | `search_std`                                      | Search engine type        |
| `max_results`   | int    | 5                                                 | Maximum number of results |

## Additional Web Settings

| Config                   | Type     | Default | Description                                                    |
|--------------------------|----------|---------|----------------------------------------------------------------|
| `private_host_whitelist` | string[] | `[]`    | Private/internal hosts allowed for web fetching                |

## `web_search` Tool Parameters

At runtime, the `web_search` tool accepts the following parameters:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | yes | Search query string |
| `count` | integer | no | Number of results to return. Default: `10`, max: `10` |
| `range` | string | no | Optional time filter: `d` (day), `w` (week), `m` (month), `y` (year) |

If `range` is omitted, PicoClaw performs an unrestricted search.
For Kagi, `d`, `w`, and `m` map to Kagi lens `time_relative`; `y` maps to a lens `time_after` date one year before the current day.

## Example `web_search` Call

```json
{
  "query": "ai agent news",
  "count": 10,
  "range": "w"
}
```

## `web_fetch` Tool Parameters

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `url` | string | yes | URL to fetch |
| `maxChars` | integer | no | Maximum characters to extract (minimum `100`). Content beyond the cap keeps the first 40% and the last 60% around a `[CONTENT TRUNCATED - N chars omitted out of T total]` notice. Default: `2000000` |

The default cap is a flood guard, not a context budget: pages fetched without `maxChars` (or under it) keep their full text, which the generic [large tool result handling](../reference/tools_configuration.md#large-tool-results) turns into a head+tail preview plus a pageable workspace artifact instead of a lossy cut. When the cap does apply, the spillover saves the full pre-truncation page (when the result is oversized), so the omitted middle stays readable.
