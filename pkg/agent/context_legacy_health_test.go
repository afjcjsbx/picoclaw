// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func newCompactionHealthTestManager(
	t *testing.T,
) (*AgentLoop, *AgentInstance, *legacyContextManager, *time.Time) {
	t.Helper()
	al := newCMTestAgentLoop(testConfig(t))
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	lcm, ok := al.contextManager.(*legacyContextManager)
	if !ok {
		t.Fatalf("context manager = %T, want *legacyContextManager", al.contextManager)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	lcm.now = func() time.Time { return now }
	return al, agent, lcm, &now
}

func TestLegacyCompactionHealth_TripsAfterTwoIneffective(t *testing.T) {
	_, agent, lcm, _ := newCompactionHealthTestManager(t)
	const sessionKey = "session-ineffective"

	for i := 0; i < maxIneffectiveCompactions; i++ {
		lcm.RecordCompactionHealth(context.Background(), sessionKey, CompactionHealthOutcome{
			AgentID:      agent.ID,
			Attempted:    true,
			Effective:    false,
			PromptTokens: 900,
			Threshold:    500,
		})
	}

	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("err = %v, want ErrCompactionThrottled", err)
	}
	if !strings.Contains(err.Error(), "did not reduce the prompt") {
		t.Fatalf("throttle reason = %q, want the ineffective-compaction reason", err.Error())
	}

	// Manual /compact is the operator's escape hatch: it must run and reset
	// the breaker for later automatic attempts.
	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonManual,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("manual compact was throttled: %v", err)
	}
	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("manual compact did not reset the breaker: %v", err)
	}
}

func TestLegacyCompactionHealth_BelowThresholdClearsBreaker(t *testing.T) {
	_, agent, lcm, _ := newCompactionHealthTestManager(t)
	const sessionKey = "session-below-threshold"

	for i := 0; i < maxIneffectiveCompactions; i++ {
		lcm.RecordCompactionHealth(context.Background(), sessionKey, CompactionHealthOutcome{
			AgentID:      agent.ID,
			Attempted:    true,
			Effective:    false,
			PromptTokens: 900,
			Threshold:    500,
		})
	}

	// The next prompt is already below the threshold: no compaction runs, but
	// the stale breaker must be cleared.
	lcm.RecordCompactionHealth(context.Background(), sessionKey, CompactionHealthOutcome{
		AgentID:   agent.ID,
		Attempted: false,
		Effective: true,
	})

	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("breaker was not cleared below threshold: %v", err)
	}
}

func TestLegacyCompactionHealth_CooldownEscalates(t *testing.T) {
	_, _, lcm, now := newCompactionHealthTestManager(t)
	const sessionKey = "session-cooldown"

	// No history: an over-threshold automatic attempt makes no progress, so it
	// becomes a failed attempt and arms the first cooldown.
	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("first attempt err = %v, want ErrNothingToCompact", err)
	}

	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrCompactionThrottled) || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("second attempt err = %v, want a cooldown throttle", err)
	}

	*now = now.Add(compactionCooldownFirst + time.Second)
	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("cooldown did not expire: %v", err)
	}
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("post-cooldown err = %v, want ErrNothingToCompact", err)
	}

	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("third attempt err = %v, want the escalated cooldown", err)
	}

	*now = now.Add(compactionCooldownSecond + time.Second)
	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("second cooldown did not expire: %v", err)
	}
}

func TestLegacyCompactionHealth_EffectiveClearsFailureCooldown(t *testing.T) {
	_, agent, lcm, _ := newCompactionHealthTestManager(t)
	const sessionKey = "session-effective"

	// A no-progress attempt arms the failure cooldown.
	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("failing attempt err = %v, want ErrNothingToCompact", err)
	}

	lcm.RecordCompactionHealth(context.Background(), sessionKey, CompactionHealthOutcome{
		AgentID:   agent.ID,
		Attempted: true,
		Effective: true,
	})

	err = lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("effective compaction did not clear the failure cooldown: %v", err)
	}
}

func TestLegacyCompactionHealth_HardBudgetBypassesThrottle(t *testing.T) {
	_, agent, lcm, _ := newCompactionHealthTestManager(t)
	const sessionKey = "session-hard-budget"

	for i := 0; i < maxIneffectiveCompactions; i++ {
		lcm.RecordCompactionHealth(context.Background(), sessionKey, CompactionHealthOutcome{
			AgentID:   agent.ID,
			Attempted: true,
			Effective: false,
		})
	}

	// A provider overflow must still be able to recover even while the soft
	// threshold is throttled.
	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonProactive,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("hard-budget compaction was throttled: %v", err)
	}
}

