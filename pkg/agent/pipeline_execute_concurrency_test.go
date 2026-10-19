package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type concurrentTestStats struct {
	mu      sync.Mutex
	active  int
	maxSeen int
	events  []string
}

func (s *concurrentTestStats) start(name, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active++
	if s.active > s.maxSeen {
		s.maxSeen = s.active
	}
	s.events = append(s.events, "start:"+name+":"+id)
}

func (s *concurrentTestStats) end(name, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	s.events = append(s.events, "end:"+name+":"+id)
}

func (s *concurrentTestStats) snapshot() (maxSeen int, events []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxSeen, append([]string(nil), s.events...)
}

type rewriteToolHook struct {
	from string
	to   string
}

func (h *rewriteToolHook) BeforeTool(
	_ context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	if call.Tool == h.from {
		call.Tool = h.to
		return call, HookDecision{Action: HookActionModify}, nil
	}
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (h *rewriteToolHook) AfterTool(
	_ context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

type abortToolHook struct {
	id string
}

func (h *abortToolHook) BeforeTool(
	_ context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	if call.Arguments["id"] == h.id {
		return call, HookDecision{Action: HookActionAbortTurn, Reason: "test abort"}, nil
	}
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (h *abortToolHook) AfterTool(
	_ context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

type concurrentTestTool struct {
	name  string
	safe  bool
	delay time.Duration
	stats *concurrentTestStats
}

func (t *concurrentTestTool) Name() string { return t.name }

func (t *concurrentTestTool) Description() string { return "test tool" }

func (t *concurrentTestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
		"required": []string{"id"},
	}
}

func (t *concurrentTestTool) IsConcurrencySafe() bool { return t.safe }

func (t *concurrentTestTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	t.stats.start(t.name, id)
	defer t.stats.end(t.name, id)

	select {
	case <-ctx.Done():
		return tools.ErrorResult(ctx.Err().Error())
	case <-time.After(t.delay):
	}

	return tools.NewToolResult(id)
}

func runToolBatch(
	t *testing.T,
	calls []providers.ToolCall,
	register func(*tools.ToolRegistry),
) (*turnState, *turnExecution) {
	t.Helper()

	provider := &sequenceProvider{
		responses: []*providers.LLMResponse{{
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		}},
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	t.Cleanup(cleanup)

	if register != nil {
		register(agent.Tools)
	}

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-concurrent-tools"), turnEventScope{
		turnID:  "turn-concurrent",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}

	ctrl, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1)
	if err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	if ctrl != ControlToolLoop {
		t.Fatalf("CallLLM control = %v, want ControlToolLoop", ctrl)
	}

	toolCtrl := pipeline.ExecuteTools(context.Background(), context.Background(), ts, exec, 1)
	if toolCtrl != ToolControlContinue {
		t.Fatalf("ExecuteTools control = %v, want ToolControlContinue", toolCtrl)
	}
	return ts, exec
}

func toolResultContents(messages []providers.Message) []string {
	var contents []string
	for _, msg := range messages {
		if msg.Role == "tool" {
			contents = append(contents, msg.Content)
		}
	}
	return contents
}

func TestExecuteTools_ConcurrencySafeCallsRunInParallelAndKeepOrder(t *testing.T) {
	stats := &concurrentTestStats{}
	tool := &concurrentTestTool{name: "safe_read", safe: true, delay: 40 * time.Millisecond, stats: stats}

	var calls []providers.ToolCall
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("%d", i)
		calls = append(calls, providers.ToolCall{
			ID:        "call-" + id,
			Name:      tool.Name(),
			Arguments: map[string]any{"id": id},
		})
	}

	_, exec := runToolBatch(t, calls, func(registry *tools.ToolRegistry) {
		registry.Register(tool)
	})

	maxSeen, _ := stats.snapshot()
	if maxSeen < 2 {
		t.Fatalf("max concurrent executions = %d, want at least 2", maxSeen)
	}
	if maxSeen > tools.MaxParallelToolCalls {
		t.Fatalf("max concurrent executions = %d, want <= %d", maxSeen, tools.MaxParallelToolCalls)
	}

	got := toolResultContents(exec.messages)
	want := []string{"0", "1", "2", "3"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool result order = %v, want %v", got, want)
	}
}

func TestExecuteTools_BoundsConcurrentToolFanOut(t *testing.T) {
	stats := &concurrentTestStats{}
	tool := &concurrentTestTool{name: "safe_read", safe: true, delay: 10 * time.Millisecond, stats: stats}

	callCount := tools.MaxParallelToolCalls * 2
	var calls []providers.ToolCall
	for i := 0; i < callCount; i++ {
		id := fmt.Sprintf("%d", i)
		calls = append(calls, providers.ToolCall{
			ID:        "call-" + id,
			Name:      tool.Name(),
			Arguments: map[string]any{"id": id},
		})
	}

	_, exec := runToolBatch(t, calls, func(registry *tools.ToolRegistry) {
		registry.Register(tool)
	})

	maxSeen, _ := stats.snapshot()
	if maxSeen > tools.MaxParallelToolCalls {
		t.Fatalf("max concurrent executions = %d, want <= %d", maxSeen, tools.MaxParallelToolCalls)
	}
	if maxSeen < 2 {
		t.Fatalf("max concurrent executions = %d, want at least 2", maxSeen)
	}

	got := toolResultContents(exec.messages)
	if len(got) != callCount {
		t.Fatalf("tool results = %d, want %d", len(got), callCount)
	}
	for i, content := range got {
		if content != fmt.Sprintf("%d", i) {
			t.Fatalf("tool result %d = %q, want %q", i, content, fmt.Sprintf("%d", i))
		}
	}
}

func TestExecuteTools_NonSafeToolSplitsConcurrentGroups(t *testing.T) {
	stats := &concurrentTestStats{}
	safe := &concurrentTestTool{name: "safe_read", safe: true, delay: 40 * time.Millisecond, stats: stats}
	unsafe := &concurrentTestTool{name: "side_effect", safe: false, delay: 40 * time.Millisecond, stats: stats}

	calls := []providers.ToolCall{
		{ID: "call-1", Name: safe.Name(), Arguments: map[string]any{"id": "1"}},
		{ID: "call-2", Name: safe.Name(), Arguments: map[string]any{"id": "2"}},
		{ID: "call-3", Name: unsafe.Name(), Arguments: map[string]any{"id": "3"}},
		{ID: "call-4", Name: safe.Name(), Arguments: map[string]any{"id": "4"}},
		{ID: "call-5", Name: safe.Name(), Arguments: map[string]any{"id": "5"}},
	}

	_, exec := runToolBatch(t, calls, func(registry *tools.ToolRegistry) {
		registry.Register(safe)
		registry.Register(unsafe)
	})

	maxSeen, events := stats.snapshot()
	if maxSeen != 2 {
		t.Fatalf("max concurrent executions = %d, want 2 (two safe groups, unsafe isolated)", maxSeen)
	}

	// The side-effectful call must start after both calls of the first safe
	// group finished, and before the second safe group starts.
	unsafeStart := -1
	for i, event := range events {
		if event == "start:side_effect:3" {
			unsafeStart = i
			break
		}
	}
	if unsafeStart < 0 {
		t.Fatalf("unsafe tool never started: %v", events)
	}
	for i, event := range events {
		if i >= unsafeStart {
			break
		}
		if event == "start:safe_read:4" || event == "start:safe_read:5" {
			t.Fatalf("second safe group started before the unsafe call: %v", events)
		}
	}
	for i, event := range events {
		if i <= unsafeStart {
			continue
		}
		if event == "start:safe_read:1" || event == "start:safe_read:2" {
			t.Fatalf("first safe group started after the unsafe call: %v", events)
		}
	}

	got := toolResultContents(exec.messages)
	want := []string{"1", "2", "3", "4", "5"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool result order = %v, want %v", got, want)
	}
}

func TestExecuteTools_HookRewriteToUnsafeToolFallsBackToSequential(t *testing.T) {
	stats := &concurrentTestStats{}
	safe := &concurrentTestTool{name: "safe_read", safe: true, delay: 40 * time.Millisecond, stats: stats}
	unsafe := &concurrentTestTool{name: "side_effect", safe: false, delay: 40 * time.Millisecond, stats: stats}

	calls := []providers.ToolCall{
		{ID: "call-1", Name: safe.Name(), Arguments: map[string]any{"id": "1"}},
		{ID: "call-2", Name: safe.Name(), Arguments: map[string]any{"id": "2"}},
	}

	provider := &sequenceProvider{
		responses: []*providers.LLMResponse{{
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		}},
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	t.Cleanup(cleanup)
	agent.Tools.Register(safe)
	agent.Tools.Register(unsafe)
	if err := al.hooks.Mount(NamedHook("rewrite", &rewriteToolHook{
		from: safe.Name(),
		to:   unsafe.Name(),
	})); err != nil {
		t.Fatalf("Mount hook failed: %v", err)
	}

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-concurrent-hook"), turnEventScope{
		turnID:  "turn-concurrent-hook",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}
	if _, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1); err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	toolCtrl := pipeline.ExecuteTools(context.Background(), context.Background(), ts, exec, 1)
	if toolCtrl != ToolControlContinue {
		t.Fatalf("ExecuteTools control = %v, want ToolControlContinue", toolCtrl)
	}

	maxSeen, events := stats.snapshot()
	if maxSeen != 1 {
		t.Fatalf("max concurrent executions = %d, want 1 after rewrite to an unsafe tool", maxSeen)
	}
	unsafeStarts := 0
	for _, event := range events {
		if event == "start:side_effect:1" || event == "start:side_effect:2" {
			unsafeStarts++
		}
	}
	if unsafeStarts != 2 {
		t.Fatalf("rewritten calls executed %d times, want 2: %v", unsafeStarts, events)
	}
}

