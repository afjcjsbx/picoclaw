# Skills tools

The skills tool configures skill discovery and installation via registries like ClawHub and GitHub.

## Tools and switches

| Tool | What it does | Switch | Default |
|------|--------------|--------|---------|
| `find_skills` | Searches the configured registries for installable skills | `tools.find_skills.enabled` | `true` |
| `install_skill` | Downloads a skill from a registry into the workspace | `tools.install_skill.enabled` | `true` |

Both tools are registered only while `tools.skills.enabled` (default `true`) is on; disabling it removes them regardless of the individual switches. Environment overrides: `PICOCLAW_TOOLS_SKILLS_ENABLED`, `PICOCLAW_TOOLS_FIND_SKILLS_ENABLED`, `PICOCLAW_TOOLS_INSTALL_SKILL_ENABLED`.

`find_skills` arguments:

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `query` | string | yes | Search query describing the desired skill capability (for example `github integration`) |
| `limit` | int | no | Maximum results, 1–20 (default `5`) |

`install_skill` arguments:

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `slug` | string | yes | Unique slug of the skill to install (for example `github`) |
| `version` | string | no | Specific version to install; defaults to the latest |
| `registry` | string | no | Registry to install from (default `github`) |
| `force` | bool | no | Reinstall even if the skill already exists (default `false`) |

## Registries

| Config                             | Type   | Default              | Description                                  |
|------------------------------------|--------|----------------------|----------------------------------------------|
| `registries.clawhub.enabled`       | bool   | true                 | Enable ClawHub registry                      |
| `registries.clawhub.base_url`      | string | `https://clawhub.ai` | ClawHub base URL                             |
| `registries.clawhub.auth_token`    | string | `""`                 | Optional Bearer token for higher rate limits |
| `registries.clawhub.search_path`   | string | `""`                 | Search API path                              |
| `registries.clawhub.skills_path`   | string | `""`                 | Skills API path                              |
| `registries.clawhub.download_path` | string | `""`                 | Download API path                            |
| `registries.clawhub.timeout`       | int    | 0                    | Request timeout in seconds (0 = default)     |
| `registries.clawhub.max_zip_size`  | int    | 0                    | Max skill zip size in bytes (0 = default)    |
| `registries.clawhub.max_response_size` | int | 0                   | Max API response size in bytes (0 = default) |
| `registries.github.enabled`        | bool   | true                 | Enable GitHub installs via registry config    |
| `registries.github.base_url`       | string | `https://github.com` | GitHub or GitHub Enterprise base URL          |
| `registries.github.auth_token`     | string | `""`                | GitHub personal access token                  |
| `registries.github.proxy`          | string | `""`                | HTTP proxy for GitHub API requests            |

The ClawHub token is optional and only raises the rate limit. The GitHub token
is needed for searches and for private repositories. Tokens are secrets: pass
them through `PICOCLAW_SKILLS_REGISTRIES_CLAWHUB_AUTH_TOKEN` and
`PICOCLAW_SKILLS_REGISTRIES_GITHUB_AUTH_TOKEN`.

## Search Settings

| Config                    | Type | Default | Description                                |
|---------------------------|------|---------|--------------------------------------------|
| `max_concurrent_searches` | int  | 2       | Max concurrent skill search requests       |
| `search_cache.max_size`   | int  | 50      | Max cached search results                  |
| `search_cache.ttl_seconds`| int  | 300     | Cache TTL in seconds                       |

## Configuration Example

```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "enabled": true,
          "base_url": "https://clawhub.ai",
          "auth_token": "",
          "search_path": "",
          "skills_path": "",
          "download_path": "",
          "timeout": 0,
          "max_zip_size": 0,
          "max_response_size": 0
        },
        "github": {
          "enabled": true,
          "base_url": "https://github.com",
          "auth_token": "",
          "proxy": ""
        }
      },
      "max_concurrent_searches": 2,
      "search_cache": {
        "max_size": 50,
        "ttl_seconds": 300
      }
    }
  }
}
```
