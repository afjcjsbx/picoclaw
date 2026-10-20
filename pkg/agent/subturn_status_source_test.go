package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// TestNewSubTurnSourceReportsRunningSubTurns verifies that spawn_status's live
// source maps the agent turn registry to subagent tasks for the calling
// conversation only.
func TestNewSubTurnSourceReportsRunningSubTurns(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				ModelName: "gpt-4o-mini",
				Provider:  "mock",
			},
		},
	}
	al := NewAgentLoop(cfg, nil, &simpleMockProviderAPI{response: "ok"})

	started := time.Unix(1700000000, 0)
	al.activeTurnStates.Store("sess-1", &turnState{
		turnID:       "root",
		channel:      "telegram",
		chatID:       "chat-A",
		childTurnIDs: []string{"subturn-1"},
	})
	al.activeTurnStates.Store("subturn-1", &turnState{
		turnID:       "subturn-1",
		parentTurnID: "root",
		depth:        1,
		agentID:      "main",
		channel:      "telegram",
		chatID:       "chat-A",
		userMessage:  "summarize page",
		phase:        TurnPhaseRunning,
		startedAt:    started,
	})
	defer func() {
		al.activeTurnStates.Delete("sess-1")
		al.activeTurnStates.Delete("subturn-1")
	}()

	source := newSubTurnSource(al)
	ctx := tools.WithToolContext(context.Background(), "telegram", "chat-A")
	ctx = tools.WithToolSessionContext(ctx, "main", "sess-1", nil)

	tasks := source(ctx)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (the root turn must be skipped)", len(tasks))
	}
	task := tasks[0]
	if task.ID != "subturn-1" {
		t.Errorf("ID = %q, want subturn-1", task.ID)
	}
	if task.Status != "running" {
		t.Errorf("Status = %q, want running", task.Status)
	}
	if task.Task != "summarize page" {
		t.Errorf("Task = %q, want the sub-turn message", task.Task)
	}
	if task.OriginChannel != "telegram" || task.OriginChatID != "chat-A" {
		t.Errorf("origin = %s/%s, want telegram/chat-A", task.OriginChannel, task.OriginChatID)
	}
	if task.Created != started.UnixMilli() {
		t.Errorf("Created = %d, want %d", task.Created, started.UnixMilli())
	}

	if got := source(context.Background()); got != nil {
		t.Errorf("source without a session key = %v, want nil", got)
	}
}
