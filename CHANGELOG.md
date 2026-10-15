# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- BytePlus (`byteplus`) as an OpenAI-compatible provider, including `thinking_level: "off"` support.

### Changed

- Removed unused channel settings fields (`discord.settings.mention_only`, `onebot.settings.group_trigger_prefix`, `pico.settings.write_timeout`); group triggering is configured through `group_trigger`.
- Per-model `rpm` is now enforced for single-candidate calls, configured streaming and image-model candidates, not only inside the fallback chain. Existing configs that set `rpm` on a model without fallbacks will now be throttled; waits of one second or more are logged.
- Documented that round-robin load balancing is applied when a model is resolved (agent startup or `/model` switch), not per request.

### Fixed

- A model written with a leading slash (for example `/gpt-4o`) now resolves to the default provider instead of failing with `unknown protocol ""`.
- Providers guide: the Z.AI Coding Plan provider id is `zai`, not `zai-coding`.

## [0.5.0] - 2026-10-11

### Added

- Agent Plugins 1.0.0 support, including MCP-backed lifecycle hooks and OAuth login for MCP servers.
- Filesystem search tool (`search_files`).
- Image generation tool, including OpenRouter generation support.
- Voice mode command with synthesized final chat replies.
- Session-scoped todo planning tool with enforced single `in_progress` task.
- Conversation forks and history labels in the Web UI.
- Cross-platform Web UI installers.
- macOS Seatbelt isolation backend.
- Configurable automatic session pruning with DB size, age and count guards.
- Repeated tool-call loop detection.
- Bounded log rotation via gateway log size/backup settings.
- Configurable low-power profile.
- Opt-in custom channel build tags and optional Seahorse in custom builds.
- `/list plugins` command and a readable `/subagents` task tree.
- Summarization and compaction reporting on channels.
- Pruning of old tool results before context compression.
- Five parallel turns by default.
- Protection of workspace secret files from filesystem reads.

### Changed

- Exec tool: improved shell guidance, per-run timeout support and float64-only timeout argument.
- Renamed protected read files.
- Refactored cron enable/disable commands and dropped the unused payload kind field.
- Consolidated shared helpers for paths, strings, MIME extension mapping and inline content sanitization.
- Indexed `message_parts` by `message_id` for faster lookups.
- Lowered the recommended session prune `max_db_size_mb` to 128.
- DeltaChat channel: safer credentials handling, more configuration options and improved documentation.
- Bumped Go to 1.27.2 and `golang.org/x/net` to v0.60.0.

### Removed

- `/btw` side-question command.
- Compatibility facades, tool path forwarding wrappers and unused CLI version wrappers.
- DeltaChat config keys `password`, `imap_server`, `imap_port`, `smtp_server`, `smtp_port`; `invite_link` was renamed to `join_invite_link`.

### Fixed

- SeaHorse: delete `messages_fts` rows by rowid to avoid O(n^2) clears, batch-load message parts in `GetMessages`, apply `busy_timeout` to every connection and avoid needless `VACUUM`s.
- Session pruning: keep session config on save, never age-prune on an untrustworthy clock, always keep the most recently active session, and prune every agent's session store.
- Agent: invalidate prompt cache when daily notes change, serialize concurrent daily-note appends, and make SeaHorse `Close` wait for the prune loop.
- Isolation: probe optional bubblewrap (`bwrap`) namespaces so the sandbox still works when optional namespaces are unavailable.
- Channel manager: fix a send-on-closed-channel race.
- HTTP server: do not exit the process on a shared HTTP server error.
- Agent: omit the cron tool from scheduled turns.
- Commands: correct `/model` list behavior.
- Plugins: secure plugin data directories and escape plugin hook context.
- Web UI: paginate long chat history and constrain the fallback chain dialog.
- Frontend: override the vulnerable `lodash-es` dependency.
- Providers: support `openai-go` v3.70.0.
- SubTurn: remove send-on-closed-channel race and finish turns gracefully on natural completion.
- Cron: allow clearing a scheduled command without confirmation and mark jobs skipped when no handler is configured.
- Search tool: stop directory traversal, skip special files before opening and paginate only displayed results.
- Agent: preserve loop-detection warnings and allow status polling.
- Providers: canonicalize the Keenable API header.
- Steering: inject post-response messages only once and let steering interrupt critical loop termination.
- Tools: route `AsyncExecutor` tools through `ExecuteAsync`, allow `timeout=0` on background exec runs, and avoid logging tool argument values and guard media store reads.
- Plugins: omit the plugin usage hint without a resource tool, reject package FIFOs before opening, restrict resource reads and MCP header env expansion, and hide plugin host paths from skill prompts.
- Image/voice: pin downloads to public IP addresses, scale response limits with media size and synchronize TTS provider reload.
- MCP: limit inherited stdio environment, allow OAuth store loading on Windows and accept case-insensitive localhost endpoints.
- Authentication: use configured scopes when refreshing OAuth tokens and adapt the Copilot provider to SDK v1.
- Updater: use fork releases for self-update and select the matching arch asset.

[unreleased]: https://github.com/afjcjsbx/picoclaw/compare/v0.4.0...HEAD
[0.5.0]: https://github.com/afjcjsbx/picoclaw/compare/v0.4.0...v0.5.0