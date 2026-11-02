package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
)

// parRecorder tracks overlapping tool executions and their start/end order.
type parRecorder struct {
	mu      sync.Mutex
	active  int
	max     int
	events  []string
	started chan struct{} // closed on the first execution start
	once    sync.Once
}

func newParRecorder() *parRecorder { return &parRecorder{started: make(chan struct{})} }

func (r *parRecorder) enter(name string) {
	r.mu.Lock()
	r.active++
	r.max = max(r.max, r.active)
	r.events = append(r.events, "start:"+name)
	r.mu.Unlock()
	r.once.Do(func() { close(r.started) })
}

func (r *parRecorder) leave(name string) {
	r.mu.Lock()
	r.active--
	r.events = append(r.events, "end:"+name)
	r.mu.Unlock()
}

func (r *parRecorder) maxConcurrent() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.max
}

func (r *parRecorder) eventIndex(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, e := range r.events {
		if e == event {
			return i
		}
	}
	return -1
}

func parToolParams() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ms":    map[string]any{"type": "number"},
			"label": map[string]any{"type": "string"},
		},
	}
}

func execParTool(rec *parRecorder, name string, args map[string]any) *tools.ToolResult {
	rec.enter(name)
	defer rec.leave(name)
	ms, _ := args["ms"].(float64)
	time.Sleep(time.Duration(ms) * time.Millisecond)
	label, _ := args["label"].(string)
	return tools.NewToolResult("done:" + label)
}

// parTool declares itself parallel-safe; seqTool does not, so it is a barrier.
type parTool struct {
	name string
	rec  *parRecorder
}

func (t *parTool) Name() string               { return t.name }
func (t *parTool) Description() string        { return "parallel-safe test tool" }
func (t *parTool) Parameters() map[string]any { return parToolParams() }
func (t *parTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	return execParTool(t.rec, t.name, args)
}

func (t *parTool) ParallelSafety(map[string]any) tools.ParallelSafety {
	return tools.ParallelSafety{Safe: true}
}

type seqTool struct {
	name string
	rec  *parRecorder
}

func (t *seqTool) Name() string               { return t.name }
func (t *seqTool) Description() string        { return "barrier test tool" }
func (t *seqTool) Parameters() map[string]any { return parToolParams() }
func (t *seqTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	return execParTool(t.rec, t.name, args)
}

func parCall(id, name string, args map[string]any) providers.ToolCall {
	raw, _ := json.Marshal(args)
	return providers.ToolCall{
		ID:        id,
		Type:      "function",
		Name:      name,
		Function:  &providers.FunctionCall{Name: name, Arguments: string(raw)},
		Arguments: args,
	}
}

type toolMsg struct{ ID, Content string }

type parHarness struct {
	t        *testing.T
	al       *AgentLoop
	provider *toolCallProvider
}

func newParHarness(
	t *testing.T,
	maxParallel int,
	calls []providers.ToolCall,
	mutate ...func(*config.Config),
) *parHarness {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:            t.TempDir(),
				ModelName:            "test-model",
				MaxTokens:            4096,
				MaxToolIterations:    10,
				MaxParallelToolCalls: maxParallel,
			},
		},
	}
	for _, m := range mutate {
		m(cfg)
	}
	provider := &toolCallProvider{toolCalls: calls, finalResp: "all done"}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	return &parHarness{t: t, al: al, provider: provider}
}

func (h *parHarness) process() string {
	h.t.Helper()
	resp, err := h.al.ProcessDirectWithChannel(context.Background(), "go", "par-session", "test", "chat1")
	if err != nil {
		h.t.Fatalf("ProcessDirectWithChannel: %v", err)
	}
	return resp
}

// toolMessages returns the persisted tool messages, in session order.
func (h *parHarness) toolMessages() []toolMsg {
	h.t.Helper()
	history, err := h.al.registry.GetDefaultAgent().Sessions.GetHistory(context.Background(), "par-session")
	if err != nil {
		h.t.Fatalf("GetHistory: %v", err)
	}
	var out []toolMsg
	for _, m := range history {
		if m.Role == "tool" {
			out = append(out, toolMsg{ID: m.ToolCallID, Content: m.Content})
		}
	}
	return out
}

func assertToolMessages(t *testing.T, got []toolMsg, want ...toolMsg) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tool messages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tool message %d = %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestExecuteTools_ParallelSegmentOverlapsAndKeepsCallOrder(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 150.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 5.0, "label": "b"}),
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_b", rec: rec})

	h.process()

	if got := rec.maxConcurrent(); got != 2 {
		t.Errorf("max concurrent executions = %d, want 2", got)
	}
	// The second call finishes first, but history follows the call order.
	if rec.eventIndex("end:par_b") > rec.eventIndex("end:par_a") {
		t.Errorf("test setup: par_b should finish before par_a, events %v", rec.events)
	}
	assertToolMessages(t, h.toolMessages(), toolMsg{"c1", "done:a"}, toolMsg{"c2", "done:b"})
}

