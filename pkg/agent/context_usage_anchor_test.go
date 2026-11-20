// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/seahorse"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type usageAnchorTestProvider struct{}

func (usageAnchorTestProvider) Chat(
	context.Context,
	[]providers.Message,
	[]providers.ToolDefinition,
	string,
	map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{}, nil
}

func (usageAnchorTestProvider) GetDefaultModel() string { return "anchor-test-model" }

func newUsageAnchorTestPipeline() (*Pipeline, *turnState, *turnExecution, *recordingContextManager) {
	agent := &AgentInstance{
		ID:                    "agent-a",
		Provider:              usageAnchorTestProvider{},
		ContextWindow:         10_000,
		MaxTokens:             0,
		SummarizeTokenPercent: 50,
		Tools:                 tools.NewToolRegistry(),
	}
	al := &AgentLoop{usageAnchors: newUsageAnchorStore()}
	ts := &turnState{
		agent:      agent,
		sessionKey: "anchor-session",
		opts:       processOptions{EnableSummary: true},
	}
	exec := &turnExecution{
		activeProvider: agent.Provider,
		llmModelName:   "anchor-test-model",
	}
	cm := &recordingContextManager{}
	return &Pipeline{al: al, ContextManager: cm}, ts, exec, cm
}

func recordTestAnchor(t *testing.T, p *Pipeline, ts *turnState, prompt []providers.Message, promptTokens int) {
	t.Helper()
	anchor := usageAnchor{
		promptTokens:   promptTokens,
		promptMessages: len(prompt),
		prefixHash:     promptPrefixHash(prompt, len(prompt)),
		overheadTokens: promptOverheadTokens(ts.agent.Provider, prompt, nil),
		historyTokens:  promptHistoryTokens(ts.agent.Provider, prompt),
		model:          "anchor-test-model",
	}
	p.al.usageAnchors.record(
		usageAnchorKey{agentID: ts.agent.ID, sessionKey: ts.sessionKey},
		anchor,
	)
}

// The system prompt changes every turn (time, session, summary), so the anchor
// must not be invalidated by it: the prefix fingerprint skips the system
// message and the estimate swaps the old envelope for the current one.
func TestUsageAnchorSurvivesSystemPromptChanges(t *testing.T) {
	p, ts, exec, _ := newUsageAnchorTestPipeline()
	recorded := []providers.Message{
		{Role: "system", Content: "old dynamic context"},
		{Role: "user", Content: "question"},
	}
	recordTestAnchor(t, p, ts, recorded, 1_500)

	extended := []providers.Message{
		{Role: "system", Content: "new dynamic context with a different size"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
	}
	exec.messages = extended

	tokens, ok := p.anchoredPromptTokens(ts, exec, 0)
	if !ok {
		t.Fatal("anchor was rejected because the system prompt changed")
	}
	want := 1_500 -
		promptOverheadTokens(ts.agent.Provider, recorded, nil) +
		promptOverheadTokens(ts.agent.Provider, extended, nil) +
		estimateMessageTokensForProvider(ts.agent.Provider, extended[2])
	if tokens != want {
		t.Fatalf("anchored prompt = %d, want %d", tokens, want)
	}
}

// A provider that reports a prompt far above the heuristic must trigger
// compaction: the real prompt size is what fills the window.
func TestCompactBeforeLLMUsesUsageAnchorOverHeuristic(t *testing.T) {
	p, ts, exec, cm := newUsageAnchorTestPipeline()
	prompt := []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "short question"},
	}
	recordTestAnchor(t, p, ts, prompt, 9_000)
	exec.messages = append(
		append([]providers.Message(nil), prompt...),
		providers.Message{Role: "assistant", Content: "working"},
	)

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1 from the provider-reported prompt size", cm.compactCalls)
	}
}

// The same prompt without provider usage stays below the threshold: the
// heuristic fallback is still the behavior when no anchor exists.
func TestCompactBeforeLLMUsesHeuristicWithoutUsageAnchor(t *testing.T) {
	p, ts, exec, cm := newUsageAnchorTestPipeline()
	exec.messages = []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "short question"},
		{Role: "assistant", Content: "working"},
	}

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 0 {
		t.Fatalf("compact calls = %d, want 0 without provider usage", cm.compactCalls)
	}
}

// Real usage also wins when it is lower than the heuristic: char counting
// overestimates some prompts, and compaction should not run early.
func TestCompactBeforeLLMTrustsRealUsageBelowHeuristic(t *testing.T) {
	p, ts, exec, cm := newUsageAnchorTestPipeline()
	ts.agent.ContextWindow = 2_000 // threshold 1000
	prompt := []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: strings.Repeat("x", 6_000)}, // ≈2400 heuristic tokens
	}
	recordTestAnchor(t, p, ts, prompt, 900)
	exec.messages = prompt

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 0 {
		t.Fatalf(
			"compact calls = %d, want 0 when the provider reports 900 tokens below the 1000 threshold",
			cm.compactCalls,
		)
	}
}

