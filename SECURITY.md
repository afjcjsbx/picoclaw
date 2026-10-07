# Security Policy

PicoClaw is under rapid development. It has not reached v1.0, and no public security audit is documented here. Do not rely on it as a security boundary or deploy it in production without your own risk assessment.

## Reporting a Vulnerability

Please do not report an unpatched vulnerability in a public issue, discussion, or pull request. Submit it privately through [GitHub Security Advisories](https://github.com/afjcjsbx/picoclaw/security/advisories/new). If private reporting is unavailable, use a private contact method listed on the maintainer's [GitHub profile](https://github.com/afjcjsbx).

Include the affected version or commit, operating system, relevant configuration, steps to reproduce, impact, and any suggested mitigation. Redact credentials and personal data. Maintainers will coordinate disclosure and fixes; there is no response-time guarantee.

## Supported Versions

There is no formal support window. Use the latest release, and include the affected versions when reporting an issue. Older releases may not receive security fixes.

## Deployment Guidance

### Protect credentials and local data

- Keep API keys, channel tokens, and other credentials in `~/.picoclaw/.security.yml` or environment variables where possible. Restrict the security file to its owner (`chmod 600`) and protect `config.json` as well if it contains credentials. See [Security Configuration](docs/security/security_configuration.md) and [Credential Encryption](docs/security/credential_encryption.md).
- Never commit credentials, session data, logs, or backups. `.security.yml` is already listed in `.gitignore`.
- Workspace data can contain conversation history, memory, skills, and tool output. Limit filesystem access to the PicoClaw user and protect backups.
- Sensitive-data filtering can redact values loaded from `.security.yml` in tool output, but it is a defense in depth and does not guarantee that every secret or personal detail is removed. See [Sensitive Data Filtering](docs/security/sensitive_data_filtering.md).
- Revoke and rotate credentials promptly if they may have been exposed.

### Restrict who can use the bot

- For channels that support `allow_from`, set it to the user IDs that should be allowed. An empty list allows everyone; use `"*"` only when open access is intentional.
- Configure group triggers where supported. Without a trigger, some channels respond to all group messages.
- Treat incoming messages and attachments as untrusted input. Review permissions independently of model instructions.

### Limit tools and integrations

- Enable only the tools you need. Review third-party skills, MCP servers, hooks, and scheduled jobs before enabling them; they can extend what the agent can access or execute.
- PicoClaw's `exec` command checks blocklisted patterns, but this is not a complete sandbox: it cannot inspect commands later started by an allowed script or build tool. Use a container, VM, or approval flow for untrusted code. See [Tools Configuration](docs/reference/tools_configuration.md).
- `restrict_to_workspace` limits PicoClaw's file tools; it does not isolate the main PicoClaw process. Optional process isolation is disabled by default, applies to child processes, and has platform-specific limits. See [Process Isolation](pkg/isolation/README.md).
- Run PicoClaw as a dedicated, unprivileged user and avoid giving its workspace access to sensitive host files.

### Consider data sent to external services

Prompts and selected conversation content are sent to the configured model provider; channel and tool integrations may send data to their own services. Review each provider's privacy and retention terms, and avoid sending data you are not authorized to share.

### Keep deployments current

Use releases from the [official PicoClaw repository](https://github.com/afjcjsbx/picoclaw) and update regularly. Review dependency and release advisories for the channels and integrations you enable.

## If You Suspect a Compromise

1. Revoke exposed API keys, bot tokens, and other credentials; issue replacements.
2. Stop the affected instance and review its configuration, workspace, and logs for unexpected access or changes.
3. Update to a fixed release when available, restore trusted data as needed, and report the incident privately.

## Official Sources

The official project repository is [github.com/afjcjsbx/picoclaw](https://github.com/afjcjsbx/picoclaw). PicoClaw has not issued any cryptocurrency or official token; claims otherwise are scams.