func TestExecuteTools_DisabledRunsSequentially(t *testing.T) {
	for _, maxParallel := range []int{0, 1} {
		t.Run(fmt.Sprintf("max_parallel_tool_calls=%d", maxParallel), func(t *testing.T) {
			rec := newParRecorder()
			h := newParHarness(t, maxParallel, []providers.ToolCall{
				parCall("c1", "par_a", map[string]any{"ms": 30.0, "label": "a"}),
				parCall("c2", "par_b", map[string]any{"ms": 30.0, "label": "b"}),
			})
			h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
			h.al.RegisterTool(&parTool{name: "par_b", rec: rec})

			h.process()

			if got := rec.maxConcurrent(); got != 1 {
				t.Errorf("max concurrent executions = %d, want 1", got)
			}
			assertToolMessages(t, h.toolMessages(), toolMsg{"c1", "done:a"}, toolMsg{"c2", "done:b"})
		})
	}
}

func TestExecuteTools_BarrierRunsAloneBetweenParallelGroups(t *testing.T) {
	rec := newParRecorder()
	args := func(label string) map[string]any { return map[string]any{"ms": 40.0, "label": label} }
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", args("a")),
		parCall("c2", "par_b", args("b")),
		parCall("c3", "seq_c", args("c")),
		parCall("c4", "par_d", args("d")),
		parCall("c5", "par_e", args("e")),
	})
	for _, name := range []string{"par_a", "par_b", "par_d", "par_e"} {
		h.al.RegisterTool(&parTool{name: name, rec: rec})
	}
	h.al.RegisterTool(&seqTool{name: "seq_c", rec: rec})

	h.process()

	startC, endC := rec.eventIndex("start:seq_c"), rec.eventIndex("end:seq_c")
	if endC != startC+1 {
		t.Errorf("barrier overlapped with other calls, events %v", rec.events)
	}
	for _, name := range []string{"par_a", "par_b"} {
		if rec.eventIndex("end:"+name) > startC {
			t.Errorf("%s did not finish before the barrier, events %v", name, rec.events)
		}
	}
	for _, name := range []string{"par_d", "par_e"} {
		if rec.eventIndex("start:"+name) < endC {
			t.Errorf("%s started before the barrier ended, events %v", name, rec.events)
		}
	}
	if got := rec.maxConcurrent(); got != 2 {
		t.Errorf("max concurrent executions = %d, want 2", got)
	}
	assertToolMessages(t, h.toolMessages(),
		toolMsg{"c1", "done:a"}, toolMsg{"c2", "done:b"}, toolMsg{"c3", "done:c"},
		toolMsg{"c4", "done:d"}, toolMsg{"c5", "done:e"},
	)
}

func TestExecuteTools_SteeringDuringParallelSegmentKeepsResultsAndSkipsLaterSegments(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 120.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 120.0, "label": "b"}),
		parCall("c3", "seq_c", map[string]any{"ms": 1.0, "label": "c"}),
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_b", rec: rec})
	h.al.RegisterTool(&seqTool{name: "seq_c", rec: rec})

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.process()
	}()
	select {
	case <-rec.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the parallel segment to start")
	}
	h.al.Steer(providers.Message{Role: "user", Content: "change course"})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the turn to finish")
	}

	// Both members of the atomic segment keep their real results.
	assertToolMessages(t, h.toolMessages(),
		toolMsg{"c1", "done:a"}, toolMsg{"c2", "done:b"},
		toolMsg{"c3", "Skipped due to queued user message."},
	)
}

// renameHook sends BeforeTool calls of one tool to another.
type renameHook struct{ from, to string }

func (h *renameHook) BeforeTool(
	_ context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	if call.Tool != h.from {
		return call, HookDecision{Action: HookActionContinue}, nil
	}
	next := call.Clone()
	next.Tool = h.to
	return next, HookDecision{Action: HookActionModify}, nil
}