func TestExecuteTools_HookAbortInConcurrentGroupStopsAfterPreparedPrefix(t *testing.T) {
	stats := &concurrentTestStats{}
	safe := &concurrentTestTool{name: "safe_read", safe: true, delay: 10 * time.Millisecond, stats: stats}

	calls := []providers.ToolCall{
		{ID: "call-1", Name: safe.Name(), Arguments: map[string]any{"id": "1"}},
		{ID: "call-2", Name: safe.Name(), Arguments: map[string]any{"id": "2"}},
		{ID: "call-3", Name: safe.Name(), Arguments: map[string]any{"id": "3"}},
	}

	provider := &sequenceProvider{
		responses: []*providers.LLMResponse{{
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		}},
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	t.Cleanup(cleanup)
	agent.Tools.Register(safe)
	if err := al.hooks.Mount(NamedHook("abort", &abortToolHook{id: "2"})); err != nil {
		t.Fatalf("Mount hook failed: %v", err)
	}

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-concurrent-abort"), turnEventScope{
		turnID:  "turn-concurrent-abort",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}
	if _, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1); err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}
	toolCtrl := pipeline.ExecuteTools(context.Background(), context.Background(), ts, exec, 1)
	if toolCtrl != ToolControlBreak {
		t.Fatalf("ExecuteTools control = %v, want ToolControlBreak", toolCtrl)
	}
	if !exec.abortedByHook {
		t.Fatal("expected exec.abortedByHook to be set")
	}

	got := toolResultContents(exec.messages)
	want := []string{"1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool result contents = %v, want %v", got, want)
	}
}

