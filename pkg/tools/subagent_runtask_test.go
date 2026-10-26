package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSubagentManager_RunTaskNilSpawnerResult verifies that a spawner
// returning (nil, nil) becomes an explicit failed task instead of a nil
// dereference on the success path.
func TestSubagentManager_RunTaskNilSpawnerResult(t *testing.T) {
	manager := NewSubagentManager(&MockLLMProvider{}, "test-model", t.TempDir())
	manager.SetSpawner(func(
		ctx context.Context,
		task, label, agentID string,
		tools *ToolRegistry,
		maxTokens int,
		temperature float64,
		hasMaxTokens, hasTemperature bool,
	) (*ToolResult, error) {
		return nil, nil
	})

	task := &SubagentTask{
		ID:      "task-1",
		Task:    "do work",
		Status:  "running",
		Created: time.Now().UnixMilli(),
	}
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.mu.Unlock()

	var callbackResult *ToolResult
	require.NotPanics(t, func() {
		manager.runTask(context.Background(), task, func(_ context.Context, result *ToolResult) {
			callbackResult = result
		})
	})

	require.Equal(t, "failed", task.Status)
	require.Contains(t, task.Result, "no result")
	require.NotNil(t, callbackResult)
	require.True(t, callbackResult.IsError)
}

// TestSubagentManager_RunTaskStatusRace exercises task updates while another
// goroutine keeps reading the task under the manager lock: every field write
// in runTask must be synchronized with those readers.
func TestSubagentManager_RunTaskStatusRace(t *testing.T) {
	release := make(chan struct{})
	manager := NewSubagentManager(&MockLLMProvider{}, "test-model", t.TempDir())
	manager.SetSpawner(func(
		ctx context.Context,
		task, label, agentID string,
		tools *ToolRegistry,
		maxTokens int,
		temperature float64,
		hasMaxTokens, hasTemperature bool,
	) (*ToolResult, error) {
		<-release
		return NewToolResult("done"), nil
	})

	task := &SubagentTask{
		ID:      "task-1",
		Task:    "do work",
		Status:  "running",
		Created: time.Now().UnixMilli(),
	}
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.runTask(context.Background(), task, nil)
	}()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = manager.GetTaskCopy("task-1")
			}
		}
	}()

	close(release)
	<-done
	close(stop)
	wg.Wait()

	require.Equal(t, "completed", task.Status)
}
