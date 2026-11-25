package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// spawnOverflowProvider requests several spawns in one turn. Children sleep long
// enough that the spawn tool has to wait for a concurrency slot.
type spawnOverflowProvider struct {
	parentCalls atomic.Int32
	childCalls  atomic.Int32
	spawnCount  int
	childDelay  time.Duration
}

func (m *spawnOverflowProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	var joined strings.Builder
	for _, msg := range messages {
		joined.WriteString(msg.Content)
		joined.WriteString("\n")
	}
	if strings.Contains(joined.String(), "You are a spawned subagent") {
		m.childCalls.Add(1)
		select {
		case <-time.After(m.childDelay):
			return &providers.LLMResponse{Content: "child done"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if m.parentCalls.Add(1) == 1 {
		calls := make([]providers.ToolCall, 0, m.spawnCount)
		for i := 0; i < m.spawnCount; i++ {
			calls = append(calls, providers.ToolCall{
				ID:        fmt.Sprintf("call_%d", i),
				Type:      "function",
				Name:      "spawn",
				Arguments: map[string]any{"task": fmt.Sprintf("task-%d", i)},
			})
		}
		return &providers.LLMResponse{Content: "Spawning.", ToolCalls: calls}, nil
	}
	return &providers.LLMResponse{Content: "All done."}, nil
}

func (m *spawnOverflowProvider) GetDefaultModel() string { return "gpt-4o-mini" }

func newSpawnOverflowAgent(t *testing.T, spawnCount int) (*AgentLoop, *spawnOverflowProvider, *bus.MessageBus) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Agents.Defaults.ModelName = "gpt-4o-mini"
	cfg.Agents.Defaults.SubTurn.MaxConcurrent = 3

	provider := &spawnOverflowProvider{spawnCount: spawnCount, childDelay: 150 * time.Millisecond}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, provider)
	t.Cleanup(al.Close)
	return al, provider, msgBus
}

// TestSpawnOverflowCLIRunsAllChildren is the regression test for the reported
// spawn5 bug: five acknowledged spawns with max_concurrent=3 must all run, and
// a CLI one-shot turn must not return before its children finish.
func TestSpawnOverflowCLIRunsAllChildren(t *testing.T) {
	al, provider, _ := newSpawnOverflowAgent(t, 5)

	response, err := al.ProcessDirect(context.Background(), "spawn five agents", "cli:overflow")
	if err != nil {
		t.Fatalf("ProcessDirect: %v", err)
	}
	if got := provider.childCalls.Load(); got != 5 {
		t.Fatalf("children that ran = %d, want 5", got)
	}
	if !strings.Contains(response, "All done.") {
		t.Fatalf("response = %q, want the post-join answer", response)
	}
}

// TestSpawnOverflowGatewayRunsAllChildren covers the fire-and-forget path:
// spawn calls wait for a slot before acknowledging, so the parent turn cannot
// exit while overflow tasks are still unstarted. Results arrive as follow-ups.
func TestSpawnOverflowGatewayRunsAllChildren(t *testing.T) {
	al, provider, msgBus := newSpawnOverflowAgent(t, 5)

	if _, err := al.ProcessDirectWithChannel(
		context.Background(), "spawn five agents", "gateway:overflow", "test", "direct",
	); err != nil {
		t.Fatalf("ProcessDirectWithChannel: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && provider.childCalls.Load() < 5 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := provider.childCalls.Load(); got != 5 {
		t.Fatalf("children that ran = %d, want 5", got)
	}

	followUps := 0
	for followUps < 5 && time.Now().Before(deadline) {
		select {
		case msg := <-msgBus.OutboundChan():
			if msg.Content != "" {
				followUps++
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if followUps < 5 {
		t.Fatalf("follow-up messages = %d, want 5", followUps)
	}
}
