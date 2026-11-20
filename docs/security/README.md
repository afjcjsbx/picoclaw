---
description: "PicoClaw security: hardening configuration, subprocess isolation, credential and secret handling, sensitive data redaction, and provider authentication."
---
# Security

Security-focused docs covering configuration, subprocess isolation, secrets handling, and provider auth.

- [Security Configuration](security_configuration.md): security-related config knobs and hardening guidance.
- [Subprocess Isolation](isolation.md): optional OS-level sandbox for exec, CLI providers, hooks, MCP servers and plugins, with `expose_paths` and `block_network` across Linux, macOS and Windows.
- [Sensitive Data Filtering](sensitive_data_filtering.md): defense-in-depth redaction of exact configured secrets from tool output; not a DLP boundary.
- [Credential Encryption](credential_encryption.md): encrypting stored API keys and credentials.
- [Antigravity Authentication & Integration Guide](ANTIGRAVITY_AUTH.md): auth flow and integration notes for the Antigravity provider.