// Only the messages appended after the anchored call are estimated; the
// anchored prompt itself is reused as-is.
func TestCompactBeforeLLMAddsDeltaToUsageAnchor(t *testing.T) {
	p, ts, exec, cm := newUsageAnchorTestPipeline()
	ts.agent.ContextWindow = 4_000 // threshold 2000
	prompt := []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "short question"},
	}
	recordTestAnchor(t, p, ts, prompt, 1_500)
	exec.messages = append(
		append([]providers.Message(nil), prompt...),
		providers.Message{Role: "tool", Content: strings.Repeat("result ", 1_000)},
	)

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1 once the appended message crosses the threshold", cm.compactCalls)
	}
}

// A rebuilt prompt (compaction, eviction, steering) invalidates the anchor and
// falls back to the heuristic instead of applying a stale prefix.
func TestCompactBeforeLLMFallsBackWhenPromptPrefixChanged(t *testing.T) {
	p, ts, exec, cm := newUsageAnchorTestPipeline()
	prompt := []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "short question"},
	}
	recordTestAnchor(t, p, ts, prompt, 9_000)
	exec.messages = []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "a different question with another length"},
		{Role: "assistant", Content: "working"},
	}

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 0 {
		t.Fatalf("compact calls = %d, want 0 after the anchored prefix changed", cm.compactCalls)
	}
}

// Usage from another model has a different tokenizer and must not be reused.
func TestCompactBeforeLLMFallsBackOnModelChange(t *testing.T) {
	p, ts, exec, cm := newUsageAnchorTestPipeline()
	prompt := []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "short question"},
	}
	recordTestAnchor(t, p, ts, prompt, 9_000)
	exec.llmModelName = "another-model"
	exec.messages = prompt

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 0 {
		t.Fatalf("compact calls = %d, want 0 after the model changed", cm.compactCalls)
	}
}

func TestUsageAnchorStoreEvictsLeastRecentlyUsed(t *testing.T) {
	store := newUsageAnchorStore()
	key := func(i int) usageAnchorKey {
		return usageAnchorKey{agentID: "agent-a", sessionKey: fmt.Sprintf("session-%d", i)}
	}
	base := time.Unix(1_700_000_000, 0)
	store.mu.Lock()
	for i := 0; i < maxUsageAnchors; i++ {
		store.entries[key(i)] = usageAnchor{
			promptTokens: i + 1,
			lastUsed:     base.Add(time.Duration(i) * time.Second),
		}
	}
	store.mu.Unlock()

	// Refresh the oldest entry, then insert one more: the second-oldest must go.
	store.load(key(0))
	store.record(key(maxUsageAnchors), usageAnchor{promptTokens: 1})

	if _, ok := store.load(key(0)); !ok {
		t.Fatal("recently used anchor was evicted")
	}
	if _, ok := store.load(key(1)); ok {
		t.Fatal("least recently used anchor survived eviction")
	}
	if _, ok := store.load(key(maxUsageAnchors)); !ok {
		t.Fatal("new anchor was not stored")
	}
}

func TestUsageAnchorStoreClear(t *testing.T) {
	store := newUsageAnchorStore()
	key := usageAnchorKey{agentID: "agent-a", sessionKey: "session"}
	store.record(key, usageAnchor{promptTokens: 10})

	store.clear(key)

	if _, ok := store.load(key); ok {
		t.Fatal("anchor survived Clear")
	}
}

