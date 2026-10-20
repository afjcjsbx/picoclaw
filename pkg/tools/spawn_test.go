package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mockSpawner implements SubTurnSpawner for testing.
type mockSpawner struct {
	lastConfig SubTurnConfig
	done       chan struct{}
}

func (m *mockSpawner) SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	m.lastConfig = cfg
	if m.done != nil {
		close(m.done)
	}

	// Extract task from system prompt for response
	task := cfg.SystemPrompt
	if strings.Contains(task, "Task: ") {
		parts := strings.Split(task, "Task: ")
		if len(parts) > 1 {
			task = parts[1]
		}
	}
	return &ToolResult{
		ForLLM:  "Task completed: " + task,
		ForUser: "Task completed",
	}, nil
}

func TestSpawnTool_Execute_EmptyTask(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test")
	tool := NewSpawnTool(manager)

	ctx := context.Background()

	tests := []struct {
		name string
		args map[string]any
	}{
		{"empty string", map[string]any{"task": ""}},
		{"whitespace only", map[string]any{"task": "   "}},
		{"tabs and newlines", map[string]any{"task": "\t\n  "}},
		{"missing task key", map[string]any{"label": "test"}},
		{"wrong type", map[string]any{"task": 123}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(ctx, tt.args)
			if result == nil {
				t.Fatal("Result should not be nil")
			}
			if !result.IsError {
				t.Error("Expected error for invalid task parameter")
			}
			if !strings.Contains(result.ForLLM, "task is required") {
				t.Errorf("Error message should mention 'task is required', got: %s", result.ForLLM)
			}
		})
	}
}

func TestSpawnTool_Execute_ValidTask(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test")
	tool := NewSpawnTool(manager)
	spawner := &mockSpawner{done: make(chan struct{})}
	tool.SetSpawner(spawner)

	ctx := context.Background()
	args := map[string]any{
		"task":     "Write a haiku about coding",
		"label":    "haiku-task",
		"agent_id": "research",
	}

	result := tool.Execute(ctx, args)
	if result == nil {
		t.Fatal("Result should not be nil")
	}
	if result.IsError {
		t.Errorf("Expected success for valid task, got error: %s", result.ForLLM)
	}
	if !result.Async {
		t.Error("SpawnTool should return async result")
	}
	<-spawner.done
	if spawner.lastConfig.TargetAgentID != "research" {
		t.Errorf("TargetAgentID = %q, want research", spawner.lastConfig.TargetAgentID)
	}
	if !spawner.lastConfig.Critical {
		t.Error("SpawnTool should mark background subturns as critical")
	}
}

func TestSpawnTool_Execute_NilManager(t *testing.T) {
	tool := NewSpawnTool(nil)

	ctx := context.Background()
	args := map[string]any{"task": "test task"}

	result := tool.Execute(ctx, args)
	if !result.IsError {
		t.Error("Expected error for nil manager")
	}
	if !strings.Contains(result.ForLLM, "Subagent manager not configured") {
		t.Errorf("Error message should mention manager not configured, got: %s", result.ForLLM)
	}
}

// TestSubagentPromptText pins the exact system prompt handed to the sub-turn
// spawner by both spawning tools, so the shared builder keeps the wording.
func TestSubagentPromptText(t *testing.T) {
	const task = "write the report"

	tests := []struct {
		name     string
		label    string
		expected string
	}{
		{
			name: "without label",
			expected: "You are a spawned subagent running in the background. " +
				"Complete the given task independently and report back when done.\n\nTask: " + task,
		},
		{
			name:  "with label",
			label: "report-task",
			expected: "You are a spawned subagent labeled \"report-task\" running in the background. " +
				"Complete the given task independently and report back when done.\n\nTask: " + task,
		},
	}

	for _, tt := range tests {
		t.Run("spawn "+tt.name, func(t *testing.T) {
			spawner := &mockSpawner{done: make(chan struct{})}
			tool := NewSpawnTool(nil)
			tool.SetSpawner(spawner)

			args := map[string]any{"task": task}
			if tt.label != "" {
				args["label"] = tt.label
			}
			if result := tool.Execute(context.Background(), args); result == nil || result.IsError {
				t.Fatalf("spawn failed: %v", result)
			}

			select {
			case <-spawner.done:
			case <-time.After(5 * time.Second):
				t.Fatal("the spawned subagent never ran")
			}
			if spawner.lastConfig.SystemPrompt != tt.expected {
				t.Errorf("system prompt =\n%q\nwant\n%q", spawner.lastConfig.SystemPrompt, tt.expected)
			}
		})
	}

	subagentTests := []struct {
		name     string
		label    string
		expected string
	}{
		{
			name: "without label",
			expected: "You are a subagent. " +
				"Complete the given task independently and provide a clear, concise result.\n\nTask: " + task,
		},
		{
			name:  "with label",
			label: "report-task",
			expected: "You are a subagent labeled \"report-task\". " +
				"Complete the given task independently and provide a clear, concise result.\n\nTask: " + task,
		},
	}

	for _, tt := range subagentTests {
		t.Run("subagent "+tt.name, func(t *testing.T) {
			spawner := &mockSpawner{}
			tool := NewSubagentTool(nil)
			tool.SetSpawner(spawner)

			args := map[string]any{"task": task}
			if tt.label != "" {
				args["label"] = tt.label
			}
			tool.Execute(context.Background(), args)

			if spawner.lastConfig.SystemPrompt != tt.expected {
				t.Errorf("system prompt =\n%q\nwant\n%q", spawner.lastConfig.SystemPrompt, tt.expected)
			}
		})
// panickingSpawner panics inside SpawnSubTurn to exercise goroutine recovery.
type panickingSpawner struct{}

func (m *panickingSpawner) SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error) {
	panic("spawner exploded")
}

func TestSpawnTool_ExecuteAsync_PanicRecovered(t *testing.T) {
	tool := NewSpawnTool(nil)
	tool.SetSpawner(&panickingSpawner{})

	results := make(chan *ToolResult, 1)
	ack := tool.ExecuteAsync(
		context.Background(),
		map[string]any{"task": "do work"},
		func(_ context.Context, result *ToolResult) { results <- result },
	)

	if ack == nil || ack.IsError {
		t.Fatalf("spawn should be acknowledged, got: %+v", ack)
	}
	if !ack.Async {
		t.Error("SpawnTool should return an async result")
	}

	select {
	case result := <-results:
		if result == nil || !result.IsError {
			t.Fatalf("panic should surface as an error result, got: %+v", result)
		}
		if !strings.Contains(result.ForLLM, "spawn failed unexpectedly") {
			t.Errorf("unexpected error message: %s", result.ForLLM)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback was not invoked after spawner panic")
	}
}

func TestSpawnTool_ExecuteAsync_NilSpawnerResult(t *testing.T) {
	tool := NewSpawnTool(nil)
	tool.SetSpawner(&nilResultSpawner{})
	results := make(chan *ToolResult, 1)
	tool.ExecuteAsync(
		context.Background(),
		map[string]any{"task": "do work"},
		func(_ context.Context, result *ToolResult) { results <- result },
	)

	select {
	case result := <-results:
		if result == nil || !result.IsError {
			t.Fatalf("nil spawner result should surface as an error, got: %+v", result)
		}
		if !strings.Contains(result.ForLLM, "spawn returned no result") {
			t.Errorf("unexpected error message: %s", result.ForLLM)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback was not invoked for nil spawner result")
	}
}
