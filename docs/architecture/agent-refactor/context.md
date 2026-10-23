# Context

## What this document covers

This document makes explicit the boundaries of context management in the agent loop:

- what fills the context window and how space is divided
- what is stored in session history vs. built at request time
- when and how context compression happens
- how token budgets are estimated

These are existing concepts. This document clarifies their boundaries rather than introducing new ones.

---

## Context window regions

The context window is the model's total input capacity. Four regions fill it:

| Region | Assembled by | Stored in session? |
|---|---|---|
| System prompt | `BuildMessages()` — static + dynamic parts | No |
| Summary | `SetSummary()` stores it; `BuildMessages()` injects it | Separate from history |
| Session history | User / assistant / tool messages | Yes |
| Tool definitions | Provider adapter injects at call time | No |

`MaxTokens` (the output generation limit) must also be reserved from the total budget.

The available space for history is therefore:

```
history_budget = ContextWindow - system_prompt - summary - tool_definitions - MaxTokens
```

---

## ContextWindow vs MaxTokens

These serve different purposes:

- **MaxTokens** — maximum tokens the LLM may generate in one response. Sent as the `max_tokens` request parameter.
- **ContextWindow** — the model's total input context capacity.

These were previously set to the same value, which caused the summarization threshold to fire either far too early (at the default 32K) or not at all (when a user raised `max_tokens`).

Current default when not explicitly configured: `ContextWindow = MaxTokens * 4`.

---

## Session history

Session history stores only conversation messages:

- `user` — user input
- `assistant` — LLM response (may include `ToolCalls`)
- `tool` — tool execution results

Session history does **not** contain:

- System prompts — assembled at request time by `BuildMessages`
- Summary content — stored separately via `SetSummary`, injected by `BuildMessages`

This distinction matters: any code that operates on session history — compression, boundary detection, token estimation — must not assume a system message is present.

---

## Turn

A **Turn** is one complete cycle:

> user message -> LLM iterations (possibly including tool calls) -> final assistant response