func TestLegacyCompactionHealth_ClearResets(t *testing.T) {
	_, agent, lcm, _ := newCompactionHealthTestManager(t)
	const sessionKey = "session-clear-health"

	for i := 0; i < maxIneffectiveCompactions; i++ {
		lcm.RecordCompactionHealth(context.Background(), sessionKey, CompactionHealthOutcome{
			AgentID:   agent.ID,
			Attempted: true,
			Effective: false,
		})
	}

	if err := lcm.Clear(context.Background(), sessionKey); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: sessionKey,
		Reason:     ContextCompressReasonThreshold,
	})
	if errors.Is(err, ErrCompactionThrottled) {
		t.Fatalf("Clear did not reset compaction health: %v", err)
	}
}

// healthTestContextManager is a ContextManager that also reports health, so a
// CompactBeforeLLM test can observe the measured outcome without an LLM call.
type healthTestContextManager struct {
	history      []providers.Message
	summary      string
	compactCalls int
	outcomes     []CompactionHealthOutcome
	onCompact    func()
}

func (m *healthTestContextManager) Assemble(context.Context, *AssembleRequest) (*AssembleResponse, error) {
	return &AssembleResponse{History: m.history, Summary: m.summary}, nil
}

func (m *healthTestContextManager) Compact(context.Context, *CompactRequest) error {
	m.compactCalls++
	if m.onCompact != nil {
		m.onCompact()
	}
	return nil
}

func (m *healthTestContextManager) Ingest(context.Context, *IngestRequest) error { return nil }
func (m *healthTestContextManager) Clear(context.Context, string) error          { return nil }

func (m *healthTestContextManager) RecordCompactionHealth(
	_ context.Context,
	_ string,
	outcome CompactionHealthOutcome,
) {
	m.outcomes = append(m.outcomes, outcome)
}

func newCompactBeforeLLMHealthTest(
	t *testing.T,
	cm *healthTestContextManager,
	longMessage providers.Message,
) (*Pipeline, *turnState, *turnExecution) {
	t.Helper()
	al := newCMTestAgentLoop(testConfig(t))
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	agent.ContextWindow = 50_000
	agent.SummarizeTokenPercent = 50

	cm.history = []providers.Message{longMessage}
	ts := &turnState{
		agent:      agent,
		opts:       processOptions{EnableSummary: true},
		sessionKey: "session-compact-health",
	}
	exec := &turnExecution{
		messages:       []providers.Message{longMessage},
		activeProvider: agent.Provider,
	}
	return &Pipeline{ContextManager: cm, Cfg: al.cfg}, ts, exec
}

func TestCompactBeforeLLMReportsIneffectiveCompaction(t *testing.T) {
	longMessage := providers.Message{Role: "user", Content: strings.Repeat("context ", 12_000)}
	cm := &healthTestContextManager{}
	pipeline, ts, exec := newCompactBeforeLLMHealthTest(t, cm, longMessage)

	pipeline.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != maxCompactionPasses {
		t.Fatalf("compact calls = %d, want %d", cm.compactCalls, maxCompactionPasses)
	}
	if len(cm.outcomes) != 1 {
		t.Fatalf("health outcomes = %d, want 1", len(cm.outcomes))
	}
	outcome := cm.outcomes[0]
	if !outcome.Attempted || outcome.Effective {
		t.Fatalf("outcome = %+v, want an attempted ineffective compaction", outcome)
	}
	if outcome.PromptTokens < outcome.Threshold {
		t.Fatalf("prompt tokens = %d, threshold = %d; want still over threshold",
			outcome.PromptTokens, outcome.Threshold)
	}
}

func TestCompactBeforeLLMReportsEffectiveCompaction(t *testing.T) {
	longMessage := providers.Message{Role: "user", Content: strings.Repeat("context ", 12_000)}
	cm := &healthTestContextManager{}
	cm.onCompact = func() { cm.history = nil }
	pipeline, ts, exec := newCompactBeforeLLMHealthTest(t, cm, longMessage)

	pipeline.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", cm.compactCalls)
	}
	if len(cm.outcomes) != 1 {
		t.Fatalf("health outcomes = %d, want 1", len(cm.outcomes))
	}
	outcome := cm.outcomes[0]
	if !outcome.Attempted || !outcome.Effective {
		t.Fatalf("outcome = %+v, want an attempted effective compaction", outcome)
	}
	if outcome.PromptTokens >= outcome.Threshold {
		t.Fatalf("prompt tokens = %d, threshold = %d; want below threshold",
			outcome.PromptTokens, outcome.Threshold)
	}
}
