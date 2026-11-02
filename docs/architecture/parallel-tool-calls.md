# Parallel Tool Calls

When the model requests several tools in one response, PicoClaw can run compatible ones concurrently. The design record is in [`../design/parallel-tool-calls-spec.md`](../design/parallel-tool-calls-spec.md); this page describes the behavior that shipped.

## Configuration

Parallel execution is on by default. To turn it off:

```json
{ "agents": { "defaults": { "max_parallel_tool_calls": 1 } } }
```

`4` is the default. `0` or `1` keeps the previous behavior: every call runs one by one. A value above `1` is the maximum number of calls running at once; extra calls wait for a free slot.

## Which calls run together

The batch is split into ordered segments by `tools.PlanBatch`. Calls are never reordered: only **consecutive** calls that are compatible share a parallel segment, and a segment finishes before the next one starts.

A call is parallel-eligible only when its tool implements `tools.ParallelAware` and reports `Safe: true` for those arguments. Everything else is a sequential barrier: tools without the interface, unknown tools, and every MCP tool.

| Eligible | Footprint |
| --- | --- |
| `web_search`, `web_fetch`, `find_skills`, `image_generate` | none |
| `read_file`, `list_dir`, `search_files`, `load_image` | read of `path` (directories cover their whole subtree) |
| `write_file`, `edit_file`, `append_file` | write of `path` |

Two file calls conflict when their resolved paths (workspace-relative, symlinks followed, compared case-insensitively) are equal or nested and at least one writes. A conflicting call closes the current group and opens the next one, so `read a`, `read b`, `write a`, `read c` runs as `[read a, read b]` then `[write a, read c]`. A path that cannot be resolved makes the call a barrier. A "parallel" group of one call is just a sequential call.

## What stays ordered

Each call goes through three phases. Only the middle one overlaps.

1. **prepare** (in call order): turn profile, `before_tool`, `approve_tool`, start event, tool feedback.
2. **run**: the tool itself.
3. **finish** (in call order): `after_tool`, delivery to the user, end event, history, loop detection.

Consequences:

- Tool messages are appended to the conversation and the session in call order, whichever call completes first.
- `agent.tool.exec_start` events of a parallel segment are emitted before any of its tools runs; `agent.tool.exec_end` events follow once the whole segment completed, in call order. `duration_ms` is the real duration of each tool.
- `after_tool` hooks of a parallel segment run after the whole segment finished.
- Hooks can only make a segment *less* parallel. If a hook renames a tool, changes its arguments so that the plan no longer holds, denies a call, or answers it with `respond`, the prepared calls of that segment run one by one.

## Steering, interrupts and loops

A parallel segment is atomic. Steering and graceful interrupts are polled after each call of a sequential segment and once after a parallel segment. A pending message skips the segments after it (`Skipped due to queued user message.`); calls of the interrupted segment keep their real results. Steering latency can therefore reach the duration of the slowest call in the segment.

Loop detection records calls in order during finish. If a call of a parallel segment trips the critical threshold, the rest of that segment keeps its (already computed) results and the following segments are skipped.

## Subagent fallback loop

`tools.RunToolLoop` (the legacy subagent path) uses the same planner. It used to run every call concurrently; now tools that do not implement `ParallelAware` run sequentially. `ToolLoopConfig.MaxParallel` is `0` for the default cap of 8 or `1` for sequential.

## Adding a tool

Implement `ParallelSafety(args map[string]any) tools.ParallelSafety`. Mark a call `Safe` only if concurrent executions of it are race-free and its filesystem effects are fully described by `Paths`. If in doubt, do not implement the interface.