This definition comes from the agent loop design (#1316). In session history, Turn boundaries are identified by `user`-role messages.

Turn is the atomic unit for compression. Cutting inside a Turn can orphan tool-call sequences — an assistant message with `ToolCalls` separated from its corresponding `tool` results. Compressing at Turn boundaries avoids this by construction.

`parseTurnBoundaries(history)` returns the starting index of each Turn.
`findSafeBoundary(history, targetIndex)` snaps a target cut point to the nearest Turn boundary.

---

## Compression paths

Three automatic compression paths exist, in order of preference. A fourth, user-triggered path (`/compact`) is described after them.

### 1. Threshold compaction (synchronous, before every LLM call)

`Pipeline.CompactBeforeLLM` runs before each LLM call of a turn, so it covers both the start of a turn and the middle of a tool loop. It only runs for turns with history and `EnableSummary` (not heartbeat, background or sub-turns).

It estimates the prompt about to be sent (`exec.messages` plus tool definitions) and compares it with `summarize_token_percent` of the usable window, i.e. `(ContextWindow - MaxTokens) * summarize_token_percent / 100`. This is the same budget the proactive check enforces, so the soft threshold always sits at or below the hard one. Message count is not a trigger (`summarize_message_threshold` is deprecated and ignored). The same value is reported as `SummarizeAtTokens` in `/context` and the UI.

When the threshold is reached it calls `ContextManager.Compact` with `Reason: ContextCompressReasonThreshold`, **blocking the turn** until it returns. If the session changed, it re-reads history and summary through `Assemble` and rebuilds `exec.messages` around the active turn (the same rebuild as the context-error retry, via `reassembleActiveTurn`; the active turn's messages are never dropped). The turn then carries on from the compacted context. `ErrNothingToCompact` means nothing changed and the prompt is kept. Compaction is best effort: any failure leaves the prompt as is, and the hard checks below remain the safety net.

- **legacy**: compaction runs synchronously on the turn's context, so a hard abort cancels it and a canceled summarization is never applied. It works in this order, stopping at the first step that changes the session:
  1. `summarizeSession` summarizes earlier Turns. The cut point is a Turn boundary (`findSafeBoundary`), so the active turn stays whole.
  2. `summarizeActiveTurn` summarizes the **middle of the active turn**. The turn's opening user message and its newest two tool rounds (an assistant message plus its tool results, never split) stay verbatim; the rounds in between, with their tool calls and bounded tool outputs, are folded into the session summary on top of the existing one. The newest round is never summarized because the model has not seen its results yet. It is skipped when the middle would reclaim less than a tenth of the threshold, so an LLM call is never spent on a few hundred tokens.
  3. Old tool outputs are cleared (`pruneOldToolResults`) when neither step applies.

  `CompactBeforeLLM` chains up to three passes per LLM call, so one call can both summarize earlier Turns and the turn's own middle. The turn then continues with the kept rounds, the summary in the system prompt, and its original user message first. The post-turn `Compact(summarize)` is a no-op for legacy: the work moved here, and being synchronous it can no longer race with messages appended while the summary is generated.
- **seahorse**: unchanged policy. A leaf compaction pass runs through `Engine.Compact`; if it creates no summary, `ErrNothingToCompact` is returned. Seahorse still compacts after the turn as before.

The summary is stored via `SetSummary`; `BuildMessages` injects it into the system prompt.

Summarization requests (legacy and seahorse) retry transient failures (rate limits, 5xx, timeouts, network errors: the `transientLLMRetryReason` classification the main loop uses) with the same `max_llm_retries` and `llm_retry_backoff_secs` backoff, and stop waiting when the turn is canceled. Other failures are not retried this way: legacy keeps its short retries and then falls back to a truncated transcript, and a failed seahorse pass leaves the turn to continue uncompacted.

### 2. Proactive budget check

`isOverContextBudget` runs before each LLM call.

Uses the full budget formula: `message_tokens + tool_def_tokens + MaxTokens > ContextWindow`. If over budget, triggers `forceCompression` and rebuilds messages before calling the LLM.

This prevents wasted (and billed) LLM calls that would otherwise fail with a context-window error.

### 3. Emergency compression (reactive)

`forceCompression` runs when the LLM returns a context-window error despite the proactive check.

Drops the oldest ~50% of Turns. If the history is a single Turn with no safe split point (e.g. one user message followed by a massive tool response), falls back to keeping only the most recent user message — breaking Turn atomicity as a last resort to avoid a context-exceeded loop.

Stores a compression note in the session summary (not in history messages) so `BuildMessages` can include it in the next system prompt.

This is the fallback for when the token estimate undershoots reality.

### 4. Manual compaction (`/compact`)

`/compact [focus]` calls `ContextManager.Compact` with `Reason: ContextCompressReasonManual`. Unlike the automatic paths it runs synchronously inside the command turn, so the reply can report the outcome. The optional `focus` text travels in `CompactRequest.Instructions` and is added to the summarizer prompts as a priority hint.

When nothing was summarized (history too short, or already compacted), `Compact` returns `ErrNothingToCompact` and the command replies "Nothing to compact yet." Other errors are reported as "Failed to compact context: ...".

- **legacy**: runs `summarizeSession` directly, so the cut point, the last-four-messages tail and the `agent.session.summarize` event match threshold compaction. It takes the same per-session dedup key; if a summarization is already in flight the command fails with "a summarization is already in progress" instead of racing it.
- **seahorse**: calls `Engine.CompactNow`, which runs one leaf pass followed by the condensed loop synchronously (the automatic path runs the condensed loop in a background goroutine). The fresh tail stays protected. It shares the `condensing` dedup map with the async loop.

Both implementations leave the automatic prompts unchanged when no focus is given.

---

## Token estimation

Estimation uses a heuristic of ~2.5 characters per token (`chars * 2 / 5`).

`estimateMessageTokens` counts:

- `Content` (rune count, for multibyte correctness)
- `ReasoningContent` (extended thinking / chain-of-thought)
- `ToolCalls` — ID, type, function name, arguments
- `ToolCallID` (tool result metadata)
- Per-message overhead (role label, JSON structure)
- `Media` items — flat per-item token estimate, added directly to the final count (not through the character heuristic, since actual cost depends on resolution and provider-specific image tokenization)

`estimateToolDefsTokens` counts tool definition overhead: name, description, JSON schema of parameters.

These are deliberately heuristic. The proactive check handles the common case; the reactive path catches estimation errors.

---

## Interface boundaries

Context budget functions (`parseTurnBoundaries`, `findSafeBoundary`, `estimateMessageTokens`, `isOverContextBudget`) are **pure functions**. They take `[]providers.Message` and integer parameters. They have no dependency on `AgentLoop` or any other runtime struct.

`BuildMessages` is the sole assembler of the final message array sent to the LLM. Budget functions inform compression decisions but do not construct messages.

`forceCompression` and `summarizeSession` mutate session state (history and summary). `BuildMessages` reads that state to construct context. The flow is:

```
budget check --> compression decision --> mutate session --> BuildMessages reads session --> LLM call
```

---

## Known gaps

These are recognized limitations in the current implementation, documented here for visibility:

- **The newest tool rounds are never summarized.** A turn whose last two rounds alone exceed the threshold stays over it until those rounds age out; the context-error retry remains the safety net. Summarizing a turn needs more than two rounds.

- **Token estimation is heuristic.** It does not account for provider-specific tokenization, exact system prompt size (assembled separately), or variable image token costs. The two-path design (proactive + reactive) is intended to tolerate this imprecision.

- **Reactive retry does not preserve media.** When the reactive path rebuilds context after compression, it currently passes empty values for media references. This is a pre-existing issue in the main loop, not introduced by the budget system.

---

## What this document does not cover

- How `AGENT.md` frontmatter configures context parameters — that is part of the Agent definition work
- How the context builder assembles context in the new architecture — that is upcoming work
- How compression events surface through the event system — that is part of the event model (#1316)
- Subagent context isolation — that is a separate track
