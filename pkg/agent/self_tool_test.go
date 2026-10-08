package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

func selfTestContext(session string) context.Context {
	ctx := tools.WithToolContext(context.Background(), "telegram", "chat-1")
	return tools.WithToolSessionContext(ctx, "main", session, nil)
}

func TestSelfToolDescriptionAndParameters(t *testing.T) {
	readOnly := NewSelfTool(nil, false)
	for _, want := range []string{
		"web_config.enabled", "request.channel", "max_iterations (integer 1–100)",
		"64 keys", "10 levels", "READ-ONLY MODE: set is disabled.",
	} {
		if !strings.Contains(readOnly.Description(), want) {
			t.Errorf("read-only description missing %q", want)
		}
	}

	readWrite := NewSelfTool(nil, true)
	if !strings.Contains(readWrite.Description(), "Warn the user before critical changes") {
		t.Error("read-write description does not warn before critical changes")
	}
	parameters := readWrite.Parameters()["properties"].(map[string]any)
	action := parameters["action"].(map[string]any)
	if got, ok := action["enum"].([]string); !ok || len(got) != 2 || got[0] != "check" || got[1] != "set" {
		t.Errorf("action enum = %#v, want [check set]", action["enum"])
	}
	for _, name := range []string{"action", "key", "value"} {
		if _, ok := parameters[name].(map[string]any)["description"].(string); !ok {
			t.Errorf("parameter %q has no description", name)
		}
	}
}

func TestSelfToolCheckFiltersAndResolvesPaths(t *testing.T) {
	runtime := &selfToolTestRuntime{view: map[string]any{
		"max_iterations": 20,
		"web_config":     map[string]any{"format": "markdown", "provider": "duckduckgo"},
		"request":        map[string]any{"channel": "telegram"},
	}}
	tool := NewSelfTool(runtime, false)
	ctx := selfTestContext("session-1")

	if got := runSelfTool(tool, ctx, "check", "web_config.format", nil, false).ForLLM; got != "markdown" {
		t.Fatalf("dot-path result = %q, want markdown", got)
	}
	if got := runSelfTool(tool, ctx, "check", "web_config.provider", nil, false).ForLLM; got != "duckduckgo" {
		t.Fatalf("web_config.provider = %q, want duckduckgo", got)
	}
	if got := runSelfTool(tool, ctx, "check", "request.channel", nil, false).ForLLM; got != "telegram" {
		t.Fatalf("request channel = %q, want telegram", got)
	}
	for _, key := range []string{"provider", "request.api_key"} {
		if result := runSelfTool(tool, ctx, "check", key, nil, false); !result.IsError {
			t.Errorf("check %q returned a value", key)
		}
	}
	for _, action := range []string{"inspect", "modify"} {
		if result := runSelfTool(tool, ctx, action, "request.channel", nil, false); !result.IsError {
			t.Errorf("undocumented action %q was accepted", action)
		}
	}
	if result := runSelfTool(tool, ctx, "set", "note", "value", true); !result.IsError {
		t.Fatal("set succeeded in read-only mode")
	}
}

func TestSelfToolCheckWithoutKeyReturnsSnapshotValues(t *testing.T) {
	runtime := &selfToolTestRuntime{view: map[string]any{
		"max_iterations":        20,
		"context_window_tokens": 32768,
		"model":                 "local",
		"model_preset":          "local",
		"workspace":             "/tmp/ws",
		"tool_names":            []string{"self", "read_file"},
		"web_config":            map[string]any{"format": "markdown"},
		"request":               map[string]any{"channel": "telegram"},
	}}
	tool := NewSelfTool(runtime, true)
	ctx := selfTestContext("session-1")
	if result := runSelfTool(tool, ctx, "set", "plan", "ship it", true); result.IsError {
		t.Fatalf("set scratchpad: %s", result.ForLLM)
	}

	got := runSelfTool(tool, ctx, "check", "", nil, false).ForLLM
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(got), &snapshot); err != nil {
		t.Fatalf("snapshot is not JSON: %v\n%s", err, got)
	}
	if snapshot["max_iterations"] != float64(20) || snapshot["model"] != "local" ||
		snapshot["context_window_tokens"] != float64(32768) {
		t.Errorf("snapshot is missing runtime values: %s", got)
	}
	if notes, _ := snapshot["scratchpad"].(map[string]any); notes["plan"] != "ship it" {
		t.Errorf("snapshot scratchpad = %v, want the plan note", snapshot["scratchpad"])
	}
}

