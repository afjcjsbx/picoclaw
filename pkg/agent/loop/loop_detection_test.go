package loop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/agent/agentevents"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestHashToolCallCanonicalizesMapOrder(t *testing.T) {
	first := map[string]any{
		"query":   "picoclaw",
		"options": map[string]any{"limit": float64(10), "lang": "en"},
	}
	second := map[string]any{
		"options": map[string]any{"lang": "en", "limit": float64(10)},
		"query":   "picoclaw",
	}

	if got, want := hashToolCall("search", first), hashToolCall("search", second); got != want {
		t.Fatalf("equivalent tool calls hashed differently: %x != %x", got, want)
	}
}

func TestHashToolCallPreservesMeaningfulArgumentDifferences(t *testing.T) {
	pageOne := hashToolCall("search", map[string]any{"query": "picoclaw", "page": 1})
	pageTwo := hashToolCall("search", map[string]any{"query": "picoclaw", "page": 2})
	if pageOne == pageTwo {
		t.Fatal("different pagination calls must not be treated as identical")
	}
}

func TestTurnStateRecordToolCallThresholds(t *testing.T) {
	ts := &turnState{loopDetectionConfig: config.LoopDetectionConfig{
		Enabled:           true,
		RepeatThreshold:   3,
		CriticalThreshold: 5,
		WindowSize:        5,
	}}
	args := map[string]any{"query": "same"}

	want := []loopStatus{
		loopStatusNone,
		loopStatusNone,
		loopStatusWarning,
		loopStatusNone,
		loopStatusCritical,
	}
	for i, expected := range want {
		status, count := ts.recordToolCall("search", args)
		if status != expected {
			t.Fatalf("call %d: status = %v, want %v (count %d)", i+1, status, expected, count)
		}
	}
	if len(ts.loopDetectionHistory) != 5 {
		t.Fatalf("history length = %d, want 5", len(ts.loopDetectionHistory))
	}

	status, count := ts.recordToolCall("search", map[string]any{"query": "different"})
	if status != loopStatusNone || count != 1 {
		t.Fatalf(
			"different call did not reset consecutive count: status=%v count=%d",
			status,
			count,
		)
	}
	if len(ts.loopDetectionHistory) != 5 {
		t.Fatalf("bounded history length = %d, want 5", len(ts.loopDetectionHistory))
	}
}

func TestTurnStateRecordToolCallSkipsSpawnStatus(t *testing.T) {
	ts := &turnState{loopDetectionConfig: config.LoopDetectionConfig{
		Enabled: true, RepeatThreshold: 3, CriticalThreshold: 6, WindowSize: 20,
	}}
	args := map[string]any{"query": "same"}
	for i := 0; i < 2; i++ {
		ts.recordToolCall("search", args)
	}
	for i := 0; i < 8; i++ {
		status, count := ts.recordToolCall("spawn_status", map[string]any{"task_id": "subagent-1"})
		if status != loopStatusNone || count != 0 {
			t.Fatalf("poll %d: status=%v count=%d", i+1, status, count)
		}
	}
	if status, count := ts.recordToolCall("search", args); status != loopStatusNone || count != 1 {
		t.Fatalf("polling did not break the earlier streak: status=%v count=%d", status, count)
	}
}

type repeatedToolProvider struct {
	mu              sync.Mutex
	callCount       int
	stopOnWarning   bool
	warningObserved bool
}

func (p *repeatedToolProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "[Loop Warning]") {
			p.warningObserved = true
		}
	}
	if p.stopOnWarning && p.warningObserved {
		return &providers.LLMResponse{Content: "recovered", FinishReason: "stop"}, nil
	}
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID:        fmt.Sprintf("call_%d", p.callCount),
			Name:      "missing_test_tool",
			Arguments: map[string]any{"query": "same"},
		}},
		FinishReason: "tool_calls",
	}, nil
}

func (p *repeatedToolProvider) GetDefaultModel() string { return "repeated-tool-model" }

type warningRetryProvider struct {
	callCount              int
	warningBeforeRetrySeen bool
	warningOnRetrySeen     bool
}

