# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.1] - 2026-10-18

### Added

- Configurable head+tail truncation for oversized tool output: `tools.exec.max_output_chars` (default 50,000), `tools.read_file.max_lines` (default 2,000) and `tools.read_file.max_line_length` (default 2,000), plus a 40/60 head+tail window for `web_fetch` and MCP text results.
- Large tool results above `tools.max_inline_result_chars` (default 16,384) are spilled to `<workspace>/.artifacts/` with a bounded inline preview; recognized `web_fetch` and MCP JSON envelopes are rewritten as pageable text.
- `/compact [focus]` command to compact the current session on demand, with an optional priority hint for the summarizer.
- Cron tool `run` action to execute a job immediately without changing its schedule, and hot-reload of `cron/jobs.json` while the gateway is running.
- `opencode-go` provider with per-model-family routing across OpenAI Chat Completions, OpenAI Responses, Anthropic Messages and Google Gemini endpoints.
- `picoclaw plugin` CLI with `list`, `show`, `add`, `remove`, `enable`, `disable`, `edit`, `validate`, `init` and `test`, plus `plugins.Validate` for package linting.
- BytePlus (`byteplus`) as an OpenAI-compatible provider, including `thinking_level: "off"` support.
- Additive tool-definition injection in `before_llm` plugin hooks.
- Configurable per-stream message-bus backpressure budgets, including the audio chunk stream, and `HealthCheckWithThreshold` for readiness checks.
- Runtime memory tuning via `PICOCLAW_MEMORY_LIMIT_MB` and `PICOCLAW_GOGC`, with transient heap release after large media payloads.

### Changed

- Default SubTurns per parent turn lowered from 5 to 3 to reduce memory use on low-RAM devices; set `agents.defaults.subturn.max_concurrent` to restore 5. Values above 32 are clamped to 32.
- Per-model `rpm` is now enforced for single-candidate calls and configured streaming and image-model candidates, not only inside the fallback chain. Existing configs that set `rpm` on a model without fallbacks are now throttled; waits of one second or more are logged.
- Cron: a job no longer runs concurrently with itself. A scheduled occurrence that falls due while the previous run of the same job is still executing is skipped while the schedule still advances.
- Cron `cron_expr` schedules honor the process time zone (`TZ`); the agent prompt now shows the zone, and the docs explain that there is no per-job time zone.
- Tool feedback is merged per LLM response on channels that cannot edit messages, so repeated tool updates do not flood the chat.
- Vision: inline images are downscaled, EXIF orientation is baked in, and pixel/size limits are enforced before decoding; image generation streams decoded images to disk.
- Providers stream JSON request bodies instead of buffering them.
- Seahorse bounds its SQLite connection pool, applies pragmas to every connection and moves temporary storage to disk; MCP server startup concurrency is bounded and isolation preflight is cached per configuration generation.
- Media cleanup uses an idle-based TTL and rejects invalid cleanup policies.
- Documentation restructured with a new landing page and Web UI guide, reorganized navigation and dedicated tool pages; README sections trimmed.
- Updated dependencies, including `openai-go` v3.77.0, `anthropic-sdk-go`, `slack-go` v0.30.1, `copilot-sdk` v1.0.19, `aws-sdk-go-v2` v1.47.3, Lark SDK v3.12.0, Pion RTP v1.10.5, `golang.org/x/net` v0.61.0, `golang.org/x/sync` v0.24.0 and `golang.org/x/oauth2` v0.37.0, plus frontend dependencies.
- Documented that round-robin load balancing is applied when a model is resolved (agent startup or `/model` switch), not per request.

### Removed

- Unused channel settings fields (`discord.settings.mention_only`, `onebot.settings.group_trigger_prefix`, `pico.settings.write_timeout`); group triggering is configured through `group_trigger`.
- Android section from the documentation and Android library builds from CI.
- `ROADMAP.md` and its dangling references.

### Fixed

- Gateway hot-reload: changed channels no longer lose their worker, the config race is fixed and the watcher is repaired.
- Agent routing now scores real history and media and hardens attachment detection; the self-evolution runtime is aligned with the documented spec.
- Agent surfaces token-limit truncation instead of a generic empty response, clarifies compaction feedback and releases inbound media after the consuming turn completes.
- Cron: one-time jobs that expired while the service was down are finalized, due jobs run concurrently, recurring runs are anchored to the scheduled time to avoid drift, `wakeChan` is buffered, `ListJobs`/`EnableJob` return deep copies, the job handler is read under lock, and Pico jobs are scoped to the channel instead of a single chat.
- MCP: case-only server names stay distinct in tool identifiers, discovery TTL advances once per turn, the closed flag is set under lock to drain in-flight calls, and large-text artifacts are bounded.
- Plugin hooks: the `turn_completed` alias is honored for process hooks, hooks are closed outside the manager lock, the effective observer timeout is logged, a `respond` hook that bypasses approval for registered tools logs a warning, hook text is truncated safely and the plugin MCP hook turn cache is bounded.
- Plugins: `read_resource` is exposed for references-only plugins.
- Seahorse and media: the session cursor is drained before per-session queries, fallback summaries truncate without splitting UTF-8 runes, and DeltaChat/Weixin subprocesses are routed through isolation.
- Isolation: absolute path arguments are exposed read-only.
- Security: writes to sensitive files are protected; filesystem reads block case variants of protected paths, resolved credential symlink targets and Windows extended drive paths; large tool-result artifacts are documented as unfiltered.
- Providers: a model written with a leading slash (for example `/gpt-4o`) resolves to the default provider instead of failing with `unknown protocol ""`; streamed JSON request bodies are replayable; serialization context is preserved on JSON encoding errors; the encoder goroutine stops when request build fails; image generation caps the provider error body kept in messages.
- Web search: oversized responses return an explicit error, SearXNG decoding is bounded, and upstream error bodies are truncated.
- Channels: Telegram retries media downloads and reports failed attachments instead of dropping them; QQ auth is restored with a pinned botgo fork; Weixin treats only `ret=-2` as a final failure, reserves reply slots for the final answer and does not retry API-rejected messages.
- Heartbeat: synchronous responses are delivered, state is reloaded before resolving the last channel, startup is only announced when enabled and the first-run timer is canceled on service stop.
- Events: the `Crash` panic policy is honored and recovered panics are counted; `bus.message.dropped` is documented.
- Netbind: partial listener binds are reported instead of silently dropping an IP family.
- Web UI: cron run sessions no longer shadow Pico chats in the session list.
- Installer: the latest release tag is resolved from the redirect.
- Frontend: vulnerable transitive dependencies are patched.
- Config migration preserves the default `tools.max_inline_result_chars` for migrated configs.
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

[0.5.1]: https://github.com/afjcjsbx/picoclaw/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/afjcjsbx/picoclaw/compare/v0.4.0...v0.5.0
