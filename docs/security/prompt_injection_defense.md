# Prompt injection defense

PicoClaw wraps web results, MCP text results, and inbound messages from groups,
channels, and unrecognized direct senders in a nonce-marked boundary. The body
is passed through after chat-template tokens and forged boundary markers are
neutralized. Suspicious phrases are logged for monitoring; they never block a
result. This is defense in depth, not a guarantee that every model will follow
the boundary.

## Trust policy

Trust comes from source metadata. A direct message is treated as trusted only
when its sender exactly matches an entry in that channel's `allow_from` list.
Group and channel messages are untrusted even when their sender is allowlisted.
Unknown or wildcard senders are not treated as trusted. Ensure `allow_from`
contains only the owner identities that should retain the trusted-DM behavior.

Hook session keys can be classified with `security.ResolveHookExternalContentSource`;
the current hook RPC path does not expose a stable source session key to the
agent loop, so hook-specific wrapping is not wired separately.

## Configuration

Under `tools.prompt_injection`, `enabled` is the master switch. `wrap_web`,
`wrap_untrusted_channels`, and `wrap_mcp_results` control each ingress family.
`log_suspicious` enables warning logs and `max_wrapped_chars` limits the
sanitized input body in runes before it is wrapped; keeping the envelope intact
prevents truncation from removing its closing marker. Environment overrides use
the `PICOCLAW_TOOLS_PROMPT_INJECTION_` prefix.

Defaults enable all protection for configs loaded through PicoClaw because the
loader overlays user values onto `DefaultConfig`. When constructing `Config`
with a direct `json.Unmarshal`, initialize it from `DefaultConfig` first; a bare
zero-value struct cannot distinguish an omitted `enabled` field from `false`.

This wrapper does not prevent unsafe tool actions or data exfiltration. Keep
tool permissions, execution restrictions, and secret filtering enabled.
