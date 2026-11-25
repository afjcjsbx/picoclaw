---
description: "Design record for parallel tool calls: the concurrency model, ordering rules, and the max_parallel_tool_calls setting that bounds them."
---
# Parallel Tool Calling — Technical Specification

Status: implemented (phases 1–4 of §5). The shipped behavior is described in [`../architecture/parallel-tool-calls.md`](../architecture/parallel-tool-calls.md); `max_parallel_tool_calls` defaults to `4`. Where PicoClaw already has a mechanism (hooks, steering, loop detection) this specification preserves it.

## 1. Problem

When the model emits several tool calls in the same response, PicoClaw used to execute them as follows:

| Path | Where | Previous behavior |
| --- | --- | --- |
| Main loop | `Pipeline.ExecuteTools`, `pkg/agent/pipeline_execute.go:110` | Strictly sequential. After each tool it checks steering and graceful interrupts; the remaining calls receive `Skipped due to queued user message.` |
| Subagent (legacy fallback) | `RunToolLoop`, `pkg/tools/toolloop.go:161` | All calls in parallel (max 8, `maxParallelToolCalls`), **with no classification at all**. `write_file` + `read_file` on the same path, or two `exec` calls, race. |

Two distinct problems:

1. The main loop wastes latency: `web_search` ×3 or `read_file` ×5 are serialized for no reason.
2. The subagent path is parallel but not safe.

The solution is a single static scheduler shared by both paths.

## 2. Goals and non-goals

Goals:

- Run in parallel only **consecutive, explicitly compatible** calls of the same batch.
- Fail closed: a tool that declares nothing is a sequential barrier.
- The order of `tool` messages in the conversation and in the session is identical to the order of the calls, regardless of which one finishes first.
- No regression on hooks, steering, loop detection, hard abort, runtime events.
- Kill switch: with parallelism disabled the behavior is identical to the previous one.

Non-goals:

- Reordering the batch or inferring semantic dependencies between results (if `web_fetch` depends on `web_search`, the model has to emit them in different turns).
- Parallelism across different LLM iterations.
- Cancelling tools that already started when a steering message arrives (see §3.6).
- Parallelism for MCP tools in v1 (see §3.1).

## 3. Design

### 3.1 Tool classification

A new optional interface in `pkg/tools/shared/parallel.go` (aliased in `shared_facade.go`):

```go
// ParallelSafety describes whether one specific call can run concurrently
// with its neighbors in the same batch.
type ParallelSafety struct {
	Safe  bool         // false => sequential barrier
	Paths []PathAccess // filesystem footprint; empty = none
}

type PathAccess struct {
	Path  string // absolute, symlink-resolved
	Write bool
}

// ParallelAware is implemented by tools that can declare parallel safety.
// Tools that do not implement it are always sequential.
type ParallelAware interface {
	Tool
	ParallelSafety(args map[string]any) ParallelSafety
}
```

The evaluation is **per call** (it receives the arguments) because the footprint depends on the path.

Initial assignment:

| Tool | Class | Footprint |
| --- | --- | --- |
| `web_search`, `web_fetch`, `find_skills` | parallel-safe | none |
| `image_generate` | parallel-safe | none |
| `read_file`, `load_image` | path-scoped | read `path` |
| `list_dir` | path-scoped | read `path` (a directory covers its whole subtree) |
| `search_files` | path-scoped | read `path` (defaults to the workspace root, whole subtree) |
| `write_file`, `edit_file`, `append_file` | path-scoped | write `path` |
| `exec`, process sessions, `cron`, `todo`, `spawn`, `spawn_status`, `subagent`, `delegate`, `install_skill`, `clawhub`, hardware (`i2c`, `spi`, `serial`) | sequential | — (side effects, shared state or hardware) |
| `message`, `reaction`, `send_file`, `send_tts` | sequential | — (delivery order to the user is observable) |
| MCP tools (`mcp_*`) | sequential | — |
| unknown / unregistered tool | sequential | — (the error shows up in order) |

MCP: **always sequential in v1**. A possible future opt-in will be per-server in config (`parallel_safe: true`), not based on `ReadOnlyHint`, which by the MCP specification is an untrusted hint.