func (h *renameHook) AfterTool(
	_ context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

func TestExecuteTools_HookTurningACallIntoABarrierDegradesTheSegment(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 30.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 30.0, "label": "b"}),
		parCall("c3", "par_c", map[string]any{"ms": 30.0, "label": "c"}),
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_b", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_c", rec: rec})
	h.al.RegisterTool(&seqTool{name: "seq_b", rec: rec})
	if err := h.al.MountHook(NamedHook("rename", &renameHook{from: "par_b", to: "seq_b"})); err != nil {
		t.Fatalf("MountHook: %v", err)
	}

	h.process()

	if got := rec.maxConcurrent(); got != 1 {
		t.Errorf("max concurrent executions = %d, want 1 (hook promoted nothing, so run in order)", got)
	}
	assertToolMessages(t, h.toolMessages(),
		toolMsg{"c1", "done:a"}, toolMsg{"c2", "done:b"}, toolMsg{"c3", "done:c"},
	)
}

func TestExecuteTools_HookDenyInsideParallelSegmentKeepsOrder(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 20.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 20.0, "label": "b"}),
		parCall("c3", "par_c", map[string]any{"ms": 20.0, "label": "c"}),
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_b", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_c", rec: rec})
	if err := h.al.MountHook(NamedHook("deny", &denyToolHook{denyTools: map[string]bool{"par_b": true}})); err != nil {
		t.Fatalf("MountHook: %v", err)
	}

	h.process()

	assertToolMessages(t, h.toolMessages(),
		toolMsg{"c1", "done:a"},
		toolMsg{"c2", "Tool execution denied by hook: tool denied by hook"},
		toolMsg{"c3", "done:c"},
	)
}

func TestExecuteTools_CriticalLoopInsideParallelSegmentSkipsOnlyLaterSegments(t *testing.T) {
	rec := newParRecorder()
	same := map[string]any{"ms": 5.0, "label": "x"}
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", same),
		parCall("c2", "par_a", same),
		parCall("c3", "par_a", same),
		parCall("c4", "seq_b", map[string]any{"ms": 1.0, "label": "b"}),
	}, func(cfg *config.Config) {
		cfg.Agents.Defaults.LoopDetection = config.LoopDetectionConfig{
			Enabled: true, RepeatThreshold: 2, CriticalThreshold: 3, WindowSize: 10,
		}
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&seqTool{name: "seq_b", rec: rec})

	h.process()

	// The three identical calls already ran together, so their results stay.
	assertToolMessages(t, h.toolMessages(),
		toolMsg{"c1", "done:x"}, toolMsg{"c2", "done:x"}, toolMsg{"c3", "done:x"},
		toolMsg{"c4", "Skipped because a repeated tool-call loop was detected."},
	)
	if rec.eventIndex("start:seq_b") != -1 {
		t.Errorf("seq_b must not run after a critical loop, events %v", rec.events)
	}
}

func TestExecuteTools_WriteThenReadSamePathStaysOrdered(t *testing.T) {
	h := newParHarness(t, 4, nil)
	ws := h.al.registry.GetDefaultAgent().Workspace
	h.al.RegisterTool(fstools.NewWriteFileTool(ws, true))
	h.al.RegisterTool(fstools.NewReadFileTool(ws, true, 0))
	h.provider.toolCalls = []providers.ToolCall{
		parCall("c1", "write_file", map[string]any{"path": "note.txt", "content": "hello"}),
		parCall("c2", "read_file", map[string]any{"path": "note.txt"}),
	}

	h.process()

	got := h.toolMessages()
	if len(got) != 2 {
		t.Fatalf("tool messages = %v", got)
	}
	if want := "hello"; !strings.Contains(got[1].Content, want) {
		t.Errorf("read_file result %q does not show the content written just before it", got[1].Content)
	}
	if data, err := os.ReadFile(filepath.Join(ws, "note.txt")); err != nil || string(data) != "hello" {
		t.Errorf("note.txt = %q, %v", data, err)
	}
}

// abortHook ends the turn with action when the tool named in before is about
// to run, or the tool named in after has just finished.
type abortHook struct {
	before, after string
	action        HookAction
}

