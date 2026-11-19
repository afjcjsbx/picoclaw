package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// sleepyBudgetTool outlasts the run-budget warning point of the test below.
type sleepyBudgetTool struct{ toolLimitTestTool }

func (sleepyBudgetTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	time.Sleep(150 * time.Millisecond)
	return tools.SilentResult("slow result")
}

func runBudgetCfg(t *testing.T, seconds int) *config.Config {
	return &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
		Workspace:                 t.TempDir(),
		ModelName:                 "test-model",
		MaxTokens:                 4096,
		MaxToolIterations:         10,
		RunBudgetSeconds:          seconds,
		RunBudgetWarningThreshold: 0.1, // 100ms of a 1s budget
	}}}
}

func TestAgentLoop_RunBudgetNoticeRidesOnToolResultOnce(t *testing.T) {
	for _, tt := range []struct {
		name    string
		seconds int
		want    int // notices seen by the last LLM call
	}{
		{"enabled", 1, 1},
		{"disabled", 0, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := &budgetNoticeProvider{toolCalls: 3}
			al := NewAgentLoop(runBudgetCfg(t, tt.seconds), bus.NewMessageBus(), provider)
			al.RegisterTool(&sleepyBudgetTool{})
			if _, err := al.ProcessDirectWithChannel(context.Background(), "hi", "rb", "test", "chat1"); err != nil {
				t.Fatalf("ProcessDirectWithChannel: %v", err)
			}
			count := func(msgs []providers.Message) (n int) {
				for _, m := range msgs {
					if strings.Contains(m.Content, runBudgetNotice) {
						if m.Role != "tool" {
							t.Fatalf("notice in a %q message, want only a tool result", m.Role)
						}
						n++
					}
				}
				return n
			}
			if got := count(provider.msgs[0]); got != 0 {
				t.Fatalf("first call saw %d notices, want 0", got)
			}
			// Latch: the notice stays in history but is never added again.
			if got := count(provider.msgs[len(provider.msgs)-1]); got != tt.want {
				t.Fatalf("last call saw %d notices, want %d", got, tt.want)
			}
			if tt.want > 0 && provider.toolDefs[1] == 0 {
				t.Fatal("tools must stay available after the notice")
			}
		})
	}
}

func TestPruneIgnoresRunBudgetNoticeLength(t *testing.T) {
	short := strings.Repeat("v", 60)
	both := short + "\n\n" + fmt.Sprintf(iterationBudgetWarningTemplate, 3, 6) + "\n\n" + runBudgetNotice
	rev := short + "\n\n" + runBudgetNotice + "\n\n" + fmt.Sprintf(iterationBudgetWarningTemplate, 3, 6)
	history := []providers.Message{
		{Role: "tool", Content: short + "\n\n" + runBudgetNotice},
		{Role: "tool", Content: both},
		{Role: "tool", Content: rev},
	}
	if pruneToolResultsBefore(history, len(history)) {
		t.Fatalf("short results carrying notices were pruned: %+v", history)
	}
}

func TestRunBudgetDurations(t *testing.T) {
	for _, tt := range []struct {
		secs          int
		threshold     float64
		budget, after time.Duration
	}{
		{0, 0.8, 0, 0},
		{-5, 0.8, 0, 0},
		{900, 0.8, 900 * time.Second, 720 * time.Second}, // the 12-minute mark of a 15-minute run
		{900, 0, 900 * time.Second, 720 * time.Second},   // unset -> default
		{900, 1.5, 900 * time.Second, 720 * time.Second},
		{1800, 0.5, 1800 * time.Second, 900 * time.Second},
	} {
		b, a := runBudgetDurations(tt.secs, tt.threshold)
		if b != tt.budget || a != tt.after {
			t.Errorf(
				"runBudgetDurations(%d, %v) = %v, %v; want %v, %v",
				tt.secs,
				tt.threshold,
				b,
				a,
				tt.budget,
				tt.after,
			)
		}
	}
}

func TestImplicitCallTimeout(t *testing.T) {
	ts := func(budget, elapsed time.Duration) *turnState {
		return &turnState{agent: &AgentInstance{RunBudget: budget}, startedAt: time.Now().Add(-elapsed)}
	}
	near := func(got, want time.Duration) bool { return got > want-time.Second && got <= want }

	if got := ts(0, 0).implicitCallTimeout(nil); got != 0 {
		t.Errorf("no budget: %v, want 0", got)
	}
	if got := ts(900*time.Second, 100*time.Second).implicitCallTimeout(nil); !near(got, 800*time.Second) {
		t.Errorf("time left: %v, want ~800s", got)
	}
	if got := ts(900*time.Second, 890*time.Second).implicitCallTimeout(nil); got != runBudgetMinCall {
		t.Errorf("nearly spent: %v, want floor %v", got, runBudgetMinCall)
	}
	if got := ts(900*time.Second, 2000*time.Second).implicitCallTimeout(nil); got != runBudgetMinCall {
		t.Errorf("spent: %v, want floor %v", got, runBudgetMinCall)
	}
	if got := ts(900*time.Second, 0).implicitCallTimeout(&config.ModelConfig{RequestTimeout: 300}); got != 0 {
		t.Errorf("explicit request_timeout: %v, want untouched (0)", got)
	}
	sub := ts(900*time.Second, 0)
	sub.parentTurnState = &turnState{}
	if got := sub.implicitCallTimeout(nil); got != 0 {
		t.Errorf("sub-turn: %v, want 0", got)
	}
}
