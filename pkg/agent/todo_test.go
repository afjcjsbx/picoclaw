package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestTodoAgentRegistration(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		cfg := config.DefaultConfig()
		if !cfg.Tools.IsToolEnabled("todo") {
			t.Fatal("todo should be enabled by default")
		}
		cfg.Tools.Todo.Enabled = enabled
		cfg.Agents.Defaults.Workspace = t.TempDir()
		instance := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})
		defer instance.Close()
		tool, ok := instance.Tools.Get("todo")
		if ok != enabled {
			t.Fatalf("registered=%v enabled=%v", ok, enabled)
		}
		if !enabled {
			continue
		}
		if !strings.Contains(tool.Description(), "Skip simple requests") {
			t.Fatal("missing planning guidance")
		}
		// Registry clones used by subagents share implementations but must not share plans.
		clone := instance.Tools.Clone()
		child, _ := clone.Get("todo")
		ctx := tools.WithToolSessionContext(context.Background(), instance.ID, "parent", nil)
		r := tool.Execute(ctx, map[string]any{"action": "write", "todos": []any{map[string]any{"id": "1", "content": "parent plan"}}})
		if r.IsError {
			t.Fatal(r.ForLLM)
		}
		r = child.Execute(tools.WithToolSessionContext(context.Background(), instance.ID, "child", nil), map[string]any{"action": "read"})
		if r.IsError || !strings.Contains(r.ForLLM, `"total_count":0`) {
			t.Fatalf("child plan: %+v", r)
		}
	}
}

type todoSubturnProvider struct {
	calls     int
	wrotePlan bool
}

func (p *todoSubturnProvider) GetDefaultModel() string { return "test-model" }
func (p *todoSubturnProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "plan", Name: "todo", Arguments: map[string]any{"action": "write", "todos": []any{map[string]any{"id": "1", "content": "child step"}}}}}}, nil
	}
	for _, message := range messages {
		if message.Role == "tool" && strings.Contains(message.Content, "child step") {
			p.wrotePlan = true
		}
	}
	return &providers.LLMResponse{Content: "done"}, nil
}
func TestTodoSubturnPlanCleanup(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	agent := al.registry.GetDefaultAgent()
	tool := tools.NewTodoTool()
	agent.Tools.Register(tool)
	provider := &todoSubturnProvider{}
	agent.Provider = provider
	parent := &turnState{ctx: context.Background(), turnID: "parent", agent: agent, pendingResults: make(chan *tools.ToolResult, 4), concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns)}
	_, err := spawnSubTurn(context.Background(), al, parent, SubTurnConfig{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls=%d", provider.calls)
	}
	if !provider.wrotePlan {
		t.Fatal("child did not write plan")
	}
	if len(parent.childTurnIDs) != 1 {
		t.Fatal("missing child")
	}
	r := tool.Execute(tools.WithToolSessionContext(context.Background(), agent.ID, parent.childTurnIDs[0], nil), map[string]any{"action": "read"})
	if r.IsError || !strings.Contains(r.ForLLM, `"total_count":0`) {
		t.Fatalf("child plan not released: %+v", r)
	}
}