Every tool marked safe must have been verified to be goroutine-safe (no unprotected shared mutable state) and must pass `go test -race` with two concurrent executions.

### 3.2 Path collision rules

For each call the tool returns absolute paths. Resolution: join with the workspace if relative, `filepath.Clean`, then resolve symlinks through the nearest existing ancestor (same mechanism as `resolvePathAgainstExistingAncestor`, `pkg/tools/fs/filesystem.go:279`, so it also works for files that do not exist yet).

Two paths **overlap** if they are equal or one is an ancestor of the other (a directory versus a file inside it). The comparison is case-insensitive on every platform: a false conflict only costs parallelism, while a missed conflict on a case-insensitive filesystem corrupts data.

Two accesses **conflict** if they overlap and at least one is a write:

| Case | Outcome |
| --- | --- |
| read A, read A | parallel |
| read A, read B | parallel |
| read A, write A | sequential |
| write A, write A | sequential |
| write A, write B | parallel |
| `search_files` on `.`, write to any file in the workspace | sequential (ancestor) |
| unresolvable path, non-string path, symlink error | `Safe: false` ⇒ barrier |

### 3.3 Planner

New file `pkg/tools/schedule.go` (package `tools`, so both `agent` and `RunToolLoop` use it). A pure function that executes nothing:

```go
// Segment is a run of calls executed together (Parallel) or one by one.
// Calls holds indexes into the original batch, in original order.
type Segment struct {
	Parallel bool
	Calls    []int
}

func PlanBatch(reg *ToolRegistry, calls []providers.ToolCall) []Segment
```

Algorithm:

```
segments, group, reserved := [], [], []
flush():
    len(group) >= 2  -> segments += Segment{Parallel: true, group}
    len(group) == 1  -> appendSequential(group[0])      // a parallel group of 1 is pointless
    group, reserved = [], []
for i, call in calls:
    fp := classify(reg, call)                // ParallelAware or {Safe:false}
    if !fp.Safe:                             // barrier
        flush(); appendSequential(i); continue
    if conflicts(reserved, fp.Paths):        // collision: closes the group,
        flush()                              // the call opens a new one
    group += i; reserved += fp.Paths
flush()
```

`appendSequential` merges adjacent sequential segments. Invariants: the concatenated indexes of the segments are always `0..n-1`; no call overtakes a preceding barrier; a parallel segment has ≥ 2 calls and no conflicting pair.

Examples (names are PicoClaw tools):

```
1 web_search  2 read_file(a)  3 exec  4 web_fetch  5 load_image(b)  6 write_file(a)
→ P[1,2]  S[3]  P[4,5,6]            // 6 does not conflict with 5 (b ≠ a)

1 read_file(a)  2 read_file(b)  3 write_file(a)  4 read_file(c)
→ P[1,2]  P[3,4]                    // 3 closes the group and opens the next one

1 read_file(a)  2 write_file(a)
→ S[1,2]                            // group of 1 + group of 1 → merged into sequential
```

A write is allowed to run in parallel with non-conflicting calls (scaffolding several files in one response is a common model pattern). For a more conservative variant it is enough to make `write_file`/`edit_file`/`append_file` return `Safe: false`: no change to the planner.

The planner knows nothing about hooks or runtime state: it works on the raw calls of the model. Revalidation after hooks is described in §3.4.

### 3.4 Execution: prepare / run / finish

`ExecuteTools` used to do everything for each call in a single ~800-line body. It is split into three phases; **only `run` runs in goroutines**.

| Phase | Content (from `pipeline_execute.go`) | Thread |
| --- | --- | --- |
| `prepare` | turn-profile deny, `BeforeTool` (continue/modify/respond/deny/abort/hard_abort), `ApproveTool`, `feedback.publish`, `ToolExecStart` event, `execCtx` construction | coordinator, in order |
| `run` | only `Tools.ExecuteWithContext(...)` + duration measurement | goroutine (semaphore `max_parallel_tool_calls`) |
| `finish` | hard-abort check, `AfterTool`, result spill, media/`ForUser` delivery, `ToolExecEnd`, message append + session persistence, `recordLoopCall`, steering/graceful poll, `pendingResults` | coordinator, **in original order** |