func TestExecuteTools_SteeringSkipsCallsAfterConcurrentGroup(t *testing.T) {
	stats := &concurrentTestStats{}
	safe := &concurrentTestTool{name: "safe_read", safe: true, delay: 40 * time.Millisecond, stats: stats}
	unsafe := &concurrentTestTool{name: "side_effect", safe: false, delay: 10 * time.Millisecond, stats: stats}

	calls := []providers.ToolCall{
		{ID: "call-1", Name: safe.Name(), Arguments: map[string]any{"id": "1"}},
		{ID: "call-2", Name: safe.Name(), Arguments: map[string]any{"id": "2"}},
		{ID: "call-3", Name: unsafe.Name(), Arguments: map[string]any{"id": "3"}},
	}

	provider := &sequenceProvider{
		responses: []*providers.LLMResponse{{
			ToolCalls:    calls,
			FinishReason: "tool_calls",
		}},
	}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	t.Cleanup(cleanup)
	agent.Tools.Register(safe)
	agent.Tools.Register(unsafe)

	pipeline := NewPipeline(al)
	ts := newTurnState(agent, makeTestProcessOpts("test-concurrent-steering"), turnEventScope{
		turnID:  "turn-concurrent-steering",
		context: newTurnContext(nil, nil, nil),
	})
	exec, err := pipeline.SetupTurn(context.Background(), ts)
	if err != nil {
		t.Fatalf("SetupTurn failed: %v", err)
	}
	if _, err := pipeline.CallLLM(context.Background(), context.Background(), ts, exec, 1); err != nil {
		t.Fatalf("CallLLM failed: %v", err)
	}

	if err := al.enqueueSteeringMessage(
		ts.sessionKey,
		ts.agentID,
		providers.Message{Role: "user", Content: "stop"},
	); err != nil {
		t.Fatalf("enqueueSteeringMessage failed: %v", err)
	}

	toolCtrl := pipeline.ExecuteTools(context.Background(), context.Background(), ts, exec, 1)
	if toolCtrl != ToolControlContinue {
		t.Fatalf("ExecuteTools control = %v, want ToolControlContinue", toolCtrl)
	}

	got := toolResultContents(exec.messages)
	want := []string{
		"1",
		"2",
		"Skipped due to queued user message.",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("tool result contents = %v, want %v", got, want)
	}

	maxSeen, events := stats.snapshot()
	if maxSeen < 2 {
		t.Fatalf("max concurrent executions = %d, want the safe group to overlap", maxSeen)
	}
	for _, event := range events {
		if event == "start:side_effect:3" {
			t.Fatalf("side-effectful call ran despite queued steering: %v", events)
		}
	}
}