func TestUsageAnchorCalibrationRatio(t *testing.T) {
	tests := []struct {
		name   string
		anchor usageAnchor
		want   float64
	}{
		{
			name:   "no history",
			anchor: usageAnchor{promptTokens: 100, overheadTokens: 10},
			want:   1,
		},
		{
			name:   "code doubles the heuristic",
			anchor: usageAnchor{promptTokens: 210, overheadTokens: 10, historyTokens: 100},
			want:   2,
		},
		{
			name:   "provider report below overhead",
			anchor: usageAnchor{promptTokens: 10, overheadTokens: 20, historyTokens: 100},
			want:   1,
		},
		{
			name:   "implausible ratio is ignored",
			anchor: usageAnchor{promptTokens: 10_000, overheadTokens: 0, historyTokens: 10},
			want:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.anchor.calibrationRatio(); got != tt.want {
				t.Fatalf("calibrationRatio() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScaleHeuristicBudget(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		ratio  float64
		want   int
	}{
		{name: "no calibration", budget: 1000, ratio: 1, want: 1000},
		{name: "real tokens larger", budget: 1000, ratio: 2, want: 500},
		{name: "real tokens smaller", budget: 1000, ratio: 0.5, want: 2000},
		{name: "never zero", budget: 1, ratio: 8, want: 1},
		{name: "non-positive budget", budget: 0, ratio: 2, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scaleHeuristicBudget(tt.budget, tt.ratio); got != tt.want {
				t.Fatalf("scaleHeuristicBudget(%d, %v) = %d, want %d", tt.budget, tt.ratio, got, tt.want)
			}
		})
	}
}

func TestCalibrateHeuristicTokens(t *testing.T) {
	if got := calibrateHeuristicTokens(100, 2.5); got != 250 {
		t.Fatalf("calibrateHeuristicTokens(100, 2.5) = %d, want 250", got)
	}
	if got := calibrateHeuristicTokens(100, 1); got != 100 {
		t.Fatalf("calibrateHeuristicTokens without calibration = %d, want 100", got)
	}
	if got := calibrateHeuristicTokens(0, 3); got != 0 {
		t.Fatalf("calibrateHeuristicTokens(0, 3) = %d, want 0", got)
	}
}

// Seahorse reports stored-context size in heuristic units. The anchor's
// real/heuristic ratio must be applied so code-heavy sessions stop
// undercounting, both in /context and in compaction triggers.
func TestSeahorseContextTokensUsesUsageCalibration(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				ModelName: "test-model",
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), usageAnchorTestProvider{})
	defer al.Close()
	agent := al.registry.GetDefaultAgent()

	engine, err := seahorse.NewEngine(
		seahorse.Config{DBPath: filepath.Join(t.TempDir(), "sessions", "seahorse.db")},
		nil,
	)
	if err != nil {
		t.Fatalf("seahorse.NewEngine: %v", err)
	}
	mgr := &seahorseContextManager{engine: engine, sessions: agent.Sessions, al: al}
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	sessionKey := "agent:" + agent.ID + ":calibration"
	if err = mgr.Ingest(ctx, &IngestRequest{
		SessionKey: sessionKey,
		Message:    providers.Message{Role: "user", Content: strings.Repeat("x", 200)},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	raw, err := engine.ContextTokens(ctx, sessionKey)
	if err != nil {
		t.Fatalf("engine.ContextTokens: %v", err)
	}
	if raw <= 0 {
		t.Fatal("expected a positive heuristic context size")
	}

	// 250 real tokens over 100 heuristic tokens: a 2.5x session.
	al.usageAnchors.record(
		usageAnchorKey{agentID: agent.ID, sessionKey: sessionKey},
		usageAnchor{promptTokens: 300, overheadTokens: 50, historyTokens: 100},
	)

	got, err := mgr.ContextTokens(ctx, sessionKey)
	if err != nil {
		t.Fatalf("ContextTokens: %v", err)
	}
	if want := calibrateHeuristicTokens(raw, 2.5); got != want {
		t.Fatalf("calibrated ContextTokens = %d, want %d (raw %d at 2.5x)", got, want, raw)
	}
}

// successfulCompactContextManager reports a compaction and an empty reassembly,
// so CompactBeforeLLM runs its post-compaction rebuild path.
type successfulCompactContextManager struct {
	compactCalls int
}

func (m *successfulCompactContextManager) Assemble(
	context.Context,
	*AssembleRequest,
) (*AssembleResponse, error) {
	return &AssembleResponse{}, nil
}

func (m *successfulCompactContextManager) Compact(context.Context, *CompactRequest) error {
	m.compactCalls++
	return nil
}

func (m *successfulCompactContextManager) Ingest(context.Context, *IngestRequest) error { return nil }

func (m *successfulCompactContextManager) Clear(context.Context, string) error { return nil }

// A compaction changes the stored history, so the next prompt must not be
// measured against the pre-compaction prompt size. The observed calibration
// ratio must survive, though: it still describes the content type.
func TestCompactBeforeLLMInvalidatesUsageAnchorBaselineAfterCompaction(t *testing.T) {
	p, ts, exec, _ := newUsageAnchorTestPipeline()
	ts.agent.Sessions = newTestSessionStore(t)
	ts.agent.ContextBuilder = NewContextBuilder(t.TempDir())
	cm := &successfulCompactContextManager{}
	p.ContextManager = cm
	p.Cfg = &config.Config{}

	prompt := []providers.Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "short question"},
	}
	anchor := usageAnchor{
		promptTokens:   9_000,
		promptMessages: len(prompt),
		prefixHash:     promptPrefixHash(prompt, len(prompt)),
		overheadTokens: 1_000,
		historyTokens:  4_000, // (9000-1000)/4000 = 2x
		model:          "anchor-test-model",
	}
	anchor.ratio = anchor.calibrationRatio()
	p.al.usageAnchors.record(
		usageAnchorKey{agentID: ts.agent.ID, sessionKey: ts.sessionKey},
		anchor,
	)
	exec.messages = prompt

	p.CompactBeforeLLM(context.Background(), ts, exec)

	if cm.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", cm.compactCalls)
	}
	key := usageAnchorKey{agentID: ts.agent.ID, sessionKey: ts.sessionKey}
	got, ok := p.al.usageAnchors.load(key)
	if !ok {
		t.Fatal("calibration entry was dropped by compaction")
	}
	if got.promptTokens != 0 || got.promptMessages != 0 {
		t.Fatalf("prompt baseline survived compaction: %+v", got)
	}
	if ratio := got.calibrationRatio(); ratio != 2 {
		t.Fatalf("calibration ratio = %v, want 2", ratio)
	}
	if ratio := p.al.usageAnchorRatioForSession(ts.sessionKey); ratio != 2 {
		t.Fatalf("session calibration ratio = %v, want 2", ratio)
	}
}