`prepare` produces a `preparedCall` whose outcome is `run`, `respond` (result synthesized by a hook) or `denied` (denial message). The last two do not go through `run` and are completed by `finish` using the existing code paths.

For each segment of the plan:

- **Sequential**: for each call `prepare → run → finish`, as before, including the steering poll after every tool. With parallelism disabled the whole batch is a single sequential segment: behavior identical to the previous one.
- **Parallel**: `prepare` of all members in order; **revalidation** (below); concurrent `run` bounded by the semaphore; wait for all; `finish` of all members in order.

Revalidation: hooks can change the name and arguments (`modify`) or replace the result. After `prepare`, `PlanBatch` is run again on the post-hook calls of the segment. If the result is not exactly one parallel segment with all members in the `run` outcome, the segment degrades to sequential (`run → finish` one at a time, without re-running the hooks). Hooks can therefore only degrade, never promote. This is a rare case; the accepted side effect is that a steering skip may show `ToolExecStart` followed by `ToolExecSkipped`.

Concurrency: the registry already recovers panics, and `RunConcurrent` adds a second defensive `recover` per goroutine. All goroutines receive `turnCtx`; a hard abort cancels it (`requestHardAbort`) and `finish` checks it after the segment, as it used to after every tool.

Why this shape: `messages`, the session, `loopDetectionHistory` (not protected by a lock), `exec.allResponsesHandled`, `handledAttachments` and the `turnExecution` flags are touched by a single goroutine. No new locks.

### 3.5 Result and event ordering

- `tool` messages are appended and persisted in the original call order. The history is deterministic regardless of completion times.
- `ToolExecStart` is emitted in `prepare` (original order, before the segment runs); `ToolExecEnd` in `finish` (original order, after the whole segment completed). `Duration` is the real duration of each tool.
- `ForUser` outputs of parallel tools are published in `finish`, hence in the original order.
- `AfterTool` for a member of a parallel segment fires once the whole segment is done, not right after its tool. This is documented in the hooks guide.

### 3.6 Interactions

**Steering and graceful interrupt.** The poll happens after every call in a sequential segment (unchanged) and **once at the end of a parallel segment** (after `finish` of all members). A parallel segment is atomic: its results are already computed and are needed to pair each `tool_call_id` with its `tool` message, so they are not discarded. If steering is pending, the following segments receive `Skipped due to queued user message.`. Consequence: steering latency can reach the duration of the slowest tool in the segment. Accepted in v1; the future mitigation (do not start members still queued on the semaphore once steering arrives) is out of scope.

**Loop detection.** `recordLoopCall` runs in `finish` in original order: same counter semantics. If a member reaches `loopStatusCritical`, the following members of the same segment have already run: their real results are appended anyway, then `skipRemainingForLoop` applies to the following segments.

**Hard abort.** Checked after the segment; if active, `ToolControlBreak` as before (results discarded).

**Async tools** (`AsyncExecutor`: `spawn`, `subagent`, …) are classified as sequential: they return immediately with `AsyncResult`, so there is nothing to gain, and callbacks stay per-call as before.

**Tool feedback.** No change to `toolFeedbackBatch`: it is invoked in `prepare`, in order; compact mode (one message per response) keeps working.

### 3.7 `RunToolLoop` (subagent)

The unconditional fan-out (`toolloop.go:161-212`) is replaced by `PlanBatch` plus a shared concurrent executor (`RunConcurrent`, in `pkg/tools`, which holds the semaphore, the recover and the per-index result collection). `ToolLoopConfig` gains `MaxParallel int` (`0` = default cap of 8, `1` = sequential); the legacy path does not read `max_parallel_tool_calls`. There are no hooks or steering in this path, hence no `prepare`/`finish`.

This is an intentional behavior change: tools that do not implement `ParallelAware` are now sequential for subagents as well. `TestRunToolLoop_BoundsConcurrentToolExecution` (`toolloop_concurrency_test.go`) is updated accordingly: the `slow` test tool implements `ParallelAware` with `Safe: true`.

### 3.8 Configuration

In `AgentDefaults` (`pkg/config/config.go`), next to `MaxParallelTurns`, same convention:

