# Todo planning tool

`todo` is enabled by default. Disable it with `"tools": {"todo": {"enabled": false}}`
or `PICOCLAW_TOOLS_TODO_ENABLED=false`. It is also available in the web tool library.
Agents with an explicit `AGENT.md` tool allowlist must include `todo` to use it.

The interface follows the read/replace pattern of
[Mistral Vibe's todo tool](https://github.com/mistralai/mistral-vibe/blob/7c19608af06f6c61d63f8f7a5c3430da73fba2ab/vibe/core/tools/builtins/todo.py),
with PicoClaw's request context, tool registry, configuration and silent results.

Read the current plan:

```json
{"action": "read"}
```

Replace it with a complete list (retain IDs across updates):

```json
{
  "action": "write",
  "todos": [
    {"id": "inspect", "content": "Inspect the failing behavior", "status": "completed", "priority": "high"},
    {"id": "fix", "content": "Implement and test the fix", "status": "in_progress", "priority": "high"},
    {"id": "document", "content": "Document the new behavior"}
  ]
}
```

Each item requires a unique, nonblank `id` (at most 128 UTF-8 bytes) and nonblank
`content` (at most 1024 UTF-8 bytes). Status defaults to `pending` and accepts
`pending`, `in_progress`, `completed`, `canceled`. Priority defaults to `medium`
and accepts `low`, `medium`, `high`. There may be at most 100 items per plan.
Use `{"action":"write","todos":[]}` to clear a plan. Missing/null `todos` on a
write is an error, preventing accidental deletion. Invalid replacements leave
the previous plan intact. Reads return `todos`, `total_count` and `message`;
writes return the same shape after updating. Results do not send chat messages.

Plans are isolated by the trusted agent ID and session key supplied by the agent
execution pipeline, including distinct sub-turn session keys. Callers cannot
select another session through tool arguments. Calls without that context fail.
Ephemeral subagent plans are released when their execution ends.
Registry clones can safely share the tool: a mutex protects reads and complete
replacements. Concurrent writes in the same session use last-writer-wins semantics;
this is not a collaborative task database.

Storage is process-local and survives turns and conversation compaction, but not
agent recreation, config reload or process restart. It is not written to disk and
is not automatically restored from conversation history. A tool instance holds
at most 256 nonempty session plans; at capacity, new plans fail explicitly instead
of evicting existing work. Clearing a plan releases its slot. Completed plans
still occupy a slot until cleared. The `/clear` chat command also discards the
current session plan after successfully clearing its history. The full plan is returned only when the tool
is called, rather than injected into every prompt.

## When it helps

Use it for investigations, implementations or other tasks with several dependent
steps: it gives the model an explicit progress checklist that can be re-read after
context compaction. The tool description tells the agent to skip simple requests,
read before resuming a plan, and verify work before marking it complete.

For a single question or one tool call, maintaining a list adds tokens, latency
and consumes tool iterations without much benefit. Status is self-reported, not
proof that work succeeded. This tool neither executes tasks nor schedules reminders;
use the execution and cron tools for those purposes. It also does not replace
persistent project tracking. The expected usefulness is architectural, not a
measured improvement in model completion rates: unit tests validate behavior and
isolation, not model planning quality. Evaluate that separately on representative
multi-step tasks before requiring todo use in an agent's instructions.