// usageReportingProvider asks for one tool round and reports a fixed prompt
// size, then answers. It separates the real-token trigger from the heuristic.
type usageReportingProvider struct {
	promptTokens int
	toolRounds   int
	requests     int
	summaryCalls int
	loopRequests [][]providers.Message
}

func (p *usageReportingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	if len(messages) == 1 && strings.HasPrefix(messages[0].Content, "Provide a concise summary") {
		p.summaryCalls++
		return &providers.LLMResponse{Content: "ANCHOR-SUMMARY"}, nil
	}
	p.requests++
	p.loopRequests = append(p.loopRequests, append([]providers.Message(nil), messages...))
	usage := &providers.UsageInfo{
		PromptTokens:     p.promptTokens,
		CompletionTokens: 10,
		TotalTokens:      p.promptTokens + 10,
	}
	if p.requests <= p.toolRounds {
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:       fmt.Sprintf("call_%d", p.requests),
				Type:     "function",
				Name:     "bulky_tool",
				Function: &providers.FunctionCall{Name: "bulky_tool", Arguments: "{}"},
			}},
			Usage: usage,
		}, nil
	}
	return &providers.LLMResponse{Content: "done", Usage: usage}, nil
}

func (p *usageReportingProvider) GetDefaultModel() string { return "usage-reporting" }

// End-to-end: the first call reports a prompt far above the heuristic, so the
// second call (mid tool loop) compacts before it is sent.
func TestUsageAnchorDrivesMidTurnCompaction(t *testing.T) {
	provider := &usageReportingProvider{promptTokens: 9_000, toolRounds: 1}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:             t.TempDir(),
				ModelName:             "test-model",
				MaxTokens:             1000,
				MaxToolIterations:     10,
				SummarizeTokenPercent: 50,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	defer al.Close()
	al.RegisterTool(&bulkyTool{size: 0})
	agent := al.registry.GetDefaultAgent()
	agent.ContextWindow = 10_000 // threshold 5000

	const sessionKey = "usage-anchor-e2e"
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, alternatingHistory(6, "seed")); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	runCompactionTurn(t, al, agent, sessionKey)

	if provider.summaryCalls == 0 {
		t.Fatal("expected compaction from the provider-reported prompt size")
	}
	if len(provider.loopRequests) != 2 {
		t.Fatalf("LLM loop calls = %d, want 2", len(provider.loopRequests))
	}
	if !requestContains(provider.loopRequests[1], "ANCHOR-SUMMARY") {
		t.Fatal("the second call must carry the summary created from the real usage anchor")
	}
}

// The same turn without usage reports stays on the heuristic and must not
// compact: the feature never invents tokens the provider did not report.
func TestUsageAnchorDoesNotCompactWithoutUsage(t *testing.T) {
	provider := &usageReportingProvider{toolRounds: 1}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:             t.TempDir(),
				ModelName:             "test-model",
				MaxTokens:             1000,
				MaxToolIterations:     10,
				SummarizeTokenPercent: 50,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	defer al.Close()
	al.RegisterTool(&bulkyTool{size: 0})
	agent := al.registry.GetDefaultAgent()
	agent.ContextWindow = 10_000

	const sessionKey = "no-usage-anchor-e2e"
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, alternatingHistory(6, "seed")); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	runCompactionTurn(t, al, agent, sessionKey)

	if provider.summaryCalls != 0 {
		t.Fatalf("summarization calls = %d, want 0 without provider usage", provider.summaryCalls)
	}
}
