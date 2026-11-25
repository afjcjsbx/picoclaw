---
description: "The memory tool: save durable facts to bounded persistent files that survive across sessions."
---
# Memory tool

The `memory` tool writes to two bounded, persistent files in the agent workspace:

- `memory/MEMORY.md` — facts about the environment, projects, decisions and lessons learned (target `memory`)
- `USER.md` — who the user is: name, preferences, communication style (target `user`)

Both files are injected into the system prompt as a **frozen snapshot taken at session start**. A write reaches disk immediately but is visible to the model only from the next session. This keeps the provider prompt cache warm across turns.

The tool is **enabled by default** (`agents.defaults.memory.memory_enabled: true`). See [Memory options](../reference/tools_configuration.md#memory) for size limits and daily-note configuration.

## Tool arguments

| Argument | Type | Required for | Description |
|----------|------|--------------|-------------|
| `action` | string | every call | `add`, `replace`, or `remove` |
| `target` | string | every call | `memory` (MEMORY.md) or `user` (USER.md) |
| `content` | string | `add`, `replace` | Entry text to write or substitute |
| `old_text` | string | `replace`, `remove` | Text that appears exactly once in the file; identifies the entry to change or delete |

Action semantics:

- **add** — appends `content` as a new line. A duplicate line (exact match after trimming) is silently ignored.
- **replace** — substitutes `old_text` with `content` in-place. The match must be unique; multiple matches return an error.
- **remove** — deletes the whole line(s) that contain `old_text`. The match must be unique.

## Character limits

Both files have a hard character limit (counted in Unicode runes, not bytes). Nothing is ever dropped silently:

| Target | Default limit |
|--------|--------------|
| `memory` (MEMORY.md) | 2 200 chars |
| `user` (USER.md) | 1 375 chars |

If a write would push the file past the limit and the new length is larger than the current length, the tool returns `success: false` with:

```json
{
  "success": false,
  "target": "memory",
  "error": "memory would be 2210/2200 chars: free at least 10 chars ...",
  "usage": "2200/2200 chars",
  "chars_to_free": 10,
  "current_entries": ["line 1", "line 2", ...]
}
```

Shrinking edits (replace/remove that make the file smaller) always succeed, even when the file is already over its limit from a previous configuration.

## Behavior notes

- Writes are **not** visible in the current session's system prompt; they appear in the next session.
- Keep each fact to 1–2 lines. Do not store task progress, logs or investigation details here; use `memory/YYYYMM/YYYYMMDD.md` daily notes for those.
- Do not re-save a fact to "check" whether it was written — the tool returns a `usage` counter on success.
- The tool is per-workspace: every agent using the same workspace shares the same memory files.
