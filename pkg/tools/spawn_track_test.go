package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

type recTracker struct{ got chan *ToolResult }

func (r *recTracker) Begin() func(*ToolResult) {
	return func(res *ToolResult) { r.got <- res }
}

func TestSpawnTool_TrackedAckAndAttribution(t *testing.T) {
	tool := NewSpawnTool(NewSubagentManager(&MockLLMProvider{}, "m", "/tmp/test"))
	tool.SetSpawner(&mockSpawner{})
	tr := &recTracker{got: make(chan *ToolResult, 1)}

	res := tool.Execute(
		WithChildTracker(context.Background(), tr),
		map[string]any{"task": "t", "label": "pricing", "agent_id": "researcher"},
	)
	if !strings.Contains(res.ForLLM, "before you finish") {
		t.Errorf("tracked ack should say the result is delivered before finishing, got: %s", res.ForLLM)
	}

	select {
	case r := <-tr.got:
		if want := `[Result from subagent "pricing" (agent researcher)]`; !strings.HasPrefix(r.ForLLM, want) {
			t.Errorf("result should start with %q, got: %s", want, r.ForLLM)
		}
	case <-time.After(time.Second):
		t.Fatal("tracker never received the child result")
	}
}

type sharedResultSpawner struct{ res *ToolResult }

func (s *sharedResultSpawner) SpawnSubTurn(context.Context, SubTurnConfig) (*ToolResult, error) {
	return s.res, nil
}

// With Async=true the real spawner has already handed the same *ToolResult to
// the parent's pendingResults, so the attribution prefix must not mutate it.
func TestSpawnTool_AttributionDoesNotMutateSharedResult(t *testing.T) {
	shared := &ToolResult{ForLLM: "body"}
	tool := NewSpawnTool(NewSubagentManager(&MockLLMProvider{}, "m", "/tmp/test"))
	tool.SetSpawner(&sharedResultSpawner{res: shared})

	got := make(chan *ToolResult, 1)
	tool.ExecuteAsync(context.Background(), map[string]any{"task": "t", "label": "x"},
		func(_ context.Context, r *ToolResult) { got <- r })

	select {
	case r := <-got:
		if !strings.HasPrefix(r.ForLLM, `[Result from subagent "x"]`) {
			t.Errorf("callback result should be attributed, got: %s", r.ForLLM)
		}
	case <-time.After(time.Second):
		t.Fatal("callback never ran")
	}
	if shared.ForLLM != "body" {
		t.Errorf("shared result was mutated: %q", shared.ForLLM)
	}
}