func TestSelfToolScratchpadIsSessionScopedAndJSONSafe(t *testing.T) {
	tool := NewSelfTool(&selfToolTestRuntime{view: map[string]any{}}, true)
	ctx := selfTestContext("session-1")
	value := map[string]any{"done": true}
	if result := runSelfTool(tool, ctx, "set", "plan", value, true); result.IsError {
		t.Fatalf("set scratchpad: %s", result.ForLLM)
	}
	if got := runSelfTool(tool, ctx, "check", "plan", nil, false).ForLLM; got != `{"done":true}` {
		t.Fatalf("scratchpad value = %q", got)
	}
	if result := runSelfTool(tool, selfTestContext("session-2"), "check", "plan", nil, false); !result.IsError {
		t.Fatal("scratchpad value leaked across sessions")
	}
	if result := runSelfTool(tool, ctx, "set", "unsafe", map[string]any{"api_key": "secret"}, true); !result.IsError {
		t.Fatal("scratchpad accepted a sensitive key")
	}
	if result := runSelfTool(tool, ctx, "set", "deep", nestedSelfValue(11), true); !result.IsError {
		t.Fatal("scratchpad accepted nesting deeper than 10")
	}
	if result := runSelfTool(tool, ctx, "set", "request.channel", "cli", true); !result.IsError {
		t.Fatal("set changed a read-only request field")
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
	if got := runSelfTool(tool, ctx, "check", "max_iterations", nil, false).ForLLM; got != "35" {
		t.Fatalf("session-1 check = %q, want 35", got)
	}
	if got := runSelfTool(tool, otherCtx, "check", "max_iterations", nil, false).ForLLM; got != "20" {
		t.Fatalf("session-2 check = %q, want 20", got)
	}

	for _, value := range []any{true, float64(100.5), float64(0), float64(101)} {
		if result := runSelfTool(tool, ctx, "set", "max_iterations", value, true); !result.IsError {
			t.Errorf("max_iterations accepted invalid value %#v", value)
		}
	}
	if result := runSelfTool(tool, selfTestContext("session-3"), "set", "max_iterations", float64(5), true); !result.IsError {
		t.Error("max_iterations changed outside an active agent turn")
	}
}

func TestSelfToolMaxIterationsRequiresSessionKey(t *testing.T) {
	agent := &AgentInstance{
		ID: "main", modelMu: &sync.RWMutex{}, Tools: tools.NewToolRegistry(), MaxIterations: 20,
	}
	al := &AgentLoop{}
	tool := NewSelfTool(&agentRuntimeControl{agent: agent}, true)
	ts := newTurnState(
		agent,
		processOptions{Dispatch: DispatchRequest{SessionKey: ""}},
		turnEventScope{agentID: agent.ID, sessionKey: "", turnID: "turn-keyless"},
	)
	ctx := WithAgentLoop(withTurnState(selfTestContext(""), ts), al)

	if result := runSelfTool(tool, ctx, "set", "max_iterations", float64(35), true); !result.IsError {
		t.Fatal("max_iterations was accepted for a turn without a session key")
	}
	if got := ts.maxIterations(); got != 20 {
		t.Fatalf("keyless turn max_iterations = %d, want 20", got)
	}
	if got := al.sessionIterationLimit("main", ""); got != 0 {
		t.Fatalf("keyless limit = %d, want none; it would leak to other keyless turns", got)
	}
	if got := al.sessionIterationLimit("main", "session-1"); got != 0 {
		t.Fatalf("session-1 limit = %d, want none", got)
	}
}

func TestSelfToolContextWindowTokensIsNotSensitive(t *testing.T) {
	runtime := &selfToolTestRuntime{view: map[string]any{"context_window_tokens": 32768}}
	tool := NewSelfTool(runtime, true)
	ctx := selfTestContext("session-1")

	if result := runSelfTool(tool, ctx, "check", "context_window_tokens", nil, false); result.IsError ||
		result.ForLLM != "32768" {
		t.Fatalf("check context_window_tokens = %q (error=%v), want 32768", result.ForLLM, result.IsError)
	}
	if result := runSelfTool(tool, ctx, "set", "context_window_tokens", float64(65536), true); result.IsError {
		t.Fatalf("set context_window_tokens: %s", result.ForLLM)
	}
	if runtime.setKey != "context_window_tokens" || runtime.setValue != float64(65536) {
		t.Fatalf("runtime.Set got %q=%v, want context_window_tokens=65536", runtime.setKey, runtime.setValue)
	}
	for _, key := range []string{"access_token", "refresh_token", "context_window_tokens.api_key"} {
		if result := runSelfTool(tool, ctx, "check", key, nil, false); !result.IsError {
			t.Errorf("check %q was allowed", key)
		}
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

func TestSelfToolScratchpadKeyLimit(t *testing.T) {
	tool := NewSelfTool(&selfToolTestRuntime{view: map[string]any{}}, true)
	ctx := selfTestContext("session-limit")
	for i := 0; i < maxSelfRuntimeKeys; i++ {
		if result := runSelfTool(tool, ctx, "set", fmt.Sprintf("key-%d", i), i, true); result.IsError {
			t.Fatalf("set key %d: %s", i, result.ForLLM)
		}
	}
	if result := runSelfTool(tool, ctx, "set", "overflow", true, true); !result.IsError {
		t.Fatal("scratchpad accepted more than 64 keys")
	}
}

func TestSelfToolScratchpadLimitsNoteSizeAndAllowsDelete(t *testing.T) {
	tool := NewSelfTool(&selfToolTestRuntime{view: map[string]any{}}, true)
	ctx := selfTestContext("session-size")

	// A JSON string adds two quote bytes, so this is exactly at the limit.
	atLimit := strings.Repeat("x", maxSelfNoteBytes-2)
	if result := runSelfTool(tool, ctx, "set", "big", atLimit, true); result.IsError {
		t.Fatalf("set note at the size limit: %s", result.ForLLM)
	}
	if result := runSelfTool(tool, ctx, "set", "big", atLimit+"x", true); !result.IsError {
		t.Error("scratchpad accepted a value over the size limit")
	}
	longKey := strings.Repeat("k", maxSelfNoteKeyLen+1)
	if result := runSelfTool(tool, ctx, "set", longKey, "v", true); !result.IsError {
		t.Error("scratchpad accepted a key over the length limit")
	}

	for i := 0; i < maxSelfRuntimeKeys-1; i++ {
		if result := runSelfTool(tool, ctx, "set", fmt.Sprintf("key-%d", i), i, true); result.IsError {
			t.Fatalf("set key %d: %s", i, result.ForLLM)
		}
	}
	if result := runSelfTool(tool, ctx, "set", "overflow", true, true); !result.IsError {
		t.Fatal("scratchpad accepted more than 64 keys")
	}
	if result := runSelfTool(tool, ctx, "set", "big", nil, true); result.IsError {
		t.Fatalf("delete note: %s", result.ForLLM)
	}
	if result := runSelfTool(tool, ctx, "check", "big", nil, false); !result.IsError {
		t.Error("deleted note is still readable")
	}
	if result := runSelfTool(tool, ctx, "set", "overflow", true, true); result.IsError {
		t.Errorf("delete did not free a scratchpad slot: %s", result.ForLLM)
	}
}

func nestedSelfValue(depth int) any {
	var value any = "leaf"
	for range depth {
		value = []any{value}
	}
	return value
}