```go
MaxParallelToolCalls int `json:"max_parallel_tool_calls,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MAX_PARALLEL_TOOL_CALLS"` // Max concurrent tool calls per batch (0 or 1 = sequential)
```

Default `4` (`pkg/config/defaults.go`); `0` or `1` disables it and restores the sequential behavior. A value > 1 is also the semaphore limit (calls beyond it wait, they are not dropped). Recommended maximum: 8 (the previous value of `maxParallelToolCalls`).

### 3.9 Observability

A single `Info` log per batch after `PlanBatch`, only if the batch has ≥ 2 calls: number of calls, segments (`P[web_search,read_file] S[exec] …`), cap. No new runtime event in v1; if parallel executions need to be distinguished in the UI, add `Parallel bool` to `ToolExecEndPayload`.

## 4. Test plan

Planner (`pkg/tools/schedule_test.go`, table-driven, no I/O):

- The examples of §3.3, plus: empty batch, single call, unknown tool, tool that does not implement `ParallelAware`, malformed arguments, unresolvable path, equivalent relative vs absolute paths, symlink to the same file, ancestor/descendant, case-insensitive paths.
- Properties: concatenating the segment indexes yields `0..n-1`; no parallel segment with < 2 members; no conflicting pair inside a parallel segment.

Execution (`pkg/agent`, with `-race`):

- Slow first tool and fast second tool: `tool` messages in original order, both in `messages` and in the session store.
- Observed maximum concurrency ≤ `max_parallel_tool_calls`.
- Kill switch (`0`/`1`): sequential execution, with the same events and messages as the previous behavior.
- `BeforeTool` with `modify` turning a member into a sequential tool ⇒ segment degraded.
- `BeforeTool` with `respond` and `deny` inside a parallel segment.
- Steering arriving during a parallel segment ⇒ following segments skipped, results of the segment present.
- Hard abort during a parallel segment.
- Critical loop in the middle of a segment.
- A panic in a tool of the segment does not block the others.

Subagent: `write_file(a)` + `read_file(a)` never concurrent; the semaphore bound test updated as in §3.7.

## 5. Rollout

1. **Planner + interface**, implemented by the tools in the table of §3.1, with tests. No behavior change.
2. **`RunToolLoop`** moves to `PlanBatch`/`RunConcurrent`. Closes the existing safety problem.
3. **`ExecuteTools` refactor** into `prepare`/`run`/`finish` with everything sequential (config `0`). Gate: the existing tests (`pipeline_execute_test.go`, `steering_test.go`, `hooks_test.go`, `loop_detection_test.go`) pass unchanged.
4. **Parallel execution** in the main loop behind `max_parallel_tool_calls`, defaulting to `4`.
5. (Optional) per-server MCP opt-in.

Documentation to update: `docs/architecture/steering.md` (poll at segment boundaries), `docs/architecture/hooks/` (`AfterTool` timing, hook order), `docs/architecture/runtime-events.md` (Start/End order), configuration guide.

## 6. Risks

- **Search provider rate limits**: several parallel `web_search` calls can return 429 (e.g. free plans at 1 req/s). Mitigations: default cap of 4 and the existing key rotation. If it shows up, add a per-tool limit to `ParallelSafety`.
- **Small devices**: parallel `search_files` and `read_file` raise peak CPU/RAM on SBCs. The cap is configurable (`0` or `1` disables it).
- **Timing changes for external hooks**: process hooks see all the `BeforeTool` calls of a segment before any execution, and `AfterTool` only at the end of the segment. This must be communicated; anything that depends on read-after-write between calls of the same batch is not affected, because those calls conflict and stay sequential.
- **Tool marked safe but not goroutine-safe**: mitigated by the `-race` audit of phase 1.
- **Steering latency** up to the slowest tool in the segment (§3.6).

## 7. Open questions

1. Parallel writes on different paths in v1 (recommended) or `write_file`/`edit_file`/`append_file` always a barrier (more conservative, no planner change)?
2. Default of `max_parallel_tool_calls`: decided as `4` (not tied to `runtime.NumCPU()`).
3. Steering: wait for the segment (v1, recommended) or, in the future, do not start the members still queued on the semaphore?