func (p *warningRetryProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.callCount++
	warning := false
	for _, msg := range messages {
		if msg.Role == "system" && strings.Contains(msg.Content, "[Loop Warning]") {
			warning = true
		}
	}
	if p.callCount <= 3 {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:        fmt.Sprintf("call_%d", p.callCount),
				Name:      "missing_test_tool",
				Arguments: map[string]any{"query": "same"},
			}},
			FinishReason: "tool_calls",
		}, nil
	}
	if p.callCount == 4 {
		p.warningBeforeRetrySeen = warning
		return nil, fmt.Errorf("context_length_exceeded")
	}
	p.warningOnRetrySeen = warning
	return &providers.LLMResponse{Content: "recovered", FinishReason: "stop"}, nil
}

func (p *warningRetryProvider) GetDefaultModel() string { return "warning-retry-model" }

func TestRunTurnLoopDetectionWarningGuidesNextCall(t *testing.T) {
	provider := &repeatedToolProvider{stopOnWarning: true}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	agent.LoopDetection = config.LoopDetectionConfig{
		Enabled:           true,
		RepeatThreshold:   3,
		CriticalThreshold: 6,
		WindowSize:        20,
	}

	ts := newTurnState(agent, makeTestProcessOpts("test-loop-warning"), turnEventScope{
		turnID:  "turn-loop-warning",
		context: agentevents.NewTurnContext(nil, nil, nil),
	})
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}
	if result.finalContent != "recovered" {
		t.Fatalf("final content = %q, want recovered", result.finalContent)
	}
	if !provider.warningObserved {
		t.Fatal("provider did not receive the loop warning")
	}
	if provider.callCount != 4 {
		t.Fatalf("provider calls = %d, want 4", provider.callCount)
	}

	for _, msg := range agent.Sessions.GetHistory(ts.sessionKey) {
		if strings.Contains(msg.Content, "[Loop Warning]") {
			t.Fatal("loop warning must not persist in session history")
		}
	}
}

func TestRunTurnLoopDetectionWarningSurvivesContextRetry(t *testing.T) {
	provider := &warningRetryProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	agent.LoopDetection = config.LoopDetectionConfig{
		Enabled: true, RepeatThreshold: 3, CriticalThreshold: 6, WindowSize: 20,
	}

	ts := newTurnState(
		agent,
		makeTestProcessOpts("test-loop-warning-context-retry"),
		turnEventScope{
			turnID: "turn-loop-warning-context-retry", context: agentevents.NewTurnContext(nil, nil, nil),
		},
	)
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}
	if result.finalContent != "recovered" || provider.callCount != 5 {
		t.Fatalf("result=%q provider calls=%d", result.finalContent, provider.callCount)
	}
	if !provider.warningBeforeRetrySeen || !provider.warningOnRetrySeen {
		t.Fatalf(
			"warning before retry=%t, after retry=%t",
			provider.warningBeforeRetrySeen,
			provider.warningOnRetrySeen,
		)
	}
	for _, msg := range agent.Sessions.GetHistory(ts.sessionKey) {
		if strings.Contains(msg.Content, "[Loop Warning]") {
			t.Fatal("loop warning must not persist in session history")
		}
	}
}

func TestRunTurnLoopDetectionCriticalStopsCleanly(t *testing.T) {
	provider := &repeatedToolProvider{}
	al, agent, cleanup := newTurnCoordTestLoop(t, provider)
	defer cleanup()
	agent.LoopDetection = config.LoopDetectionConfig{
		Enabled:           true,
		RepeatThreshold:   2,
		CriticalThreshold: 3,
		WindowSize:        5,
	}

	ts := newTurnState(agent, makeTestProcessOpts("test-loop-critical"), turnEventScope{
		turnID:  "turn-loop-critical",
		context: agentevents.NewTurnContext(nil, nil, nil),
	})
	result, err := al.runTurn(context.Background(), ts, NewPipeline(al))
	if err != nil {
		t.Fatalf("runTurn failed: %v", err)
	}
	if result.status != agentevents.TurnEndStatusCompleted {
		t.Fatalf("status = %v, want completed", result.status)
	}
	if result.finalContent != loopDetectionResponse {
		t.Fatalf("final content = %q, want %q", result.finalContent, loopDetectionResponse)
	}
	if provider.callCount != 3 {
		t.Fatalf("provider calls = %d, want 3", provider.callCount)
	}

	history := agent.Sessions.GetHistory(ts.sessionKey)
	if len(history) == 0 || history[len(history)-1].Content != loopDetectionResponse {
		t.Fatal("critical stop response was not persisted")
	}
}