func (h *abortHook) BeforeTool(
	_ context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	if call.Tool == h.before {
		return call, HookDecision{Action: h.action}, nil
	}
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (h *abortHook) AfterTool(
	_ context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	if result.Tool == h.after {
		return result, HookDecision{Action: h.action}, nil
	}
	return result, HookDecision{Action: HookActionContinue}, nil
}

// assertToolEventsClosed fails when a tool start event has no end or skipped
// event for the same tool, which would leave the call open for subscribers.
func assertToolEventsClosed(t *testing.T, events []runtimeevents.Event) {
	t.Helper()
	open := map[string]int{}
	for _, evt := range events {
		switch p := evt.Payload.(type) {
		case ToolExecStartPayload:
			open[p.Tool]++
		case ToolExecEndPayload:
			open[p.Tool]--
		case ToolExecSkippedPayload:
			open[p.Tool]--
		}
	}
	for tool, n := range open {
		if n > 0 {
			t.Errorf("tool %q has %d start event(s) without an end or skipped event", tool, n)
		}
	}
}

func subscribeToolEvents(t *testing.T, h *parHarness) (<-chan runtimeevents.Event, func()) {
	t.Helper()
	return subscribeRuntimeEventsForTest(
		t, h.al, 64,
		runtimeevents.KindAgentToolExecStart,
		runtimeevents.KindAgentToolExecEnd,
		runtimeevents.KindAgentToolExecSkipped,
	)
}

func TestExecuteTools_HardAbortAfterParallelSegmentEndsTheTurn(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 30.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 30.0, "label": "b"}),
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_b", rec: rec})
	if err := h.al.MountHook(NamedHook("abort", &abortHook{after: "par_a", action: HookActionHardAbort})); err != nil {
		t.Fatalf("MountHook: %v", err)
	}
	events, closeEvents := subscribeToolEvents(t, h)
	defer closeEvents()

	_, _ = h.al.ProcessDirectWithChannel(context.Background(), "go", "par-session", "test", "chat1")

	if got := rec.maxConcurrent(); got != 2 {
		t.Errorf("max concurrent executions = %d, want 2", got)
	}
	h.provider.mu.Lock()
	calls := h.provider.calls
	h.provider.mu.Unlock()
	if calls != 1 {
		t.Errorf("LLM calls = %d, want 1: a hard abort must not start another iteration", calls)
	}
	// Both calls started; the abort hit while finishing the first one, so the
	// whole segment must be closed, not just the call being finished.
	collected := collectRuntimeEventStream(events)
	if got := len(filterRuntimeEvents(collected, runtimeevents.KindAgentToolExecStart)); got != 2 {
		t.Errorf("start events = %d, want 2", got)
	}
	assertToolEventsClosed(t, collected)
}

// A hook abort keeps the session (a hard abort restores it), so every call of
// the aborted segment must still have its tool message.
func TestExecuteTools_HookAbortAfterParallelSegmentAnswersEveryCall(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 30.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 30.0, "label": "b"}),
	})
	h.al.RegisterTool(&parTool{name: "par_a", rec: rec})
	h.al.RegisterTool(&parTool{name: "par_b", rec: rec})
	if err := h.al.MountHook(NamedHook("abort", &abortHook{after: "par_a", action: HookActionAbortTurn})); err != nil {
		t.Fatalf("MountHook: %v", err)
	}
	events, closeEvents := subscribeToolEvents(t, h)
	defer closeEvents()

	_, _ = h.al.ProcessDirectWithChannel(context.Background(), "go", "par-session", "test", "chat1")

	const abandoned = "No result recorded: the turn was aborted."
	assertToolMessages(t, h.toolMessages(), toolMsg{"c1", abandoned}, toolMsg{"c2", abandoned})
	assertToolEventsClosed(t, collectRuntimeEventStream(events))
}

// The abort hits while preparing the second call, after the first one already
// emitted its start event and before anything ran.
func TestExecuteTools_HookAbortWhilePreparingParallelSegmentRunsNothing(t *testing.T) {
	rec := newParRecorder()
	h := newParHarness(t, 4, []providers.ToolCall{
		parCall("c1", "par_a", map[string]any{"ms": 30.0, "label": "a"}),
		parCall("c2", "par_b", map[string]any{"ms": 30.0, "label": "b"}),
		parCall("c3", "par_c", map[string]any{"ms": 30.0, "label": "c"}),
	})
	for _, name := range []string{"par_a", "par_b", "par_c"} {
		h.al.RegisterTool(&parTool{name: name, rec: rec})
	}
	if err := h.al.MountHook(NamedHook("abort", &abortHook{before: "par_b", action: HookActionAbortTurn})); err != nil {
		t.Fatalf("MountHook: %v", err)
	}
	events, closeEvents := subscribeToolEvents(t, h)
	defer closeEvents()

	_, _ = h.al.ProcessDirectWithChannel(context.Background(), "go", "par-session", "test", "chat1")

	if got := rec.maxConcurrent(); got != 0 {
		t.Errorf("max concurrent executions = %d, want 0: no tool may run once the segment is aborted", got)
	}
	const abandoned = "No result recorded: the turn was aborted."
	assertToolMessages(t, h.toolMessages(),
		toolMsg{"c1", abandoned}, toolMsg{"c2", abandoned}, toolMsg{"c3", abandoned})
	assertToolEventsClosed(t, collectRuntimeEventStream(events))
}
