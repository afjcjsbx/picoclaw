# Seahorse Memory System

> Back to [README](README.md) · Related: [Session System](session-system.md),
> [Session Startup Cost and Pruning](session-startup-and-pruning.md),
> [Context Compression](agent-refactor/context.md)

Seahorse is PicoClaw's optional, SQLite-backed **short-term context manager**. It
indexes the conversation history of every session, compresses older turns into
summaries, assembles a budget-aware prompt for each LLM call, and exposes two
retrieval tools (`short_grep` and `short_expand`) so the model can recover
details that were compacted away.

This document describes the system as it is implemented, including the parts
where the code, comments, tool descriptions, or older documentation disagree.
Those mismatches are collected in [Known gaps](#known-gaps) and should be read
before relying on a behavior described only in a tool description.

Seahorse is **not** the long-term memory system. `memory/MEMORY.md` is a
separate workspace file injected into the system prompt by the context builder.
Seahorse indexes session transcripts only: it does not index `MEMORY.md`,
`AGENT.md`, skills, cron definitions, or arbitrary workspace files.

## Contents

- [At a glance](#at-a-glance)
- [Enabling Seahorse](#enabling-seahorse)
- [Runtime architecture](#runtime-architecture)
- [Storage layout](#storage-layout)
- [Token estimation](#token-estimation)
- [Data model and ordinals](#data-model-and-ordinals)
- [Write path: ingestion](#write-path-ingestion)
- [Startup bootstrap and reconciliation](#startup-bootstrap-and-reconciliation)
- [Context assembly](#context-assembly)
- [Compaction](#compaction)
- [Retrieval tools](#retrieval-tools)
- [Configuration reference](#configuration-reference)
- [Pruning and disk management](#pruning-and-disk-management)
- [Operations](#operations)
- [Performance characteristics](#performance-characteristics)
- [Security and privacy](#security-and-privacy)
- [Hot reload behavior](#hot-reload-behavior)
- [Known gaps](#known-gaps)
- [Source map](#source-map)

## At a glance

| Property | Value |
| --- | --- |
| Activation | `agents.defaults.context_manager: "seahorse"` |
| Default manager | `legacy` (empty string or `legacy`) |
| Storage | SQLite, pure-Go `modernc.org/sqlite` |
| Default database | `<default-agent-workspace>/sessions/seahorse.db` |
| Canonical history | Session JSONL store, written by the agent runtime |
| SQLite role | Derived full-text index plus compacted context view |
| Indexing | FTS5 with the `trigram` tokenizer |
| Prompt budget | `context_window - max_tokens`, with a fallback |
| Fresh tail | Last 32 context items are protected from automatic leaf compaction |
| Retrieval tools | `short_grep`, `short_expand` |
| Pruning | Opt-in, through `session.prune`, only active with Seahorse |
| Encryption | None |

## Enabling Seahorse

Seahorse is opt-in. The default configuration keeps the legacy context manager,
which stores the summary in the session store and does not create a SQLite
database.

```json
{
  "agents": {
    "defaults": {
      "context_manager": "seahorse"
    }
  }
}
```

Environment override:

```sh
PICOCLAW_AGENTS_DEFAULTS_CONTEXT_MANAGER=seahorse
```

Selection happens once, in `AgentLoop.resolveContextManager`
(`pkg/agent/turn_coord.go`):

1. Empty string or `"legacy"` selects the legacy manager.
2. Any other name is looked up in the context-manager registry.
3. An unknown name logs a warning and falls back to legacy.
4. If the factory returns an error, the error is logged and the runtime falls
   back to legacy.

The `seahorse` factory is registered in every build so the name is always
recognized, but the factory itself can fail:

- With the `no_seahorse` build tag, it returns "not included in this build".
- On `mipsle`, `netbsd`, and `freebsd` with `arm`, it returns "unavailable on
  this platform".
- If the SQLite database cannot be opened or migrated, it returns the
  underlying error.

In all of those cases the gateway still starts, using legacy context handling.
This is deliberate, but it means a broken or unreadable `seahorse.db` degrades
memory behavior with only a warning instead of failing loudly.

### Removal from a build

`no_seahorse` removes the `pkg/seahorse` import and its SQLite dependency from
the `picoclaw` executable when no other selected feature needs SQLite. See
[Custom Builds](../guides/custom-builds.md#omit-seahorse) for build tags,
binary-size measurements, and the Matrix and WhatsApp Native exceptions.

### What is not configurable

`agents.defaults.context_manager_config` is accepted by the config schema and
passed to the factory, but `newSeahorseContextManager` ignores it. The engine
always receives only `DBPath`:

```go
engine, err := seahorse.NewEngine(seahorse.Config{
    DBPath: dbPath,
}, completeFn)
```

Consequences:

- The database cannot be moved through configuration.
- `ignoreSessionPatterns` and `statelessSessionPatterns` cannot be configured.
  The `seahorse.Config` fields exist and are implemented, but the agent adapter
  never sets them. Only the hardcoded `heartbeat` pattern is active.
- There is no per-agent Seahorse configuration. The manager is built from the
  default agent, and every agent shares the resulting database.

Settings are read at startup. A hot reload swaps the provider and agent
registry, but it does not rebuild the context manager. See
[Hot reload behavior](#hot-reload-behavior).

## Runtime architecture

```
                 inbound turn
                      |
                      v
        +----------------------------+
        |  ContextManager.Assemble   |
        |  (Seahorse adapter)        |
        +-------------+--------------+
                      |
          reads context_items + summaries
                      |
                      v
        +----------------------------+
        |  BuildMessages             |
        |  summary XML + system      |
        +-------------+--------------+
                      |
                      v
                    LLM call
                      |
                      v
        +----------------------------+
        |  JSONL session store       |
        |  canonical history         |
        +-------------+--------------+
                      |
                      v
        +----------------------------+
        |  ContextManager.Ingest     |
        |  derived SQLite index      |
        +-------------+--------------+
                      |
                      v
        +----------------------------+
        |  Compact (leaf + optional  |
        |  condensed summaries)      |
        +----------------------------+
```

The JSONL history is the source of truth. SQLite is a derived index and a
compacted view. The two can diverge, so startup runs a reconciliation pass for
the sessions known to the default agent.

### Context manager interface

| Method | Seahorse implementation |
| --- | --- |
| `Assemble` | Computes the effective budget, calls `Engine.Assemble`, converts messages back to provider messages, and returns summary XML separately |
| `Compact` | Dispatches on `Reason`: manual uses `CompactNow`, retry uses `CompactUntilUnder`, everything else uses `Engine.Compact` |
| `Ingest` | Converts one provider message and calls `Engine.Ingest` |
| `Clear` | Calls `Engine.ClearSession`, then clears the owning agent's JSONL history and summary |
| `Close` | Optional method detected by the agent loop; stops the prune loop and closes the engine |

### Components

| Component | File | Responsibility |
| --- | --- | --- |
| Agent adapter | `pkg/agent/context_seahorse.go` | Factory, startup bootstrap, pruning loop, assembly budget, message conversion, tool registration |
| Engine | `pkg/seahorse/short_engine.go` | Lifecycle, ingestion, session filtering, bootstrap, clear/delete, database settings |
| Compaction | `pkg/seahorse/short_compaction.go` | Leaf and condensed summarization, prompts, fallbacks, manual and emergency paths |
| Assembler | `pkg/seahorse/short_assembler.go` | Budget selection, fresh-tail trimming, summary XML, compression warning |
| Store | `pkg/seahorse/store.go` | Schema operations, messages, parts, summaries, context items, search, vacuum |
| Schema | `pkg/seahorse/schema.go` | Tables, FTS5, triggers, indexes, migrations |
| Retrieval | `pkg/seahorse/short_retrieval.go` | `Grep`, `ExpandMessages`, duration parsing |
| Tools | `pkg/seahorse/tool_grep.go`, `pkg/seahorse/tool_expand.go` | Tool schemas, argument parsing, output shaping |
| Constants | `pkg/seahorse/short_constants.go` | Fanout, tail, chunk, target, and iteration limits |

## Storage layout

### Database path and sharing

The adapter resolves the workspace from the **default agent**:

```go
agent := al.registry.GetDefaultAgent()
dbPath := agent.Workspace + "/sessions/seahorse.db"
```

With the default workspace this is `~/.picoclaw/workspace/sessions/seahorse.db`.
The directory is created with mode `0755` if missing. Every agent, including
routed agents with different workspaces, writes into this one database. Session
keys are globally unique (`sk_v1_<sha256>`), so conversations do not collide,
but the database is a shared trust boundary.

### SQLite settings

| Setting | Value | Where |
| --- | --- | --- |
| Journal mode | `WAL` | Set once with `PRAGMA journal_mode = WAL` |
| Busy timeout | `5000` ms | DSN, applied to every pooled connection |
| Synchronous | `NORMAL` | DSN pragma |
| Temp store | `FILE` (1) | DSN pragma, spills sorts and temp tables to disk |
| Max open connections | `2` | `sqliteMaxOpenConns` |
| Max idle connections | `2` | Same as open cap |
| Ingest chunk size | `200` messages | `IngestBatchChunkSize` |
| Foreign keys | Not enabled | No `PRAGMA foreign_keys = ON` |

Two connections allow one writer next to one reader; further callers wait. The
cap bounds the per-connection page cache on memory-constrained devices. A long
`VACUUM` or prune pass can hold the write lock, and writers then fail after the
busy timeout. Readers in WAL mode are not blocked by a writer.

Batch ingest is chunked to `IngestBatchChunkSize` messages per transaction for
the same reason: one transaction for a whole-history bootstrap would hold the
single write lock long enough to starve other writers on slow storage.

### Tables

| Table | Purpose |
| --- | --- |
| `conversations` | One row per session key, with `created_at` and `updated_at` |
| `messages` | Raw message rows: role, content, model, reasoning, token count, timestamp |
| `message_parts` | Structured parts: `text`, `tool_use`, `tool_result`, `media` |
| `summaries` | Leaf and condensed summaries with depth and descendant metadata |
| `summary_parents` | Edges from a condensed summary to the summaries it was built from |
| `summary_messages` | Edges from a leaf summary to its source messages |
| `context_items` | The ordered list of messages and summaries that form the current context |
| `summaries_fts` | FTS5 index over summary content |
| `messages_fts` | FTS5 index over message content |

`context_items` has primary key `(conversation_id, ordinal)` and stores either a
`message_id` or a `summary_id`. It is the only table compaction rewrites.

`messages_fts` uses `message_id` as the FTS5 `rowid` and keeps `message_id` as an
`UNINDEXED` column, so the delete trigger is a rowid lookup instead of a full
scan. `summaries_fts` stores `summary_id` as an indexed column and joins on it.

### Triggers and indexes

Six triggers keep the FTS tables in sync:

- `summaries_ai`, `summaries_ad`, `summaries_au` insert, delete, and reinsert
  summary rows in `summaries_fts`.
- `messages_ai`, `messages_ad`, `messages_au` do the same for `messages_fts`,
  using `rowid = message_id`.

Indexes cover conversation and time lookups on messages, message parts,
summaries, summary parents, and context items.

### Migrations

`runSchema` is idempotent. It creates missing tables and then runs three
migrations:

1. Add `messages.reasoning_content` if missing.
2. Add `messages.model_name` if missing.
3. Rebuild a legacy `messages_fts` table that did not declare `message_id` as
   `UNINDEXED`, repopulating it from `messages` with `rowid = message_id`.

Migration 3 is a full rebuild of the message FTS index and can be expensive on a
large database. It runs synchronously at startup.

### FTS5 availability

`checkFTS5Support` verifies that SQLite was compiled with FTS5. If the trigram
tokenizer cannot be created, it only logs a warning. The schema still declares
`tokenize="trigram"`, so a build without trigram fails table creation and the
engine factory fails, which makes the runtime fall back to legacy. The warning
understates the outcome.

## Token estimation

Seahorse uses the shared heuristic in `pkg/tokenizer`, the same one used by the
agent prompt budget:

- `chars * 2 / 5`, approximately 2.5 characters per token.
- Character counts are rune counts, so CJK and emoji are counted per character.
- The estimate includes `Content`, `ReasoningContent`, tool call IDs, tool call
  type, function name, function arguments, and `ToolCallID`.
- Each message adds 12 characters of JSON/role overhead.
- Each media item adds a flat 256 tokens, because image tokenization depends on
  the provider and resolution.
- Structured system parts are counted as an alternative representation, not in
  addition to `Content`.

Message token counts are computed once by the adapter when a message is
ingested, using the original provider message. They are stored in
`messages.token_count`, copied into `context_items.token_count`, and summed for
the context size. Summaries are estimated from their generated text only. If a
context item has a zero token count, the assembler falls back to the message or
summary row.

These are estimates, not provider tokenizer counts. The hard proactive budget
check and the overflow retry exist because the estimate can undershoot.

## Data model and ordinals

A conversation is a session key. A message belongs to a conversation. A summary
belongs to a conversation and has a kind (`leaf` or `condensed`) and a depth
(`0` for leaf, `max(parent depth) + 1` for condensed). A context item points at
one message or one summary and carries an ordinal.

Ordinals are spaced by `OrdinalStep = 100`:

```
100  message
200  message
300  summary   <- leaf compaction replaced messages 100..200
400  message
```

New items are appended at `max(ordinal) + 100`. When compaction replaces a
range, the new summary is inserted at the integer midpoint of the replaced
range, which leaves room for later inserts between neighbors. If the midpoint is
already occupied, the store resequences the whole context and appends the new
summary at the end.

Messages and summaries are never removed by compaction. Only `context_items` is
rewritten. Raw history remains in `messages`, and every summary remains in
`summaries`, so retrieval can still find compacted content.

### Identifiers

- Conversation IDs are SQLite autoincrement integers.
- Message IDs are SQLite autoincrement integers and are global across
  conversations.
- Summary IDs are strings of the form `sum_<unix-nanoseconds-in-hex>`.
- Session keys are opaque strings. Seahorse does not parse them.

`short_grep` returns message IDs as strings because the tool JSON encodes
`int64` with `,string`. `short_expand` accepts strings or numbers.

## Write path: ingestion

The agent calls `ContextManager.Ingest` after each persisted message. The
adapter converts one provider message to one Seahorse message and calls
`Engine.Ingest` with a one-element slice.

Ingestion is independent of `EnableSummary`. A turn with history enabled but
summarization disabled still writes messages into SQLite; only `NoHistory`
suppresses both assembly and persistence. Heartbeat turns and sub-turns use
`NoHistory` and therefore do not touch Seahorse. Background task turns keep
history enabled, so they are indexed normally.

`Engine.Ingest`:

1. Returns `nil, nil` for an ignored session (`heartbeat`).
2. Returns `nil, nil` for a stateless session (never configured in practice).
3. Locks one of 256 FNV-1a sharded mutexes for the session key. Different
   sessions can ingest concurrently; the same session is serialized.
4. Gets or creates the `conversations` row.
5. Writes the messages with `AddMessagesBatch`, which inserts each message and
   its parts and appends the resulting IDs to `context_items` in the same
   transaction. Context ordinals continue from `max(ordinal) + 100`, `+200`, and
   so on.
6. Returns the message count and the sum of the input token estimates.

`AddMessagesBatch` splits the batch into chunks of `IngestBatchChunkSize` (200)
messages, one transaction per chunk. Two properties follow:

- A chunk is atomic. A failure rolls back that chunk completely, including the
  FTS entries written by the `messages_ai` trigger, so a chunk never leaves
  messages without context items.
- A batch is **not** atomic across chunks. Chunks committed before the failing
  one stay visible. `AddMessagesBatch` returns a nil ID slice with the error, so
  callers must not read a partial failure as "nothing was written".

The chunk size is a write-lock bound, not a memory bound. SQLite serializes
writers, so a whole-history bootstrap in one transaction would hold the lock for
the entire batch and push concurrent writers (another session's ingest,
compaction) into `SQLITE_BUSY` after the busy timeout. Chunking keeps the lock
window short while still removing the per-message commit.

The adapter sends one message per `Ingest`, so the hot path is one chunk of one
message: two commits before this change (message, then context append), one
after. Bootstrap delta ingestion passes the remaining history and gets one
commit per 200 messages instead of one per message plus a context append.

Ingest errors are logged by the agent and do not fail the turn. Because assembly
reads SQLite, a message that failed to ingest is missing from the next prompt
even though it exists in JSONL. It reappears only after a successful restart
bootstrap. A chunk lost to a mid-batch failure behaves the same way: the
messages stay in JSONL and the next bootstrap re-ingests them.

### Provider message conversion

`providerToSeahorseMessage` maps:

| Provider field | Seahorse representation |
| --- | --- |
| `Role` | `Message.Role` |
| `Content` | `Message.Content` |
| `ModelName` | `Message.ModelName` |
| `ReasoningContent` | `Message.ReasoningContent` |
| `CreatedAt` | UTC, truncated to the second; zero becomes "now" at insert |
| `ToolCalls` | One `tool_use` part per call, with name, arguments, ID |
| `ToolCallID` | A `tool_result` part with the ID and `Text = Content` |
| `Media` | One `media` part per URI, with no MIME type |
| `Attachments` | Dropped |
| `SystemParts` | Dropped |

Token count is computed from the provider message before this conversion.

The content Seahorse receives is the LLM-facing content, after any large tool
result spilling or truncation and after sensitive-data filtering. It is not
necessarily the full raw tool output. If the tool layer replaced a result with
an artifact reference, Seahorse indexes only that reference.

### Content derivation and fidelity

When a message has at least one part, `AddMessageWithPartsAndReasoning` does
**not** store the original `Content`. It stores a readable rendering derived from
the parts:

```go
text                  -> p.Text
tool_use              -> "[tool_use: <name>, args: <arguments>]"
tool_result           -> "[tool_result for <toolCallId>: <text>]"
media                 -> "[media: <uri> (<mime>)]"
unknown type          -> p.Text
```

The parts are stored separately, in order: tool calls, then tool result, then
media. No `text` part is created for the original content. This has several
consequences:

- Text that accompanies media is lost from the stored content. A user message
  with an image and a caption becomes `[media: ...]` plus the media part.
- Text that accompanies tool calls is lost from the stored content. An
  assistant message with text and a tool call keeps the tool call but drops the
  prose.
- Tool results are preserved but wrapped. The stored content becomes
  `[tool_result for call_1: <original>]`, and that wrapped string is what the
  model sees on later turns, because the tool result part only carries the ID
  when the message is reconstructed.
- An assistant tool-call message is reconstructed with both the derived text and
  the structured `ToolCalls`, duplicating the call in the prompt.
- Media MIME is always empty, because only the URI is copied into the part.
- Attachments, including non-media files, are not indexed or retrievable.

Plain text messages with no tool calls, tool result, or media store their
content unchanged.

### Session filtering

`Engine.Ingest` and `Engine.Compact` ignore sessions matching the hardcoded
`heartbeat` pattern. `Engine.Assemble` also returns `nil` for ignored sessions,
and the adapter turns that into an empty `AssembleResponse`.

The pattern compiler supports `*` (any run of non-colon characters) and `**`
(any run, including colons), anchors the pattern at both ends, and treats
everything else literally. Patterns are matched against the full session key, so
`heartbeat` does not match `heartbeat:foo`; `heartbeat*` would. Because
`context_manager_config` is ignored, users cannot add patterns.
`StatelessSessionPatterns` is compiled but never populated.

Heartbeat turns also set `NoHistory`, so they do not assemble or ingest in the
first place. The hardcoded ignore is a second layer.

## Startup bootstrap and reconciliation

`newSeahorseContextManager` runs synchronously before the gateway becomes ready:

1. Open the database, enable WAL, and run schema migrations.
2. If pruning is enabled and at least one threshold is active, run one prune
   pass and start the background prune loop. This happens before bootstrap so a
   bloated database can be reduced first.
3. Register `short_grep` and `short_expand` on every agent in the current
   registry.
4. For every session returned by `agent.Sessions.ListSessions()` on the
   **default agent**, read JSONL history and call `Engine.Bootstrap`.

Bootstrap errors are logged per session and do not abort startup. A session that
fails remains partially indexed until a later restart or until new messages are
ingested.

### Reconciliation algorithm

`Engine.Bootstrap` compares the JSONL message list with the rows already in
SQLite:

1. Ignore and stateless sessions return immediately.
2. Empty JSONL returns immediately. Any existing rows for that session are left
   untouched, so a session whose JSONL was emptied or deleted keeps stale data.
3. Get or create the conversation.
4. Read at most `len(jsonlMessages)` database messages, oldest first.
5. Run in-place repair for `reasoning_content`, `model_name`, and `created_at`.
6. If the counts match and every message matches (including reasoning, model,
   and timestamp after truncation to whole seconds), stop. This is the fast
   no-op path.
7. Otherwise compute the longest matching prefix from index 0.
8. If the mismatch is in the middle and the DB has messages after the anchor,
   delete all messages after the anchor and re-ingest the JSONL tail.
9. If the DB has no messages after the anchor and the JSONL has a longer tail,
   ingest only the tail.
10. If the first message does not match and the DB is non-empty, delete all
    messages and re-ingest the whole history.
11. If the DB is empty, ingest everything.

Matching compares role, content or parts, model name, reasoning content, and
timestamps. Timestamps are normalized to UTC seconds. A missing timestamp on
either side is treated as compatible. Token counts are intentionally ignored,
because re-estimation can produce a different value.

The repair step only runs when every overlapping message matches while ignoring
the repaired fields. It backfills:

- `reasoning_content` when the DB value is empty and JSONL has a value.
- `model_name` whenever JSONL has a non-empty value that differs from the DB,
  so a changed model name is overwritten rather than only backfilled.
- `created_at` when the DB value differs, but only if every overlapping message
  has a non-zero JSONL timestamp.

### Why bootstrap is needed

The JSONL history is authoritative, and SQLite can lag behind after a crash, a
manual edit, a downgrade, or a session that was written while Seahorse was
disabled. Bootstrapping at startup also prevents duplicate indexing: a session
that exists on disk but not in SQLite is brought in sync before any new turn can
append to it.

The cost is proportional to the number of sessions and the size of their JSONL
history. See
[Session Startup Cost and Automatic Pruning](session-startup-and-pruning.md)
for measurements and operational guidance.

### What bootstrap does not repair

- Extra database messages beyond the JSONL length. `GetMessages` is called with
  `limit = len(jsonlMessages)`, so a longer DB tail is invisible and survives.
- Summaries or summary context items left behind after a raw-message rebuild.
  Deleting messages removes message-linked context items and
  `summary_messages` rows, but not `summaries` rows or summary context items.
  A stale summary can remain in the assembled context and in `short_grep`
  results after the messages it summarizes were deleted or changed.
- Missing context items for messages that were inserted before an ingestion
  failure. Bootstrap's fast path only compares messages, not membership in
  `context_items`.
- Sessions that are not listed by the default agent's store, including
  pre-existing sessions of routed agents with a different workspace.

### Multi-agent behavior

The database is shared, but startup bootstrap iterates only the default agent's
session store. A routed agent with its own JSONL store:

- gets incremental ingestion for turns that happen after Seahorse is enabled;
- does not get its pre-existing history imported at startup;
- is not repaired if JSONL and SQLite diverge while the gateway is stopped.

The same applies after a workspace change or a new agent is added. Only a
restart with that agent as the default, or direct use of the engine bootstrap
API, would index its old history. Bootstrap is not exposed as a command.

## Context assembly

`seahorseContextManager.Assemble` receives `Budget = ContextWindow` and
`MaxTokens` from the agent. If the incoming budget is zero or negative, it uses
`100000` instead. It then computes:

```
effectiveBudget = Budget - MaxTokens
```

If the result is less than or equal to zero (a configuration where the output
reserve is at least the whole window), it logs a warning and uses
`Budget / 2`.

### Selection algorithm

1. Read all `context_items` for the conversation, ordered by ordinal. If there
   are none, return an empty result.
2. Resolve every item to a message or summary and a token count.
3. Split into an evictable prefix and a protected fresh tail:
   `tailStart = len(items) - 32`.
4. Sum the fresh-tail tokens.
5. If the fresh tail alone exceeds the budget, trim it (see below).
6. Compute `remainingBudget = budget - freshTailTokens`, clamped to zero.
7. If all evictable items fit in the remaining budget, include all of them.
8. Otherwise walk the evictable prefix from newest to oldest, keeping each item
   that fits and **stopping at the first item that does not fit**. Older items
   are not reconsidered even if a smaller one would fit.
9. Combine the kept evictable prefix with the fresh tail, preserving
   chronological order.
10. Return messages in `AssembleResult.Messages` and the XML for summaries in
    `AssembleResult.Summary`.

The fresh tail is measured in **context items**, not messages. A summary counts
as one item, so compacted sessions can protect a longer span of raw history than
an uncompacted one.

### Fresh-tail trimming

If the fresh tail exceeds the budget:

1. If there is a user message in the tail, and the slice from the latest user
   message to the end of the tail is itself over budget, keep that slice and
   return it even though it is over budget. This preserves the active turn.
2. Otherwise drop items from the oldest end until the tail fits.
3. Then advance the start until the remaining history begins at a
   provider-safe boundary: not a `tool` message and not an assistant message
   with tool calls.

The assembler logs `assemble: preserving active turn over budget` or
`assemble: trimmed fresh tail to safe boundary`.

The provider layer also sanitizes history in `sanitizeHistoryForProvider`:
leading tool messages, leading assistant tool-call messages, orphaned tool
results, and assistant tool-call messages with an invalid predecessor are
dropped. This is a second line of defense, but it can silently discard context
that the assembler tried to preserve.

### Summary XML

Summaries are not inserted into the message history. They are returned as a
single string and injected into the system prompt by `BuildMessagesFromPrompt`
under the `CONTEXT_SUMMARY:` prefix. The full prefix states that the summary is
approximate, may be incomplete or outdated, and that explicit instructions take
precedence.

Each summary is formatted as:

```xml
<summary id="sum_18f..." kind="leaf" depth="0" descendant_count="0" earliest_at="2026-10-24T10:00:00Z" latest_at="2026-10-24T10:05:00Z">
  <content>
    ...escaped summary text...
  </content>
</summary>
```

The `earliest_at` and `latest_at` attributes are omitted when the summary has no
source timestamps, which can happen for a condensed summary whose parents have
none.

A condensed summary also gets:

```xml
  <parents>
    <summary_ref id="sum_child1" />
    <summary_ref id="sum_child2" />
  </parents>
```

`<parents>` lists the summaries that were condensed into this one, despite the
name. Content is XML-escaped (`&`, `<`, `>`, `"`, `'`). IDs, kinds, depths, and
timestamps are generated internally. Summaries with empty content are skipped.

Only summaries currently present in `context_items` are included. Older
summaries that are no longer in the context are still searchable with
`short_grep`.

### Compression warning

If the selected summaries include a summary at depth 2 or deeper, or at least
two condensed summaries, the summary string also gets:

```
Your context has been heavily compressed through multi-level summarization.
- Do NOT assert specific facts (commands, SHAs, paths, timestamps) from summaries without expanding.
- When uncertain, use expand to recover original detail before making claims.
- Tool escalation: grep → describe → expand
```

The `describe` tool does not exist. The available tools are `short_grep` and
`short_expand`. This text is currently misleading.

## Compaction

Compaction rewrites `context_items`, creates summaries, and links them to their
sources. It never deletes raw messages or summary rows.

### Constants

| Constant | Value | Used by |
| --- | --- | --- |
| `OrdinalStep` | 100 | Context ordering |
| `FreshTailCount` | 32 items | Leaf and condensed selection, assembly |
| `LeafMinFanout` | 8 messages | Leaf trigger and minimum chunk |
| `CondensedMinFanout` | 4 summaries | Condensed fallback selection |
| `CondensedMinFanoutHard` | 2 | **Unused** |
| `LeafChunkTokens` | 20000 tokens | Leaf chunk cap and condensed run cap |
| `LeafTargetTokens` | 1200 tokens | Leaf summary target |
| `CondensedTargetTokens` | 2000 tokens | Condensed target and minimum run size |
| `ContextThreshold` | 0.75 | Only when `CompactInput.Budget` is nil, plus the unused `NeedsCompaction` |
| `MaxCompactIterations` | 20 | `CompactUntilUnder` cap |
| `MaxExpandTokens` | 4000 | **Unused** |
| `maxTruncatedSummaryBytes` | 2048 bytes | Deterministic fallback |

### Trigger paths

| Path | Reason | Caller | Behavior |
| --- | --- | --- | --- |
| Post-turn | `summarize` | `pipeline_finalize.go`, `pipeline_execute.go` | `Engine.Compact`, leaf sync, condensed async only if over budget |
| Pre-LLM threshold | `threshold` | `pipeline_compact.go` via `CompactBeforeLLM` | `Engine.Compact`, blocking; returns `ErrNothingToCompact` if no summary |
| Proactive budget | `proactive_budget` | `pipeline_setup.go` | `Engine.Compact` before the first LLM call if the hard budget is exceeded |
| Overflow retry | `llm_retry` | `pipeline_llm.go` | `CompactUntilUnder(ContextWindow)`, up to 20 iterations |
| Manual | `manual` | `/compact` command | `CompactNow`, leaf and condensed synchronously |

The agent passes `Budget = ContextWindow` on every automatic path. Therefore the
internal 75 percent threshold is not the normal trigger: the agent's own
`summarize_token_percent` threshold (default 75 percent of the usable window)
decides when to call `CompactBeforeLLM`, and `Engine.Compact` always attempts a
leaf pass when called.

`CompactBeforeLLM` runs before every LLM call, including inside a tool loop, and
chains up to three passes per call. It estimates the prompt from `exec.messages`
plus tool definitions and compares it with
`(ContextWindow - MaxTokens) * summarize_token_percent / 100`. If the prompt is
below the threshold, it does nothing.

The post-turn call happens after the final assistant message is persisted. It is
best effort: the return value is discarded, so errors are not logged at this
call site and the turn continues.

### `Engine.Compact`

1. Leaf compaction runs synchronously, regardless of the threshold, subject to
   the leaf minimums below.
2. `tokensBefore` is read **after** the leaf pass.
3. Budget resolution:
   - `input.Budget != nil`: use it. A zero value logs an error but is still used.
   - `input.Budget == nil`: `int(tokensBefore * 0.75)`.
4. If `Force` is true, or `tokensBefore > budget` and `budget > 0`, launch
   condensed compaction in a goroutine. Deduplicate per conversation with the
   `condensing` map.
5. Read `tokensAfter` and set `TokensSaved = tokensBefore - tokensAfter` if
   positive.

Because `tokensBefore` is measured after the leaf pass, `TokensSaved` does not
include the synchronous leaf savings. It only reports condensed savings that
finished before the measurement.

The asynchronous condensed loop runs with the engine's `shutdownCtx`, not the
request context. It can outlive the turn. `Close` cancels it but does not wait
for the goroutine, so a database close can race with an in-flight compaction.

### Leaf compaction

`compactLeaf`:

1. Reads all context items.
2. Counts message items and sums their tokens.
3. Returns immediately if `messageCount < 8` **and** `messageTokens < 20000`.
   Therefore it triggers when either the count reaches 8 or the tokens reach
   20000.
4. Computes `tailStart = len(items) - 32`. In forced mode (used only by
   `CompactUntilUnder`), `tailStart = len(items)`.
5. Scans from ordinal 0 up to `tailStart`, accumulating contiguous message
   items:
   - A message starts or extends the chunk.
   - Accumulation stops once the chunk reaches 20000 tokens.
   - A non-message item breaks the chunk. If the accumulated chunk already has
     at least 8 messages, the scan stops; otherwise the chunk is reset.
6. Requires the final chunk to contain at least 8 **context items** that are
   messages. Because non-message items break and reset, these are contiguous
   messages.
7. Loads each message in the chunk.
8. Walks backward from the chunk start and collects up to two prior summaries
   as `<previous_context>`, regardless of adjacency.
9. Generates the summary with the three-level escalation described below.
10. Creates a `leaf` summary at depth 0 with token count, earliest and latest
    source timestamps, and source message token total.
11. Links the summary to its source messages in `summary_messages`.
12. Replaces the ordinal range with one summary context item at the midpoint.

Important edge cases:

- A young session with fewer than 32 context items has an empty evictable
  region, so nothing is compacted even if it has thousands of tokens.
- A single message larger than 20000 tokens can stop the scan before 8 messages
  are collected, so the leaf pass returns `nil`. The same applies when the first
  8 messages are separated by summaries or when messages are inside the fresh
  tail.
- The chunk can exceed 20000 tokens by the size of the last message, because
  the check happens after adding it.
- The fresh tail is protected during normal and manual compaction. Only
  `CompactUntilUnder` bypasses it.
- The leaf pass compacts at most one chunk per call. `CompactBeforeLLM` can
  chain up to three calls; the post-turn path calls once. Large histories are
  compacted over several turns.

### Summarizer model and retries

The summarizer uses the **default agent's** provider and resolved candidate
model, captured when the context manager is built. Routed agents' sessions are
summarized with that model, not with their own model. After a hot reload the
captured provider and model are stale.

The adapter sends a single user message containing the prompt, with no tools,
`MaxTokens` and `Temperature` from the compaction level, and
`prompt_cache_key: "seahorse"`. Transient failures (rate limits, 5xx, timeouts,
network errors) are retried with the configured `max_llm_retries` and
`llm_retry_backoff_secs`, and the retry stops when the turn is canceled. Other
errors abort the compaction pass.

### Leaf summary generation

With `complete == nil`, Seahorse uses deterministic truncation immediately. In
the agent adapter `complete` is always set.

Input is built by `formatMessagesForSummary`: a timestamp line, then the
message content. If content is empty and parts exist, it uses the same readable
part rendering as ingestion.

`inputTokens` is the sum of stored message token counts.
`targetTokens = min(1200, inputTokens * 0.35)`.

Level 1, normal prompt:

- `MaxTokens = 2400`
- `Temperature = 0.3`
- If the response is empty, retry once with `Temperature = 0`.
- Accept only if the estimated response size is at most `targetTokens`.

Level 2, aggressive prompt:

- `aggressiveTarget = min(640, inputTokens * 0.20)`
- `MaxTokens = aggressiveTarget * 2`
- `Temperature = 0.3`, retry at 0 if empty.
- Accept only if the estimated size is at most `aggressiveTarget`.

Level 3, deterministic fallback:

- Concatenate message contents with newlines.
- Truncate to 2048 bytes on a valid UTF-8 boundary.
- Append `\n[Truncated from N messages]`.

The prompts require plain text, no headings, file operation tracking with
`Files: none` when empty, and an `Expand for details about: ...` trailer. A
manual `/compact` focus is appended inside a `<focus>` block with an instruction
to prioritize it. Automatic prompts are unchanged.

If the LLM call returns an error, the whole leaf compaction returns the error.
There is no fallback for transport or provider errors, only for empty or
oversized responses.

### Condensed compaction

`compactCondensed`:

1. Reads distinct summary depths currently in context, ascending.
2. For each depth, tries `selectOldestChunkAtDepth`.
3. If no candidates, falls back to `selectShallowestCondensationCandidate`.
4. If still none, returns `nil`.
5. Generates the condensed summary with three-level escalation.
6. Computes merged metadata:
   - `depth = max(parent depth) + 1`
   - `descendant_count = sum(parent descendant count + 1)`
   - `descendant_token_count = sum(parent token count + parent descendant tokens)`
   - `source_message_tokens = sum(parent source message tokens)`
   - earliest and latest timestamps are merged
7. Creates a `condensed` summary and parent edges in `summary_parents`.
8. Replaces the candidate items in `context_items`:
   - If the candidates are consecutive, replace the ordinal range with one
     summary at the midpoint.
   - If non-candidate items sit between candidates, delete each candidate by
     summary ID and insert the new summary at the midpoint of the candidate
     ordinals. If that midpoint is occupied by a non-candidate item, the context
     is resequenced and the new summary is appended at the end, which breaks
     chronological order.

#### `selectOldestChunkAtDepth`

This is the primary selector:

- Starts at ordinal 0 and scans up to the fresh-tail boundary.
- The **first** item must be a summary. Any non-summary item breaks the scan, so
  the context must begin with summaries.
- All collected summaries must have the same depth.
- A different depth breaks the scan.
- The run stops before exceeding 20000 summary tokens.
- The accumulated token count must be at least 2000; otherwise no candidates.

A single large summary can satisfy the 2000-token minimum. A context that starts
with a message, or with a leaf summary followed by a message, fails this
selector.

#### `selectShallowestCondensationCandidate`

The fallback:

- Groups summaries by depth, ignoring non-summary items and adjacency.
- Only considers items before the fresh-tail boundary.
- Sorts depths ascending.
- Picks the shallowest depth with at least 4 summaries.
- Takes the first 4 in context order.
- Never uses the hard fanout of 2; the `forced` parameter is always false.

This is why condensed compaction still works after a normal leaf summary: the
fallback groups non-consecutive leaf summaries at depth 0.

#### Condensed summary generation

`inputTokens` is the sum of candidate summary token counts.
`targetTokens = min(2000, inputTokens * 0.35)`.

Level 1 uses the normal condensation prompt with `MaxTokens = 4000` and
`Temperature = 0.3`, retrying at 0 if empty. Unlike the leaf path, it accepts
**any non-empty response**, without checking the target size.

Level 2 uses the same prompt with
`aggressiveTarget = min(640, inputTokens * 0.20)` and `MaxTokens` twice that.
It also accepts any non-empty response.

Level 3 concatenates the summaries, truncates to 2048 bytes on a UTF-8 boundary,
and appends `\n[Condensed from N summaries]`.

### `runCondensedLoop`

The loop:

1. Returns if the context is canceled.
2. Reads the current context token count.
3. Tries `compactCondensed`.
4. Returns if no candidate was found.
5. Records one created summary.
6. Returns if tokens did not decrease.
7. Returns if tokens did not improve compared with the previous iteration.
8. Repeats.

Despite the comment, the loop has **no budget check**. It keeps condensing until
there is no candidate or no progress. Each iteration reduces the number of
summaries, so it terminates, but it can create more summaries than needed.

The loop is deduplicated per conversation through `CompactionEngine.condensing`.
Only condensed loops share this map. Leaf passes are not deduplicated.

### `CompactNow` (manual `/compact`)

`CompactNow`:

1. Reads the token count before any work.
2. Runs one leaf pass synchronously.
3. If the condensed dedup slot is free, runs the condensed loop synchronously
   and holds the slot.
4. Computes `TokensSaved` across both phases.
5. Returns counts of created leaf and condensed summaries.

If an asynchronous condensed loop already holds the slot, the manual condensed
phase is skipped. The command can then report "Nothing to compact yet" when the
leaf pass also created nothing, even though a condensed loop is running.

The agent adapter treats zero created summaries as `ErrNothingToCompact`, which
the `/compact` command renders as "Nothing to compact yet." A successful manual
compaction replies "Context compacted." and sends a channel notice such as
"Compacted context: summarized 2 blocks of older messages and merged 3
summaries into a higher-level summary to free up space."

### `CompactUntilUnder` (overflow retry)

`CompactUntilUnder` is used when the provider reports a context-window error.
It loops up to 20 times:

1. Read current context tokens. If at or below the budget, return.
2. Try a forced leaf pass, which ignores the fresh tail.
3. If no leaf summary, try condensed compaction.
4. If neither made progress, stop.

It never sets `TokensSaved`. The budget passed by the adapter is
`ContextWindow`, not `ContextWindow - MaxTokens`, so the prompt can still be too
large for the output reserve. The agent then trims history with
`trimHistoryToFitContextWindow` around the compacted context.

### What compaction preserves and removes

Preserved:

- All rows in `messages`.
- All rows in `summaries`, including summaries no longer in the context.
- Parent and source links for summaries that were created.
- JSONL session history, unchanged.

Removed or rewritten:

- `context_items` entries replaced by a summary.
- `summary_messages` rows only when the underlying message is deleted by
  bootstrap, not by compaction.

Not updated:

- `conversations.updated_at` is never maintained after insert.
- `summaries.model` is never populated; the summarizer model name is not stored.
- The JSONL summary field. Seahorse summaries exist only in SQLite.

### Clearing a session

`/clear` calls `ContextManager.Clear`, which:

1. Calls `Engine.ClearSession`. If the session has no conversation row, this is
   a no-op.
2. `ClearConversation` deletes context items, `summary_messages` rows,
   `summary_parents` edges in both directions, summaries, message parts, and
   messages, all in one transaction. FTS rows are removed by the delete
   triggers.
3. The `conversations` row is kept, so the next message reuses the same
   conversation ID.
4. The adapter then clears the owner's JSONL history and summary and saves the
   session.

If the Seahorse clear fails, the JSONL clear is skipped and the command reports
the error. Pruning is different: it calls `DeleteSession`, which also removes
the conversation row.

### Compaction events and feedback

When the synchronous compaction result reports at least one summary, the
adapter:

1. Emits `agent.session.summarize` with
   `SummarizedMessages = leafSummaries + condensedSummaries`. The field name is
   historical; it counts summaries, not messages.
2. Sends a context-feedback notice to the session's channel.

For the threshold reason only, a result with zero summaries returns
`ErrNothingToCompact`. Other automatic reasons ignore that condition.

The event and notice are produced before an asynchronous condensed loop
finishes, so condensed summaries created in the background are not counted and
do not produce a notice. Only manual compaction, which runs the loop
synchronously, reports both phases accurately.

## Retrieval tools

Both tools are registered on every agent at startup. They are subject to the
per-agent tool allowlist in `AGENT.md` and to turn-profile tool filtering, so an
agent can be configured not to see them. They are not available after a hot
reload, because the new agent registry is built without them and the shared-tool
registration does not include them.

### `short_grep`

Schema:

| Parameter | Type | Default | Behavior |
| --- | --- | --- | --- |
| `pattern` | string, required | none | FTS5 query, or LIKE if it contains `%` |
| `scope` | `both`, `summary`, `message` | `both` | Which tables to search |
| `role` | `user`, `assistant` | all | Applied to messages only |
| `all_conversations` | boolean | false | **No effect** in the current implementation |
| `since` | RFC3339 string | none | Lower time bound |
| `before` | RFC3339 string | none | Upper time bound |
| `last` | `6h`, `7d`, `2w`, `1m` | none | Relative lower bound, overrides `since` |
| `limit` | integer | 20 | Per-scope limit |

`last` parsing accepts an integer followed by one of `h`, `d`, `w`, `m`
(lowercase, months are 30 days). It does not accept spaces, decimals, uppercase
units, or years.

An unrecognized `scope` value is not rejected. It matches neither branch, so the
tool returns `success: true`, empty result arrays, and the "no matches" hint.

Search mode:

- If the pattern contains `%` anywhere, both summaries and messages use LIKE.
- Otherwise summaries use FTS5 only, and messages try FTS5 first and fall back
  to LIKE when FTS5 returns an error or zero results.

Only `messages.content` is indexed and searched. `reasoning_content` is stored
and replayed during assembly, but it is not searchable through `short_grep`, is
not included in `short_expand` output, and is not part of the text fed to the
summarizer.

LIMIT semantics:

- `limit == 0` becomes 20.
- A positive limit is applied per scope, so a `both` search can return up to
  2 × limit rows.
- A negative limit is not validated. The store only adds `LIMIT` when the value
  is positive, so a negative limit returns all matching rows.

FTS5 query handling (`SanitizeFTS5Query`):

- Whitespace-separated tokens are each wrapped in double quotes and joined with
  spaces, which FTS5 treats as an implicit AND.
- Quoted phrases are preserved as phrases.
- Internal double quotes are stripped.
- `AND`, `OR`, `NOT`, `NEAR`, `*`, `+`, `-`, `^`, `:`, and parentheses are
  treated as literal text, not operators.
- With the trigram tokenizer, queries shorter than 3 characters may not match
  through FTS5. Messages still get the LIKE fallback; summaries do not.
- `%` cannot be searched literally, because it switches the whole search to LIKE
  and is then used as a wildcard.

LIKE handling:

- The pattern is wrapped as `%<pattern>%`.
- `_` remains a single-character wildcard.
- ASCII matching is case-insensitive; non-ASCII behavior follows SQLite.

Ordering and ranking:

- FTS5 results are ordered by `bm25`, ascending. bm25 returns negative values;
  more negative means a better match.
- LIKE results are ordered by `created_at DESC`, newest first.
- There is no deterministic tiebreaker for equal ranks or identical timestamps.

Time filters:

- Messages use `messages.created_at`.
- Summaries use `summaries.created_at`, which is when the summary was produced,
  not the earliest or latest time of the content it covers.
- Timestamps are stored as UTC strings. The filter formats the input without
  converting to UTC, so a `since` or `before` with a non-UTC offset is compared
  as local wall time and can be off by the offset.

Output:

```json
{
  "success": true,
  "summaries": [
    {
      "id": "sum_18f...",
      "content": "full summary text",
      "depth": 0,
      "kind": "leaf",
      "conversationId": 1,
      "rank": -1.2
    }
  ],
  "messages": [
    {
      "id": "42",
      "snippet": "full message content",
      "role": "user",
      "conversationId": 1,
      "rank": -0.8
    }
  ],
  "hint": "No matches. Try: %keyword% for fuzzy search, or all_conversations: true"
}
```

Notes:

- `totalSummaries` and `totalMessages` are computed internally but not included
  in the tool output.
- Results do not include timestamps, model names, or session keys.
- `snippet` for messages is the full stored content, not an excerpt.
- `content` for summaries is the full summary.
- There is no per-result or total token cap. A limit of 20 full messages can be
  very large.
- The hint suggests `all_conversations`, which has no effect.

#### Conversation scoping defect

`GrepInput` has no conversation ID, and `RetrievalEngine.Grep` never sets
`SearchInput.ConversationID`. The store therefore searches **all conversations**
for every call. `all_conversations` is a no-op, and the documented default
"current conversation only" is not implemented.

Impact:

- A model in one session can retrieve content from every other session,
  including other users, channels, and agents that share the database.
- `conversationId` is returned, but the model has no session key to interpret
  it.
- The privacy boundary is the process, not the conversation.

### `short_expand`

Schema: `message_ids`, an array of strings. The implementation also accepts
numbers, because the parser handles both `string` and `float64`.

Behavior:

1. Requires a non-empty array. Otherwise returns an error result.
2. Parses each ID. Invalid strings return an error; non-string, non-number
   values are silently ignored.
3. Fetches each message by global ID. Missing IDs are silently skipped.
4. Preserves input order and duplicates.
5. Sums stored `token_count` for found messages.
6. Returns `success`, `tokenCount`, and `messages`.

Each message includes:

- `id` as a string
- `role`
- `content`, which is the stored derived content
- `conversationId`
- `parts`, filtered by type:
  - `text`: `text`
  - `tool_use`: `name`, `arguments`, `toolCallId`
  - `tool_result`: `toolCallId` only, content omitted from the part
  - `media`: `mediaUri`, `mimeType`
  - unknown types: only `type`

Important behavior:

- The tool description says tool result content is not returned. In practice,
  the message-level `content` for a tool result message is the rendered
  `[tool_result for ID: text]` string, so the text is still exposed.
- `ReasoningContent` is stored but not included in the tool output.
- There is no cap on the number of IDs or on returned tokens.
- Any message ID in the database can be fetched, regardless of conversation.
  This includes messages from other sessions and agents.
- Media MIME is empty for messages ingested through the agent adapter, because
  the adapter does not copy MIME into the media part.
- Media URIs can reveal filesystem paths or internal references.
- Summary IDs returned by `short_grep` cannot be expanded. `short_expand` only
  accepts message IDs, and there is no tool that walks `summary_messages` or
  `summary_parents`.
- A missing or malformed ID does not fail the whole request unless the malformed
  value is a string that fails numeric parsing.

## Configuration reference

Seahorse has fewer knobs than its constants suggest. Most behavior is compiled
in.

| Setting | Default | Effect |
| --- | --- | --- |
| `agents.defaults.context_manager` | `""` (legacy) | Set to `seahorse` to enable |
| `PICOCLAW_AGENTS_DEFAULTS_CONTEXT_MANAGER` | unset | Environment override for the above |
| `agents.defaults.context_manager_config` | unset | Accepted, ignored by Seahorse |
| `agents.defaults.context_window` | `max_tokens * 4` | Assembly budget and compaction trigger base |
| `agents.defaults.max_tokens` | `32768` | Output reserve subtracted from the assembly budget |
| `agents.defaults.summarize_token_percent` | `75` | Agent-level threshold for pre-LLM compaction |
| `agents.defaults.summarize_message_threshold` | `20` | Deprecated and ignored |
| `session.prune.enabled` | `false` | Master switch for pruning |
| `session.prune.max_age_days` | `0` | Age threshold, disabled |
| `session.prune.max_sessions` | `0` | Session-count cap, disabled |
| `session.prune.max_db_size_mb` | `0` | Database-size guard, disabled |
| `session.prune.check_interval_minutes` | `0` (effective 60) | Background prune interval |
| `session.prune.vacuum` | `true` when omitted | VACUUM after deletions when worthwhile |

Environment overrides for pruning use the `PICOCLAW_SESSION_PRUNE_` prefix.

The following `seahorse.Config` fields are implemented but not wired from agent
configuration:

- `DBPath`: fixed to `<default-workspace>/sessions/seahorse.db`.
- `IgnoreSessionPatterns`: only `heartbeat` is added.
- `StatelessSessionPatterns`: never populated.

The following constants are not reachable from configuration at all:
`FreshTailCount`, `LeafMinFanout`, `CondensedMinFanout`, `LeafChunkTokens`,
`LeafTargetTokens`, `CondensedTargetTokens`, `MaxCompactIterations`,
`OrdinalStep`.

## Pruning and disk management

Pruning is implemented in `pkg/session/pruner` and driven by the Seahorse
context manager. It is the only feature that deletes Seahorse data and JSONL
files. Because the prune loop is started only by
`newSeahorseContextManager`, `session.prune` has no effect when the context
manager is `legacy`. The configuration is still accepted and validated, which
can make an ineffective setup look active.

### Semantics

- Disabled by default. `enabled: true` plus at least one non-zero threshold is
  required; enabling with all thresholds zero does nothing.
- A session is selected when its last activity is older than `max_age_days`, or
  when it is not among the `max_sessions` most recently active sessions.
- `max_db_size_mb` is a separate guard: only sessions present in the database
  are candidates, deleted oldest-first, until the file fits or no progress is
  made. It is bounded to 32 delete/reclaim cycles.
- `max_sessions` counts every session, including cron jobs, background tasks,
  and subagent sessions, not only interactive chats. On a busy device a small
  cap can evict real chats in favor of recent automated sessions.
- Last activity is the newest of the newest Seahorse message time and the owning
  session store's last write time, falling back to the oldest Seahorse message.
- Timestamps before 2024-01-01 are treated as implausible and ignored. Age-based
  pruning is skipped entirely while the system clock reads earlier than that.
- Undated sessions are never selected. They still count toward `max_sessions`.
- The most recently active session is never deleted, whatever the thresholds
  say. This is a hard floor of one session.
- Every agent's session store is included. If any store cannot delete sessions,
  pruning is skipped entirely and a warning is logged.
- A session in JSONL but not yet in SQLite is still a candidate, using the store
  timestamp. It has no database space to reclaim.
- `Assemble` and `Compact` create a conversation row even when the session has no
  messages. Such rows are undated, are never selected by pruning, and still
  count toward `max_sessions`, so abandoned empty conversations can accumulate.
- Deletion removes the database conversation row and the JSONL session from every
  store that lists it. A failure in one side is logged; the pass continues if
  the other side succeeded.

### Startup and background

If pruning is active, one pass runs synchronously during
`newSeahorseContextManager`, before tool registration and before the bootstrap.
A background loop then runs every `check_interval_minutes` (default 60) until
`Close`. The loop stop is synchronous: `Close` waits for a running pass to
finish before closing the database.

### Reclaiming space

Deleting rows does not shrink the SQLite file. The pruner:

- Runs `VACUUM` when vacuuming is enabled and either the database-size guard
  forced it or the reclaimable space is at least 1 MiB and at least one tenth of
  the file.
- Otherwise runs `PRAGMA wal_checkpoint(TRUNCATE)` to fold and truncate the WAL,
  which is cheap and does not block writers for long.

`VACUUM` rewrites the whole database, needs free disk space roughly equal to the
database size, and blocks writers. On slow storage it can exceed the 5 second
busy timeout and cause live ingestion to fail. The first pass is synchronous and
adds to startup time.

`DBFileSize` reports the main database plus the `-wal` file. It ignores `-shm`.

When vacuuming is disabled, the size guard can only shrink the file by
truncating the WAL. If that does not reduce `DBFileSize`, the guard stops after
its first batch instead of deleting every session, which is intentional: it
never deletes data it cannot use to make progress.

### Interaction with compaction

Compaction never deletes messages or summaries, so the database keeps growing
with conversation length even when the prompt stays small. Pruning is the only
way to remove old sessions and reclaim disk. A database that is large because of
a few very long sessions will stay large until those sessions age out, exceed
the session cap, or hit the size guard.

## Operations

### Confirming Seahorse is active

- The gateway log shows `seahorse` ingest and compaction messages.
- `sessions/seahorse.db` exists.
- The tool list includes `short_grep` and `short_expand`.
- `/context` reflects compacted history, not the raw JSONL length.

If `context_manager` is `seahorse` but the log shows a fallback warning, the
factory failed. Common causes: the database cannot be opened or migrated, FTS5
or trigram is unavailable, or the build excludes Seahorse.

### Inspecting the database

Stop the gateway before copying or modifying the database, or at least
checkpoint the WAL first.

```sh
sqlite3 ~/.picoclaw/workspace/sessions/seahorse.db

.headers on
.mode column

SELECT COUNT(*) AS conversations FROM conversations;
SELECT COUNT(*) AS messages FROM messages;
SELECT COUNT(*) AS summaries FROM summaries;

SELECT c.session_key,
       COUNT(DISTINCT m.message_id) AS messages,
       COUNT(DISTINCT s.summary_id) AS summaries
FROM conversations c
LEFT JOIN messages m ON m.conversation_id = c.conversation_id
LEFT JOIN summaries s ON s.conversation_id = c.conversation_id
GROUP BY c.conversation_id
ORDER BY messages DESC
LIMIT 20;

SELECT ci.conversation_id,
       SUM(ci.token_count) AS context_tokens,
       COUNT(*) AS items
FROM context_items ci
GROUP BY ci.conversation_id
ORDER BY context_tokens DESC
LIMIT 20;

PRAGMA integrity_check;
PRAGMA journal_mode;
PRAGMA freelist_count;
```

`SessionStatus.TotalTokens` is the sum of `context_items.token_count`, not the
total tokens in `messages`. A session can have a very large `messages` table and
a small context size because compaction folded history into summaries.

### Backups

With the gateway stopped, copy `seahorse.db`, and `seahorse.db-wal` if present.
The WAL contains committed transactions that have not been checkpointed. A copy
of only the main file can miss recent data. Restoring an older database is safe
but loses summaries and retrieval data created after the backup; the next
startup bootstrap reconciles raw messages for the default agent.

### Rebuilding the index

With the gateway stopped, delete `seahorse.db`, `seahorse.db-wal`, and
`seahorse.db-shm`. The next startup creates a new database and bootstraps the
default agent's sessions from JSONL. Summaries, context compaction, and
retrieval history are lost and must be regenerated by normal use.

This is also the recovery path for a corrupted database, since a failed open or
migration otherwise falls back to legacy and leaves the file in place.

### Troubleshooting

| Symptom | Likely cause | Action |
| --- | --- | --- |
| `context_manager: seahorse` but memory behaves like legacy | Factory failed and fell back | Check the gateway log for "Failed to create context manager" |
| Gateway takes minutes to become ready | Synchronous bootstrap of many large sessions | Enable pruning, reduce session count or history size |
| `short_grep` returns nothing for a short query | Trigram FTS needs at least 3 characters; summaries have no LIKE fallback | Use a longer pattern or `%..%` |
| `short_grep` returns content from other chats | Conversation scoping is not implemented | Treat the database as process-wide; restrict access |
| `/compact` says "Nothing to compact yet" during automatic compaction | Async condensed loop holds the dedup slot | Retry after the loop finishes |
| No new summaries despite a long chat | Fewer than 8 contiguous messages outside the 32-item fresh tail, or one huge message blocking the scan | Continue the conversation; compaction will catch up |
| Database grows despite compaction | Compaction never deletes raw rows | Enable pruning or accept the disk cost |
| Ingest errors during a long VACUUM | Busy timeout exceeded | Increase the interval, vacuum less often, or avoid the size guard on slow storage |

## Performance characteristics

### Startup

Bootstrap reads each known session's full JSONL history and reconciles it with
SQLite. Message parts are loaded in batched queries, but the reconciliation is
still linear in total history size. On slow storage, tens of megabytes of JSONL
and a large FTS index can take minutes. Pruning runs before bootstrap to reduce
the work when it has something to delete.

### Per turn

- Ingest writes one message and its parts and appends the context item in a
  single transaction per chunk (one message for the adapter path, up to
  `IngestBatchChunkSize` for bootstrap), so the hot path costs one commit per
  message instead of two.
- Assembly loads all context items and resolves each one with a separate query.
  Messages also load their parts with a per-message query. This is an N+1 query
  pattern, so prompt assembly cost grows with context size.
- Leaf compaction loads up to 20000 tokens of messages, one query per message,
  plus a small number of summary lookups.
- Condensed selection rescans context items for each depth and fetches summaries
  one by one. It is bounded by the number of summaries in the evictable region.
- Search runs a count query and a data query per scope. FTS5 with trigram is
  fast for matching but the index is large and update-heavy.

### Memory

- The SQLite connection pool is capped at two connections, bounding page cache
  growth.
- `temp_store = FILE` spills sorts and temporary tables to disk instead of the
  Go heap. On a RAM-backed temporary directory this shifts memory to the kernel,
  it does not eliminate it.
- FTS5 trigram indexes are stored on disk and consume significant space relative
  to the indexed text.
- The engine holds no global message cache. Each assembly reads from SQLite.

### Practical limits

| Resource | Bound |
| --- | --- |
| Message size | No explicit limit; whole messages are stored and summarized |
| Leaf chunk | About 20000 tokens, plus the last message |
| Fresh tail | 32 context items |
| Leaf target | 1200 tokens, aggressive 640 |
| Condensed target | 2000 tokens |
| Fallback summary | 2048 bytes |
| Compact iterations | 20 per `CompactUntilUnder` |
| Search results | 20 per scope by default, negative limit unbounded |
| Expand results | Unbounded |
| Database connections | 2 |
| Busy timeout | 5 seconds |

## Security and privacy

Seahorse stores full message content, reasoning content, tool results, summaries,
media URIs, and model names in an unencrypted SQLite file. It is part of the same
trust domain as the JSONL session files.

Key points:

- **Cross-conversation retrieval.** `short_grep` searches every conversation and
  `short_expand` fetches any message ID. Any agent using the shared manager can
  read any indexed session, including sessions of other agents, channels, and
  users sharing the process. This is the most important privacy property to
  understand before enabling Seahorse on a multi-user deployment.
- **No encryption at rest.** The database is protected only by filesystem
  permissions. The directory is created with `0755`; the file mode follows the
  process umask, which is commonly `0644`. Other local users may be able to read
  it. Restrict the workspace permissions or use full-disk encryption.
- **Prompt injection through stored content.** Tool output, web pages, and user
  text are summarized and later injected into the system prompt as
  `CONTEXT_SUMMARY`. XML escaping prevents tag injection, not instruction
  injection. The prefix tells the model to defer to explicit instructions, but a
  summary can still bias behavior.
- **Media paths.** `short_expand` returns `mediaUri`, and the FTS content
  includes `[media: <uri>]`. If URIs contain filesystem paths or signed URLs,
  they become visible to the model and to anyone reading the database.
- **Secret filtering.** Tool results sent to the model pass through the
  configured sensitive-data filter before ingestion, but user messages,
  reasoning, and assistant text are stored as provided. Do not assume the
  database is redacted.
- **Backups.** A database copy contains the same data as the JSONL history.
  Treat it with the same care, and remember the `-wal` file.
- **Pruning is destructive.** It deletes both the database rows and the JSONL
  files for selected sessions. Test thresholds on a copy before enabling on a
  device with important history.

## Hot reload behavior

`ReloadProviderAndConfig` rebuilds the agent registry and swaps the provider and
config, but it does not recreate `al.contextManager`. The existing Seahorse
manager keeps:

- the original database path and SQLite handles;
- the original default agent's session store used for startup bootstrap
  (`Clear` resolves the owning agent dynamically and may use a newer store);
- the original provider object and model captured by `providerToCompleteFn`;
- the original pruning configuration and loop;
- the original session shard mutexes and dedup state.

The new registry does not get `short_grep` and `short_expand`, because they were
registered directly on the previous registry's agents and are not part of
`registerSharedTools`.

Practical consequences after a reload:

- Retrieval tools disappear from the model's tool list.
- Automatic compaction may fail if the old provider was closed or its
  credentials changed.
- Changing `context_manager`, `context_manager_config`, workspace, pruning
  settings, or the summarizer model has no effect until a full gateway restart.
- Newly added agents use the shared manager for assembly and ingestion, but
  their startup history is not bootstrapped.

## Known gaps

The following behaviors are surprising, undocumented elsewhere, or contradict
comments and tool descriptions. They are listed with their impact so operators
can decide whether they matter for a deployment.

### Retrieval and isolation

1. **`short_grep` is not scoped to the current conversation.**
   `GrepInput` has no conversation field and `RetrievalEngine.Grep` never sets
   `SearchInput.ConversationID`. `all_conversations` is a no-op. Impact:
   cross-session and cross-agent content disclosure.
2. **`short_expand` accepts any message ID.**
   There is no conversation check and no ownership check. Summary IDs from
   `short_grep` cannot be expanded at all. Impact: the same disclosure as above,
   plus unbounded output and no way to drill into a summary.
3. **Tool descriptions overstate capabilities.**
   `short_grep` documents `AND`/`OR`/`NOT` and current-conversation defaults that
   the implementation does not provide. It also documents `totalSummaries` and
   `totalMessages`, which are omitted from the output. The heavy-compression
   warning references a `describe` tool that does not exist.
4. **Search time filters use summary creation time.**
   `since` and `before` filter `summaries.created_at`, not the time range the
   summary covers. A summary of last week's messages created today is treated as
   today's content.
5. **Non-UTC time filters are off.**
   The filter formats the input in its own location but compares it with UTC
   database strings. Use `Z` timestamps.
6. **Short queries miss summaries.**
   The trigram tokenizer needs at least 3 characters. Messages fall back to
   LIKE; summaries do not, so a 2-character pattern can return messages but no
   summaries.
7. **Negative limits are unbounded.**
   `limit: -1` skips the SQL `LIMIT` clause and returns every match.

### Message fidelity

8. **Tool result text is wrapped.**
   Stored content for a tool result is `[tool_result for ID: text]`, and that
   wrapper is what later turns see. The raw text is not restored during
   assembly.
9. **Text next to media or tool calls is lost.**
   Part-based ingestion discards the original `Content`. Captions, assistant
   prose before a tool call, and similar text disappear from the indexed
   history.
10. **Attachments are dropped.**
    `providers.Message.Attachments` is not converted. Non-media attachments are
    neither indexed nor retrievable.
11. **Media MIME is empty.**
    The adapter copies only the URI. `short_expand` therefore reports an empty
    `mimeType` for agent-ingested messages.
12. **Assistant tool calls are duplicated.**
    The stored content renders the call, and assembly also reconstructs
    structured `ToolCalls`, so the model sees both forms.

### Ingestion and durability

13. **Multi-chunk ingestion is not atomic across chunks.**
    Chunks are atomic individually, but a failure in a later chunk leaves the
    earlier chunks committed. The lost chunk stays in JSONL and is re-ingested
    by the next bootstrap.
14. **Ingest failures are only logged.**
    The message remains in JSONL but is absent from SQLite and therefore from
    the next assembled prompt until a restart bootstrap succeeds.
15. **No deduplication.**
    Ingest has no idempotency key. Calling it twice with the same messages
    creates duplicates. The agent calls it once per persisted message, but a
    retry or a manual import would duplicate.
16. **Bootstrap cannot see extra database rows.**
    `GetMessages` is limited to the JSONL length, so a longer database tail is
    invisible and survives reconciliation.
17. **Empty JSONL leaves stale rows.**
    Bootstrap returns immediately for an empty history, so a session whose JSONL
    was cleared or deleted keeps its old messages and summaries in SQLite.
18. **Rebuilds leave orphan summaries.**
    Message deletion removes `summary_messages` links and message context items,
    but not summary rows or summary context items. Stale summaries can remain in
    the context and in search results after the source messages changed or were
    deleted.

### Compaction

19. **Young sessions do not compact.**
    The fresh tail is 32 context items. Until a session has more than 32 items,
    the evictable region is empty and leaf compaction does nothing.
20. **Contiguity requirements can block leaf compaction.**
    A single huge message, an interleaved summary, or fewer than 8 messages in
    the evictable region makes the pass return `nil`.
21. **Leaf and condensed selection are asymmetric.**
    Leaf requires a contiguous message run. Condensed first requires the context
    to start with a contiguous same-depth summary run, then falls back to
    non-contiguous depth grouping.
22. **Condensed fallback never uses the hard fanout.**
    `CondensedMinFanoutHard = 2` is dead code. The fallback always requires 4.
23. **`runCondensedLoop` has no budget stop.**
    The comment says it stops when under threshold, but it only stops on no
    candidate or no progress.
24. **`TokensSaved` is incomplete.**
    `Engine.Compact` measures the baseline after the leaf pass, so leaf savings
    are not counted. `CompactUntilUnder` never sets it.
25. **Manual compaction can be silently skipped.**
    If an async condensed loop is running, `CompactNow` skips its condensed
    phase and may report "Nothing to compact yet."
26. **Resequencing breaks chronology.**
    When a non-contiguous condensed replacement hits an occupied midpoint, the
    context is resequenced and the new summary is appended at the end, after
    newer messages.
27. **`ContextThreshold` is mostly dead.**
    The agent always passes a budget, so the 75 percent branch is only used by
    direct engine callers. `NeedsCompaction` is unused.
28. **Async condensed results are not announced.**
    `agent.session.summarize` and the channel notice are emitted before the
    background loop finishes, so background condensed summaries are omitted
    from both.
29. **`MaxExpandTokens` is unused.**
    Expansion has no token cap.

### Startup, multi-agent, and lifecycle

30. **Only the default agent's sessions are bootstrapped.**
    Pre-existing history of routed agents is never imported.
31. **The database lives in the default agent's workspace.**
    Changing the default agent or its workspace changes the database location.
32. **`context_manager_config` is ignored.**
    Database path, ignore patterns, and stateless patterns cannot be configured.
33. **A factory failure falls back to legacy with only a warning.**
    Corrupt or unreadable databases do not stop the gateway.
34. **Hot reload does not rebuild the manager.**
    Provider, model, config, and session-store references stay stale, and the
    retrieval tools disappear from the new registry.
35. **Async compaction outlives the turn.**
    It uses `shutdownCtx`. `Close` cancels but does not wait, so a database close
    can race with a running condensed loop.
36. **`conversations.updated_at` is never maintained.**
    It always equals the insert time.
37. **`summaries.model` is never populated.**
    There is no record of which model produced a summary.
38. **The compaction event miscounts.**
    `agent.session.summarize` sets `SummarizedMessages` to the number of
    summaries, not messages.

### Storage and maintenance

39. **Compaction does not reclaim disk.**
    Raw messages and all summaries are retained. Only pruning deletes data.
40. **Trigram availability is effectively required.**
    A missing trigram tokenizer logs a warning, then schema creation fails and
    the runtime falls back to legacy.
41. **No encryption and no restrictive file mode.**
    The database is readable by anyone who can read the workspace.
42. **`DBFileSize` ignores `-shm`.**
    Size reports can undercount transient shared-memory files.
43. **Empty conversation rows are never pruned.**
    `Assemble` and `Compact` create a `conversations` row before any message
    exists. The row has no timestamp for pruning to use, so it is treated as
    undated, is never selected, and still counts toward `max_sessions`.

## Source map

| Topic | Source |
| --- | --- |
| Factory, DB path, pruning, tools, bootstrap | `pkg/agent/context_seahorse.go` |
| Manager selection and legacy fallback | `pkg/agent/turn_coord.go` |
| Assembly budget and max-token reserve | `pkg/agent/context_seahorse.go` |
| Compression trigger paths | `pkg/agent/pipeline_compact.go`, `pkg/agent/pipeline_setup.go`, `pkg/agent/pipeline_llm.go`, `pkg/agent/pipeline_finalize.go` |
| Summary injection into system prompt | `pkg/agent/context.go` |
| Context usage reporting | `pkg/agent/context_usage.go` |
| Engine, ingestion, bootstrap | `pkg/seahorse/short_engine.go` |
| Compaction algorithms and prompts | `pkg/seahorse/short_compaction.go` |
| Assembly and XML | `pkg/seahorse/short_assembler.go` |
| Store operations | `pkg/seahorse/store.go` |
| Schema and migrations | `pkg/seahorse/schema.go` |
| FTS sanitization | `pkg/seahorse/fts5_sanitize.go` |
| Retrieval | `pkg/seahorse/short_retrieval.go` |
| Tool schemas and output | `pkg/seahorse/tool_grep.go`, `pkg/seahorse/tool_expand.go` |
| Constants | `pkg/seahorse/short_constants.go` |
| Message and summary types | `pkg/seahorse/types.go` |
| Pruning policy | `pkg/session/pruner/pruner.go` |
| Build tags and size notes | `docs/guides/custom-builds.md` |
| Startup cost and pruning operations | `docs/architecture/session-startup-and-pruning.md` |

## Related documentation

- [Session System](session-system.md): session keys, scopes, JSONL storage.
- [Session Startup Cost and Automatic Pruning](session-startup-and-pruning.md):
  startup measurements, pruning configuration, disk reclamation.
- [Context Compression](agent-refactor/context.md): the three
  agent compression paths and the legacy manager.
- [Configuration Guide](../guides/configuration.md): agent defaults, workspace,
  sessions, routing.
- [Custom Builds](../guides/custom-builds.md): `no_seahorse`, SQLite
  dependencies, binary sizes.
- [Security](../security/README.md): sensitive-data filtering and deployment
  hardening.
