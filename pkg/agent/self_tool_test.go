package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/tools"
)

type selfToolTestRuntime struct {
	view     map[string]any
	setKey   string
	setValue any
}

func (r *selfToolTestRuntime) Snapshot(context.Context) map[string]any { return r.view }
func (r *selfToolTestRuntime) Set(_ context.Context, key string, value any) error {
	r.setKey, r.setValue = key, value
	return nil
}

func runSelfTool(tool *SelfTool, ctx context.Context, action, key string, value any, hasValue bool) *tools.ToolResult {
	args := map[string]any{"action": action}
	if key != "" {
		args["key"] = key
	}
	if hasValue {
		args["value"] = value
	}
	return tool.Execute(ctx, args)
}

func selfSnapshot(t *testing.T, tool *SelfTool, ctx context.Context) map[string]any {
	t.Helper()
	result := runSelfTool(tool, ctx, "check", "", nil, false)
	if result.IsError {
		t.Fatal(result.ForLLM)
	}
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func selfTestContext(session string) context.Context {
	ctx := tools.WithToolContext(context.Background(), "telegram", "chat-1")
	return tools.WithToolSessionContext(ctx, "main", session, nil)
}

func TestSelfToolDescriptionAndParameters(t *testing.T) {
	tool := NewSelfTool(nil, false)
	for _, want := range []string{"last LLM input", "null", "next turn", "READ-ONLY MODE"} {
		if !strings.Contains(tool.Description(), want) {
			t.Errorf("description missing %q", want)
		}
	}
	properties := tool.Parameters()["properties"].(map[string]any)
	if got := properties["action"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []string{"check", "set"}) {
		t.Fatalf("actions = %v", got)
	}
	if got := properties["key"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []string{
		"max_iterations", "context_window_tokens", "model_preset",
	}) {
		t.Fatalf("keys = %v", got)
	}
}

func TestSelfToolRejectsUnsupportedArguments(t *testing.T) {
	runtime := &selfToolTestRuntime{view: map[string]any{"model": "local"}}
	tool := NewSelfTool(runtime, true)
	cases := make([]map[string]any, 0, 23)
	cases = append(cases, []map[string]any{
		{},
		{"action": 1},
		{"action": "check", "extra": true},
		{"action": "check", "key": "model"},
		{"action": "check", "key": nil},
		{"action": "check", "value": nil},
		{"action": "set"},
		{"action": "set", "key": nil, "value": 10},
		{"action": "set", "key": "max_iterations"},
	}...)
	for _, action := range []string{"list", "schema", "explain", "diff", "history", "undo", "reset", "logs"} {
		cases = append(cases, map[string]any{"action": action})
	}
	for _, key := range []string{"", "note", "model", "config", "max_iterations.child", "api_key"} {
		cases = append(cases, map[string]any{"action": "set", "key": key, "value": nil})
	}
	for _, args := range cases {
		if result := tool.Execute(context.Background(), args); !result.IsError {
			t.Errorf("accepted %v", args)
		}
	}
	if runtime.setKey != "" {
		t.Fatal("invalid arguments reached runtime")
	}
}

func TestSelfToolReadOnlyAndUnavailableRuntime(t *testing.T) {
	runtime := &selfToolTestRuntime{view: map[string]any{"model": "local"}}
	tool := NewSelfTool(runtime, false)
	if got := selfSnapshot(t, tool, context.Background())["model"]; got != "local" {
		t.Fatal(got)
	}
	for _, value := range []any{10, nil} {
		if result := runSelfTool(tool, context.Background(), "set", "max_iterations", value, true); !result.IsError {
			t.Fatal("read-only tool accepted set")
		}
	}
	if runtime.setKey != "" {
		t.Fatal("read-only set reached runtime")
	}
	unavailable := NewSelfTool(nil, true)
	if result := runSelfTool(unavailable, context.Background(), "check", "", nil, false); !result.IsError {
		t.Fatal("missing runtime accepted check")
	}
	if result := runSelfTool(unavailable, context.Background(), "set", "max_iterations", nil, true); !result.IsError {
		t.Fatal("missing runtime accepted set")
	}
	runtime.view["invalid"] = math.Inf(1)
	if result := runSelfTool(tool, context.Background(), "check", "", nil, false); !result.IsError {
		t.Fatal("invalid snapshot accepted")
	}
}

func TestSelfToolSnapshotExposesOnlyRuntimeFields(t *testing.T) {
	f := newSessionModelFixture(t)
	ctx := f.turnContext("private-session")
	snapshot := selfSnapshot(t, f.selfTool, ctx)
	keys := []string{
		"model",
		"model_presets",
		"max_iterations",
		"context_window_tokens",
		"tool_names",
		"turn",
		"context_usage",
	}
	if len(snapshot) != len(keys) {
		t.Fatalf("unexpected snapshot fields: %v", snapshot)
	}
	for _, key := range keys {
		if _, ok := snapshot[key]; !ok {
			t.Errorf("missing field %s", key)
		}
	}
	if !reflect.DeepEqual(snapshot["model_presets"], []any{"local", "remote"}) {
		t.Fatal(snapshot["model_presets"])
	}
	encoded, _ := json.Marshal(snapshot)
	for _, private := range []string{
		"local-key", "remote-key", f.al.GetConfig().ModelList[0].APIBase,
		f.baseAgent.Workspace, "private-session",
	} {
		if strings.Contains(string(encoded), private) {
			t.Errorf("snapshot exposed %q", private)
		}
	}
}

func TestSelfToolWritesRequireActiveSessionTurn(t *testing.T) {
	f := newSessionModelFixture(t)
	for _, key := range []string{"max_iterations", "context_window_tokens", "model_preset"} {
		for _, ctx := range []context.Context{selfTestContext("session-a"), f.turnContext("")} {
			for _, value := range []any{nil, map[string]any{
				"max_iterations": 5, "context_window_tokens": 8192, "model_preset": "remote",
			}[key]} {
				if result := runSelfTool(f.selfTool, ctx, "set", key, value, true); !result.IsError {
					t.Errorf("%s accepted write without active session", key)
				}
			}
		}
	}
	if f.al.sessionRuntimeFor(f.baseAgent.ID, "") != (sessionRuntime{}) ||
		f.al.sessionModel(sessionModelKey{agentID: f.baseAgent.ID}) != nil {
		t.Fatal("write leaked to keyless turns")
	}
}

func TestSelfToolNullResetsOnlySelectedSessionSetting(t *testing.T) {
	f := newSessionModelFixture(t)
	settings := map[string]any{"max_iterations": 3, "context_window_tokens": 8192, "model_preset": "remote"}
	for _, session := range []string{"session-a", "session-b"} {
		for key, value := range settings {
			if result := runSelfTool(f.selfTool, f.turnContext(session), "set", key, value, true); result.IsError {
				t.Fatal(result.ForLLM)
			}
		}
	}
	id := f.baseAgent.ID
	ctx := f.turnContext("session-a")
	ts := turnStateFromContext(ctx)
	ts.maxIterationsOverride.Store(3)
	ts.contextWindowOverride.Store(8192)
	if result := runSelfTool(f.selfTool, ctx, "set", "max_iterations", nil, true); result.IsError {
		t.Fatal(result.ForLLM)
	}
	if ts.maxIterations() != f.baseAgent.MaxIterations || ts.contextWindow() != 8192 ||
		f.al.sessionIterationLimit(id, "session-a") != 0 || f.al.sessionContextWindow(id, "session-a") != 8192 {
		t.Fatal("iteration reset changed the wrong setting")
	}
	if result := runSelfTool(f.selfTool, ctx, "set", "context_window_tokens", nil, true); result.IsError {
		t.Fatal(result.ForLLM)
	}
	if ts.contextWindow() != f.baseAgent.ContextWindow || f.al.sessionContextWindow(id, "session-a") != 0 {
		t.Fatal("context window did not reset")
	}
	key := sessionModelKey{agentID: id, sessionKey: "session-a"}
	override := f.al.acquireSessionModelOverride(key)
	ts.agent = override.apply(f.baseAgent)
	if result := runSelfTool(f.selfTool, ctx, "set", "model_preset", nil, true); result.IsError {
		t.Fatal(result.ForLLM)
	}
	if selfSnapshot(t, f.selfTool, ctx)["model"] != "remote" {
		t.Fatal("model reset changed the active turn")
	}
	f.al.releaseSessionModelOverride(override)
	if f.al.sessionModel(key) != nil || f.callTurn("session-a") != "local reply" {
		t.Fatal("model did not reset next turn")
	}
	if f.al.sessionIterationLimit(id, "session-b") != 3 || f.al.sessionContextWindow(id, "session-b") != 8192 ||
		f.callTurn("session-b") != "remote reply" {
		t.Fatal("reset leaked to another session")
	}
	if f.baseAgent.MaxIterations != 10 || f.baseAgent.Model != "local" {
		t.Fatal("reset changed configured agent")
	}
}

func TestSelfToolMaxIterationsIsSessionScoped(t *testing.T) {
	agent := &AgentInstance{
		ID: "main", modelMu: &sync.RWMutex{}, Tools: tools.NewToolRegistry(), MaxIterations: 20,
	}
	al := &AgentLoop{}
	tool := NewSelfTool(&agentRuntimeControl{agent: agent}, true)
	turnContext := func(session string) (context.Context, *turnState) {
		ts := newTurnState(
			agent,
			processOptions{Dispatch: DispatchRequest{SessionKey: session}},
			turnEventScope{agentID: agent.ID, sessionKey: session, turnID: "turn-" + session},
		)
		return WithAgentLoop(withTurnState(selfTestContext(session), ts), al), ts
	}
	ctx, ts := turnContext("session-1")
	otherCtx, otherTS := turnContext("session-2")

	if result := runSelfTool(tool, ctx, "set", "max_iterations", float64(35), true); result.IsError {
		t.Fatalf("set max_iterations: %s", result.ForLLM)
	}
	if got := ts.maxIterations(); got != 35 {
		t.Fatalf("running turn max_iterations = %d, want 35", got)
	}
	if got := agent.MaxIterations; got != 20 {
		t.Fatalf("agent max_iterations = %d, want it to stay 20", got)
	}
	if got := al.sessionIterationLimit("main", "session-1"); got != 35 {
		t.Fatalf("session-1 limit = %d, want 35", got)
	}
	if got := al.sessionIterationLimit("main", "session-2"); got != 0 {
		t.Fatalf("session-2 limit = %d, want none", got)
	}
	if got := otherTS.maxIterations(); got != 20 {
		t.Fatalf("session-2 turn max_iterations = %d, want 20", got)
	}
	if got := fmt.Sprint(selfSnapshot(t, tool, ctx)["max_iterations"]); got != "35" {
		t.Fatalf("session-1 check = %q, want 35", got)
	}
	if got := fmt.Sprint(selfSnapshot(t, tool, otherCtx)["max_iterations"]); got != "20" {
		t.Fatalf("session-2 check = %q, want 20", got)
	}

	for _, value := range []any{true, float64(100.5), float64(0), float64(101)} {
		if result := runSelfTool(tool, ctx, "set", "max_iterations", value, true); !result.IsError {
			t.Errorf("max_iterations accepted invalid value %#v", value)
		}
	}
	result := runSelfTool(tool, selfTestContext("session-3"), "set", "max_iterations", float64(5), true)
	if !result.IsError {
		t.Error("max_iterations changed outside an active agent turn")
	}
}

func TestSelfToolContextWindowTokensIsSessionScoped(t *testing.T) {
	f := newSessionModelFixture(t)
	ctx := f.turnContext("session-a")
	base := f.baseAgent.ContextWindow

	if got := selfSnapshot(t, f.selfTool, ctx)["context_window_tokens"]; got != float64(base) {
		t.Fatalf("context window = %v, want %d", got, base)
	}
	for _, value := range []any{float64(100), float64(2000000), float64(0), "big", true} {
		if result := runSelfTool(f.selfTool, ctx, "set", "context_window_tokens", value, true); !result.IsError {
			t.Errorf("context_window_tokens accepted invalid value %#v", value)
		}
	}
	if f.baseAgent.ContextWindow != base {
		t.Fatalf("invalid set changed the agent context window to %d", f.baseAgent.ContextWindow)
	}

	if result := runSelfTool(f.selfTool, ctx, "set", "context_window_tokens", float64(65536), true); result.IsError {
		t.Fatalf("set context_window_tokens: %s", result.ForLLM)
	}
	if f.baseAgent.ContextWindow != base {
		t.Fatalf("agent context window changed to %d, want it to stay %d", f.baseAgent.ContextWindow, base)
	}
	if got := f.al.sessionContextWindow(f.baseAgent.ID, "session-a"); got != 65536 {
		t.Fatalf("session-a context window = %d, want 65536", got)
	}
	if got := f.al.sessionContextWindow(f.baseAgent.ID, "session-b"); got != 0 {
		t.Fatalf("session-b context window = %d, want none", got)
	}
	if got := fmt.Sprint(selfSnapshot(t, f.selfTool, ctx)["context_window_tokens"]); got != "65536" {
		t.Fatalf("check after set = %q, want 65536", got)
	}
	otherCtx := f.turnContext("session-b")
	if got := fmt.Sprint(selfSnapshot(t, f.selfTool, otherCtx)["context_window_tokens"]); got != fmt.Sprint(
		base,
	) {
		t.Fatalf("session-b check = %q, want %d", got, base)
	}
}

func TestSelfInt(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  int
		ok    bool
	}{
		{35, 35, true},
		{float64(35), 35, true},
		{json.Number("35"), 35, true},
		{float64(100.5), 0, false},
		{json.Number("35.5"), 0, false},
		{float64(1e30), 0, false},
		{"35", 0, false},
		{true, 0, false},
		{nil, 0, false},
	} {
		got, err := selfInt(tc.value)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("selfInt(%#v) = %d, %v; want %d, ok=%v", tc.value, got, err, tc.want, tc.ok)
		}
	}
}
