package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestSelfContextUsageBudget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		window  int
		reserve int
		used    int
		free    int
		percent int
	}{
		{name: "normal", window: 10000, reserve: 2000, used: 4000, free: 4000, percent: 50},
		{name: "full", window: 10000, reserve: 2000, used: 8000, percent: 100},
		{name: "over budget", window: 10000, reserve: 2000, used: 9000, percent: 100},
		{name: "reserve exceeds window", window: 1000, reserve: 2000, used: 100, percent: 100},
		{name: "unknown window", used: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := &turnState{agent: &AgentInstance{MaxTokens: 123}}
			if got := ts.selfContextSnapshot(tc.window); got != nil {
				t.Fatalf("budget before a request = %v, want nil", got)
			}
			ts.recordSelfContextUsage(nil, nil, &providers.UsageInfo{PromptTokens: tc.used},
				map[string]any{"max_tokens": tc.reserve})
			got := ts.selfContextSnapshot(tc.window)
			if tc.window == 0 {
				if got != nil {
					t.Fatalf("budget for an unknown window = %v, want nil", got)
				}
				return
			}
			if got["used_tokens"] != tc.used || got["available_tokens"] != tc.free ||
				got["used_percent"] != tc.percent || got["source"] != "provider" ||
				got["output_reserve_tokens"] != tc.reserve {
				t.Fatalf("unexpected budget: %v", got)
			}
		})
	}
}

func TestSelfToolInspectsActiveTurnAndLastInput(t *testing.T) {
	provider := &seahorseTestProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	t.Cleanup(cleanup)
	al.cfg.Tools.Self.Enabled = true
	self := NewSelfTool(&agentRuntimeControl{agent: agent, cfg: al.cfg}, true)
	agent.Tools.Register(self)
	agent.ContextWindow = 32768
	iteration, estimatedInput := 0, 0
	seen := map[string]bool{}
	provider.chatFn = func(
		_ context.Context, messages []providers.Message, defs []providers.ToolDefinition,
		_ string, _ map[string]any,
	) (*providers.LLMResponse, error) {
		iteration++
		for _, message := range messages {
			if message.Role != "tool" || !strings.HasPrefix(message.ToolCallID, "inspect-") {
				continue
			}
			var value map[string]any
			if err := json.Unmarshal([]byte(message.Content), &value); err != nil {
				t.Fatalf("self result is not complete JSON: %s", message.Content)
			}
			seen[message.ToolCallID] = true
			switch message.ToolCallID {
			case "inspect-turn":
				value = value["turn"].(map[string]any)
				if value["phase"] != "tools" || value["iteration"] != float64(1) ||
					value["remaining_iterations"] != float64(9) || value["elapsed_ms"].(float64) < 0 {
					t.Fatalf("unexpected active turn: %v", value)
				}
			case "inspect-provider":
				value = value["context_usage"].(map[string]any)
				if value["source"] != "provider" || value["used_tokens"] != float64(1200) ||
					value["available_tokens"] != float64(32768-4096-1200) {
					t.Fatalf("unexpected provider budget: %v", value)
				}
			case "inspect-estimate":
				value = value["context_usage"].(map[string]any)
				if value["source"] != "local_estimate" || value["used_tokens"] != float64(estimatedInput) {
					t.Fatalf("missing usage reused a stale provider count: %v", value)
				}
			}
		}
		switch iteration {
		case 1:
			return &providers.LLMResponse{
				Usage: &providers.UsageInfo{PromptTokens: 1200},
				ToolCalls: []providers.ToolCall{
					{ID: "inspect-turn", Name: "self", Arguments: map[string]any{"action": "check"}},
					{
						ID: "inspect-provider", Name: "self",
						Arguments: map[string]any{"action": "check"},
					},
				},
			}, nil
		case 2:
			estimatedInput = EstimateToolDefsTokens(defs)
			for _, message := range messages {
				estimatedInput += EstimateMessageTokens(message)
			}
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{
				{
					ID: "inspect-estimate", Name: "self",
					Arguments: map[string]any{"action": "check"},
				},
			}}, nil
		default:
			return &providers.LLMResponse{Content: "done"}, nil
		}
	}
	result, err := al.runAgentLoop(context.Background(), agent, processOptions{
		Dispatch:  DispatchRequest{SessionKey: "self-introspection", UserMessage: "private user message"},
		NoHistory: true,
	})
	if err != nil || result != "done" || iteration != 3 || len(seen) != 3 {
		t.Fatalf("turn = %q, %v after %d calls", result, err, iteration)
	}
}

func TestSelfToolTurnIntrospectionIsReadOnlyAndSessionScoped(t *testing.T) {
	f := newSessionModelFixture(t)
	ctx := f.turnContext("session-a")
	ts := turnStateFromContext(ctx)
	ts.setIteration(4)
	for _, key := range []string{"turn", "context_usage", "turn.iteration", "context_usage.used_tokens"} {
		if result := runSelfTool(f.selfTool, ctx, "set", key, 1, true); !result.IsError {
			t.Fatalf("set on read-only field %s succeeded", key)
		}
	}
	if result := runSelfTool(f.selfTool, ctx, "set", "max_iterations", 2, true); result.IsError {
		t.Fatal(result.ForLLM)
	}
	if got := selfSnapshot(t, f.selfTool, ctx)["turn"].(map[string]any)["remaining_iterations"]; got != float64(0) {
		t.Fatalf("remaining iterations = %v, want 0", got)
	}
	otherCtx := f.turnContext("session-b")
	if got := selfSnapshot(t, f.selfTool, otherCtx)["turn"].(map[string]any)["remaining_iterations"]; got != float64(
		10,
	) {
		t.Fatalf("session-a's limit leaked to session-b: %v", got)
	}
	for _, turnCtx := range []context.Context{ctx, otherCtx, selfTestContext("session-c")} {
		if got := selfSnapshot(t, f.selfTool, turnCtx)["context_usage"]; got != nil {
			t.Fatalf("context usage before an LLM call = %v", got)
		}
	}
	ts.recordSelfContextUsage(nil, nil, &providers.UsageInfo{PromptTokens: 1000}, map[string]any{"max_tokens": 1024})
	if result := runSelfTool(f.selfTool, ctx, "set", "context_window_tokens", 8192, true); result.IsError {
		t.Fatal(result.ForLLM)
	}
	if got := selfSnapshot(t, f.selfTool, ctx)["context_usage"].(map[string]any)["available_tokens"]; got != float64(
		6168,
	) {
		t.Fatalf("available tokens after changing the session window = %v, want 6168", got)
	}
	if got := selfSnapshot(t, f.selfTool, otherCtx)["context_usage"]; got != nil {
		t.Fatalf("usage leaked: %v", got)
	}
	if got := selfSnapshot(t, f.selfTool, selfTestContext("session-c"))["turn"]; got != nil {
		t.Fatalf("inactive turn = %v", got)
	}
}

func TestSelfToolCheckReturnsCompleteArrays(t *testing.T) {
	values := make([]string, 25)
	for i := range values {
		values[i] = "tool"
	}
	self := NewSelfTool(&selfToolTestRuntime{view: map[string]any{"tool_names": values}}, false)
	got := selfSnapshot(t, self, selfTestContext("session-a"))["tool_names"].([]any)
	if len(got) != len(values) {
		t.Fatalf("check truncated the array: %v", got)
	}
}
