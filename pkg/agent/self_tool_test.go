package agent

import (
	"context"
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
		"web_config":     map[string]any{"format": "markdown"},
		"request":        map[string]any{"channel": "telegram"},
	}}
	tool := NewSelfTool(runtime, false)
	ctx := selfTestContext("session-1")

	if got := runSelfTool(tool, ctx, "check", "web_config.format", nil, false).ForLLM; got != "markdown" {
		t.Fatalf("dot-path result = %q, want markdown", got)
	}
	if got := runSelfTool(tool, ctx, "inspect", "request.channel", nil, false).ForLLM; got != "telegram" {
		t.Fatalf("request channel = %q, want telegram", got)
	}
	for _, key := range []string{"provider", "request.api_key", "__class__"} {
		if result := runSelfTool(tool, ctx, "check", key, nil, false); !result.IsError {
			t.Errorf("check %q was allowed", key)
		}
	}
	if result := runSelfTool(tool, ctx, "set", "note", "value", true); !result.IsError {
		t.Fatal("set succeeded in read-only mode")
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

func TestSelfToolRuntimeSetValidatesIterations(t *testing.T) {
	agent := &AgentInstance{
		modelMu:           &sync.RWMutex{},
		runtimeSettingsMu: &sync.RWMutex{},
		MaxIterations:     20,
	}
	tool := NewSelfTool(&agentRuntimeControl{agent: agent}, true)
	ctx := selfTestContext("session-1")
	if result := runSelfTool(tool, ctx, "set", "max_iterations", float64(35), true); result.IsError {
		t.Fatalf("set max_iterations: %s", result.ForLLM)
	}
	if got := agent.maxIterations(); got != 35 {
		t.Fatalf("max_iterations = %d, want 35", got)
	}
	for _, value := range []any{true, float64(100.5), float64(101)} {
		if result := runSelfTool(tool, ctx, "set", "max_iterations", value, true); !result.IsError {
			t.Errorf("max_iterations accepted invalid value %#v", value)
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

func nestedSelfValue(depth int) any {
	var value any = "leaf"
	for range depth {
		value = []any{value}
	}
	return value
}
