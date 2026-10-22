package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func todoContext(agent, session string) context.Context {
	return WithToolSessionContext(context.Background(), agent, session, nil)
}

func todoArgs(content string) map[string]any {
	return map[string]any{"action": "write", "todos": []any{map[string]any{"id": "step-1", "content": content}}}
}

func readTodo(t *testing.T, tool *TodoTool, ctx context.Context) []TodoItem {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{"action": "read"})
	if result.IsError || !result.Silent {
		t.Fatalf("read: %+v", result)
	}
	var data struct {
		Todos []TodoItem `json:"todos"`
		Total int        `json:"total_count"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &data); err != nil {
		t.Fatal(err)
	}
	if data.Todos == nil || data.Total != len(data.Todos) {
		t.Fatalf("invalid result: %s", result.ForLLM)
	}
	return data.Todos
}

func TestTodoLifecycleAndIsolation(t *testing.T) {
	tool := NewTodoTool()
	ctx := todoContext("main", "chat")
	if len(readTodo(t, tool, ctx)) != 0 {
		t.Fatal("new plan not empty")
	}
	args := todoArgs("implement")
	if r := tool.Execute(ctx, args); r.IsError {
		t.Fatal(r.ForLLM)
	}
	// Mutating caller arguments must not mutate stored state.
	args["todos"].([]any)[0].(map[string]any)["content"] = "mutated"
	got := readTodo(t, tool, ctx)
	if len(got) != 1 || got[0].Content != "implement" || got[0].Status != "pending" || got[0].Priority != "medium" {
		t.Fatalf("got %+v", got)
	}
	for _, other := range []context.Context{todoContext("main", "child"), todoContext("other", "chat")} {
		if len(readTodo(t, tool, other)) != 0 {
			t.Fatal("cross-session leak")
		}
	}
	for _, status := range []string{"in_progress", "completed", "canceled"} {
		r := tool.Execute(
			ctx,
			map[string]any{"action": "write", "todos": []TodoItem{{"step-1", "implement", status, "high"}}},
		)
		if r.IsError {
			t.Fatal(r.ForLLM)
		}
		if got := readTodo(t, tool, ctx); len(got) != 1 || got[0].Status != status {
			t.Fatalf("got %+v", got)
		}
	}
	if r := tool.Execute(ctx, map[string]any{"action": "write", "todos": []any{}}); r.IsError {
		t.Fatal(r.ForLLM)
	}
	if len(readTodo(t, tool, ctx)) != 0 || len(tool.lists) != 0 {
		t.Fatal("clear failed")
	}
}

func TestTodoInvalidWritesAreAtomic(t *testing.T) {
	tool := NewTodoTool()
	ctx := todoContext("main", "chat")
	tool.Execute(ctx, todoArgs("original"))
	cases := []string{
		`{}`,
		`{"action":"delete"}`,
		`{"action":"write"}`,
		`{"action":"write","todos":null}`,
		`{"action":"write","todos":{}}`,
		`{"action":"read","todos":[]}`,
		`{"action":"write","todos":[null]}`,
		`{"action":"write","todos":[1]}`,
		`{"action":"write","todos":[{"id":"a","content":"x","status":"oops"}]}`,
		`{"action":"write","todos":[{"id":"a","content":"x","priority":"urgent"}]}`,
		`{"action":"write","todos":[{"id":"a","content":"x","status":null}]}`,
		`{"action":"write","todos":[{"id":"a","content":"x","status":""}]}`,
		`{"action":"write","todos":[{"id":"a","content":"x","extra":true}]}`,
		`{"action":"write","todos":[{"id":" ","content":"x"}]}`,
		`{"action":"write","todos":[{"id":"a","content":" "}]}`,
		`{"action":"write","todos":[{"id":"a","content":"x"},{"id":"a","content":"y"}]}`,
		`{"action":"write","todos":[{"id":"a","content":"x","status":"in_progress"},{"id":"b","content":"y","status":"in_progress"}]}`,
		`{"action":"write","todos":[],"extra":true}`,
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			var args map[string]any
			json.Unmarshal([]byte(raw), &args)
			if r := tool.Execute(ctx, args); !r.IsError {
				t.Fatalf("accepted %s", raw)
			}
			if got := readTodo(t, tool, ctx); len(got) != 1 || got[0].Content != "original" {
				t.Fatalf("changed state: %+v", got)
			}
		})
	}
	for _, args := range []map[string]any{todoArgs(strings.Repeat("x", 1025)), {"action": "write", "todos": make([]TodoItem, 101)}} {
		if r := tool.Execute(ctx, args); !r.IsError {
			t.Fatal("accepted oversized plan")
		}
	}
	if r := tool.Execute(context.Background(), todoArgs("x")); !r.IsError {
		t.Fatal("missing context accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if r := tool.Execute(canceled, todoArgs("x")); !r.IsError {
		t.Fatal("canceled write accepted")
	}
}

func TestTodoConcurrentSessionsAndCapacity(t *testing.T) {
	tool := NewTodoTool()
	var wg sync.WaitGroup
	for i := 0; i < maxTodoSessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := todoContext("main", fmt.Sprint(i))
			for j := 0; j < 3; j++ {
				if r := tool.Execute(ctx, todoArgs(fmt.Sprint(i))); r.IsError {
					t.Error(r.ForLLM)
				}
				readTodo(t, tool, ctx)
			}
		}(i)
	}
	wg.Wait()
	extra := todoContext("main", "extra")
	if r := tool.Execute(extra, todoArgs("x")); !r.IsError {
		t.Fatal("capacity not enforced")
	}
	tool.Execute(todoContext("main", "0"), map[string]any{"action": "write", "todos": []any{}})
	if r := tool.Execute(extra, todoArgs("x")); r.IsError {
		t.Fatal(r.ForLLM)
	}
}

// Exercise the legacy LLM/tool pipeline with real todo calls, not direct context simulation.
type todoLegacyProvider struct {
	MockLLMProvider
	t     *testing.T
	calls int
}

func (p *todoLegacyProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	defs []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	switch p.calls {
	case 1:
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "read-plan", Name: "todo", Arguments: map[string]any{"action": "read"}},
			},
		}, nil
	case 2:
		last := messages[len(messages)-1]
		if last.Role != "tool" || !strings.Contains(last.Content, `"total_count":0`) {
			p.t.Fatalf("child inherited parent plan: %+v", last)
		}
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{ID: "write-plan", Name: "todo", Arguments: todoArgs("child plan")}},
		}, nil
	default:
		if !strings.Contains(messages[len(messages)-1].Content, "child plan") {
			p.t.Fatal("child write failed")
		}
		return &providers.LLMResponse{Content: "done"}, nil
	}
}

func TestTodoLegacySubagentIsolation(t *testing.T) {
	tool := NewTodoTool()
	ctx := todoContext("main", "parent")
	tool.Execute(ctx, todoArgs("parent plan"))
	registry := NewToolRegistry()
	registry.Register(tool)
	provider := &todoLegacyProvider{t: t}
	manager := NewSubagentManager(provider, "test", t.TempDir())
	manager.SetTools(registry)
	task := &SubagentTask{ID: "child", Task: "do work"}
	manager.runTask(ctx, task, nil)
	if task.Status != "completed" || provider.calls != 3 {
		t.Fatalf("task=%+v calls=%d", task, provider.calls)
	}
	if got := readTodo(t, tool, ctx); len(got) != 1 || got[0].Content != "parent plan" {
		t.Fatalf("parent changed: %+v", got)
	}
	if len(tool.lists) != 1 {
		t.Fatal("legacy child plan was not released")
	}
}
