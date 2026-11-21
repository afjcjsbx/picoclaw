package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestAutoTailTokenBudget(t *testing.T) {
	tests := []struct {
		name          string
		contextWindow int
		maxTokens     int
		want          int
	}{
		{name: "large window", contextWindow: 200_000, maxTokens: 20_000, want: 18_000},
		{name: "128k window", contextWindow: 128_000, maxTokens: 32_000, want: 9_600},
		{name: "floor", contextWindow: 16_000, maxTokens: 4_000, want: 2_000},
		{name: "half of tiny window", contextWindow: 4_000, maxTokens: 3_000, want: 500},
		{name: "no window", contextWindow: 0, maxTokens: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoTailTokenBudget(tt.contextWindow, tt.maxTokens); got != tt.want {
				t.Fatalf("autoTailTokenBudget(%d, %d) = %d, want %d",
					tt.contextWindow, tt.maxTokens, got, tt.want)
			}
		})
	}
}

func TestComputeTailStart_TokenBudgetExtendsBeyondMessageFloor(t *testing.T) {
	history := make([]providers.Message, 10)
	for i := range history {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		history[i] = providers.Message{Role: role, Content: strings.Repeat("x", 500)}
	}
	cfg := contextProtection{
		protectFirstN:       1,
		protectLastN:        2,
		minTailUserMessages: 0,
		tailTokenBudget:     1_000,
	}

	got := computeTailStart(history, len(history), 0, cfg, nil)
	if got != 5 {
		t.Fatalf("tail start = %d, want 5 (two-message floor extended by the token budget)", got)
	}
}

func TestComputeTailStart_MinTailUserMessages(t *testing.T) {
	history := make([]providers.Message, 8)
	for i := range history {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		history[i] = providers.Message{Role: role, Content: "x"}
	}
	cfg := contextProtection{
		protectLastN:        1,
		minTailUserMessages: 2,
	}

	got := computeTailStart(history, len(history), 0, cfg, nil)
	if got != 4 {
		t.Fatalf("tail start = %d, want 4 (the last two real user messages)", got)
	}
}

func TestComputeHistoryProtection_ActiveTurnDoesNotOverProtectHistory(t *testing.T) {
	history := make([]providers.Message, 12)
	for i := range history {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		history[i] = providers.Message{Role: role, Content: "x"}
	}
	agent := &AgentInstance{
		ProtectFirstN:       1,
		ProtectLastN:        4,
		MinTailUserMessages: 0,
	}
	got := computeHistoryProtection(history, 6, agent)
	if !got.ok {
		t.Fatal("expected a compressible middle")
	}
	if got.tailStart != 6 {
		t.Fatalf("tail start = %d, want the active-turn limit 6", got.tailStart)
	}
	if got.headEnd != 1 {
		t.Fatalf("head end = %d, want 1", got.headEnd)
	}
}

func TestAlignHeadEnd_KeepsToolResultsWithTheirCall(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1", Content: "one"},
		{Role: "tool", ToolCallID: "c1", Content: "two"},
		{Role: "user", Content: "next"},
	}
	if got := alignHeadEnd(history, 2, len(history)); got != 4 {
		t.Fatalf("head end = %d, want 4 to keep both tool results", got)
	}
}

func TestLegacySummarize_KeepsProtectedHeadAndTokenTail(t *testing.T) {
	cfg := testConfig(t)
	first, last, minUser, budget := 1, 2, 1, 400
	cfg.Agents.Defaults.ProtectFirstN = &first
	cfg.Agents.Defaults.ProtectLastN = &last
	cfg.Agents.Defaults.MinTailUserMessages = &minUser
	cfg.Agents.Defaults.TailTokenBudget = &budget
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	history := []providers.Message{
		{Role: "user", Content: "FIRST-INSTRUCTION"},
		{Role: "assistant", Content: "ack"},
	}
	for i := 0; i < 8; i++ {
		history = append(history,
			providers.Message{Role: "user", Content: "middle-question-" + strings.Repeat("q", 400)},
			providers.Message{Role: "assistant", Content: "middle-answer-" + strings.Repeat("a", 400)},
		)
	}
	const sessionKey = "session-protected-head-tail"
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	lcm := al.contextManager.(*legacyContextManager)
	if !lcm.summarizeSession(context.Background(), agent, sessionKey, nil, "") {
		t.Fatal("expected a summary")
	}

	got, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got) >= len(history) {
		t.Fatalf("history = %d messages, want fewer than %d", len(got), len(history))
	}
	if got[0].Content != "FIRST-INSTRUCTION" {
		t.Fatalf("first message = %q, want the protected head", got[0].Content)
	}
	if got[len(got)-1].Content != history[len(history)-1].Content {
		t.Fatalf("last message = %q, want the protected tail", got[len(got)-1].Content)
	}
	if !strings.HasPrefix(got[len(got)-2].Content, "middle-question-") {
		t.Fatalf("tail start = %q, want the newest turn", got[len(got)-2].Content)
	}
	if summary, _ := agent.Sessions.GetSummary(context.Background(), sessionKey); summary != "test" {
		t.Fatalf("summary = %q, want the mock summary", summary)
	}
}

func TestLegacyForceCompression_DoesNotCutActiveTurn(t *testing.T) {
	al, agent, ts, turn := activeTurnFixture(t, 2)
	prior := []providers.Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "older question"},
		{Role: "assistant", Content: "older answer"},
	}
	history := append(append([]providers.Message(nil), prior...), turn...)
	const sessionKey = "active-turn-force"
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	lcm := al.contextManager.(*legacyContextManager)
	result, ok := lcm.forceCompression(sessionKey, ts)
	if !ok {
		t.Fatal("expected compression")
	}
	if result.DroppedMessages == 0 {
		t.Fatal("expected prior history to be dropped")
	}

	got, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got) < len(turn) {
		t.Fatalf("history = %d messages, want at least the %d of the active turn", len(got), len(turn))
	}
	suffix := got[len(got)-len(turn):]
	for i := range turn {
		if suffix[i].Content != turn[i].Content {
			t.Fatalf("active turn message %d = %q, want %q", i, suffix[i].Content, turn[i].Content)
		}
	}
}

func TestLegacyForceCompression_PreservesHeadAndTail(t *testing.T) {
	cfg := testConfig(t)
	first, last := 2, 4
	cfg.Agents.Defaults.ProtectFirstN = &first
	cfg.Agents.Defaults.ProtectLastN = &last
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	history := make([]providers.Message, 0, 12)
	for i := 0; i < 6; i++ {
		history = append(history,
			providers.Message{Role: "user", Content: "question " + string(rune('a'+i))},
			providers.Message{Role: "assistant", Content: "answer " + string(rune('a'+i))},
		)
	}
	const sessionKey = "session-head-tail-drop"
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	lcm := al.contextManager.(*legacyContextManager)
	result, ok := lcm.forceCompression(sessionKey, nil)
	if !ok {
		t.Fatal("expected compression")
	}
	if result.DroppedMessages == 0 {
		t.Fatal("expected messages to be dropped")
	}

	got, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got) != first+len(history)/2 {
		t.Fatalf("history = %d messages, want %d (head %d + newest half)", len(got), first+len(history)/2, first)
	}
	if got[0].Content != history[0].Content || got[1].Content != history[1].Content {
		t.Fatalf("protected head changed: %+v", got[:first])
	}
	for i := 0; i < last; i++ {
		want := history[len(history)-last+i]
		if got[len(got)-last+i].Content != want.Content {
			t.Fatalf("protected tail message %d = %q, want %q", i, got[len(got)-last+i].Content, want.Content)
		}
	}
}

func TestPruneToolResultsToBudget_StopsWhenTheRangeFits(t *testing.T) {
	workspace := t.TempDir()
	bulky := strings.Repeat("x", 5_000)
	history := []providers.Message{
		{Role: "tool", Content: bulky},
		{Role: "tool", Content: bulky},
	}
	if !pruneToolResultsToBudget(history, 0, len(history), workspace, nil, 2_500) {
		t.Fatal("expected the first result to be evicted")
	}
	if !strings.HasPrefix(history[0].Content, archivedToolResultPrefix) {
		t.Fatalf("first result = %q, want an archive stub", history[0].Content)
	}
	if history[1].Content != bulky {
		t.Fatal("the newest result should survive when the older one reclaims enough")
	}
}

func TestLegacyPrune_ArchivesHugeToolResultInTheTail(t *testing.T) {
	cfg := testConfig(t)
	first, last, budget := 1, 4, 100
	cfg.Agents.Defaults.ProtectFirstN = &first
	cfg.Agents.Defaults.ProtectLastN = &last
	cfg.Agents.Defaults.TailTokenBudget = &budget
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	bulky := strings.Repeat("huge tail output ", 400)
	history := []providers.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "old", Type: "function", Name: "read"}}},
		{Role: "tool", ToolCallID: "old", Content: "small old output"},
		{Role: "assistant", Content: "done with the old step"},
		{Role: "user", Content: "now read the huge file"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "huge", Type: "function", Name: "read"}}},
		{Role: "tool", ToolCallID: "huge", Content: bulky},
		{Role: "assistant", Content: "done"},
	}

	if !pruneOldToolResults(history, agent, len(history)) {
		t.Fatal("expected the huge tail tool result to be evicted")
	}
	if len(history) != 8 {
		t.Fatalf("messages = %d, want 8 (pruning must not drop messages)", len(history))
	}
	if history[0].Content != "start" {
		t.Fatalf("protected head changed: %q", history[0].Content)
	}
	if data := readArchivedToolResult(t, history[6].Content); string(data) != bulky {
		t.Fatalf("archived tail output = %q, want the original", data)
	}
	if history[2].Content != "small old output" {
		t.Fatalf("short output changed: %q", history[2].Content)
	}
}

func TestNewAgentInstance_ContextProtectionDefaultsAndOverrides(t *testing.T) {
	defaultsCfg := testConfig(t)
	defaultsCfg.Agents.Defaults.ProtectFirstN = nil
	defaultsCfg.Agents.Defaults.ProtectLastN = nil
	defaultsCfg.Agents.Defaults.MinTailUserMessages = nil
	defaultsCfg.Agents.Defaults.TailTokenBudget = nil
	defaultsCfg.Agents.Defaults.ContextWindow = 100_000
	defaultsCfg.Agents.Defaults.MaxTokens = 10_000

	al := newCMTestAgentLoop(defaultsCfg)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if agent.ProtectFirstN != defaultProtectFirstN ||
		agent.ProtectLastN != defaultProtectLastN ||
		agent.MinTailUserMessages != defaultMinTailUserMessages {
		t.Fatalf("defaults = %d/%d/%d, want %d/%d/%d",
			agent.ProtectFirstN, agent.ProtectLastN, agent.MinTailUserMessages,
			defaultProtectFirstN, defaultProtectLastN, defaultMinTailUserMessages)
	}
	if agent.TailTokenBudget != 9_000 {
		t.Fatalf("tail budget = %d, want 9000", agent.TailTokenBudget)
	}

	overrideCfg := testConfig(t)
	first, last, minUser, budget := 0, 0, 0, 0
	overrideCfg.Agents.Defaults.ProtectFirstN = &first
	overrideCfg.Agents.Defaults.ProtectLastN = &last
	overrideCfg.Agents.Defaults.MinTailUserMessages = &minUser
	overrideCfg.Agents.Defaults.TailTokenBudget = &budget

	al = newCMTestAgentLoop(overrideCfg)
	agent = al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	if agent.ProtectFirstN != 0 || agent.ProtectLastN != 0 ||
		agent.MinTailUserMessages != 0 || agent.TailTokenBudget != 0 {
		t.Fatalf("overrides not applied: %d/%d/%d/%d",
			agent.ProtectFirstN, agent.ProtectLastN, agent.MinTailUserMessages, agent.TailTokenBudget)
	}
}

func TestContextProtectionConfig_ExplicitZeroAndOmitted(t *testing.T) {
	var defaults config.AgentDefaults
	raw := `{
		"protect_first_n": 0,
		"protect_last_n": 0,
		"min_tail_user_messages": 0,
		"tail_token_budget": 0
	}`
	if err := json.Unmarshal([]byte(raw), &defaults); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	checks := []struct {
		name string
		got  *int
	}{
		{name: "protect_first_n", got: defaults.ProtectFirstN},
		{name: "protect_last_n", got: defaults.ProtectLastN},
		{name: "min_tail_user_messages", got: defaults.MinTailUserMessages},
		{name: "tail_token_budget", got: defaults.TailTokenBudget},
	}
	for _, check := range checks {
		if check.got == nil {
			t.Fatalf("%s = nil, want an explicit zero", check.name)
		}
		if *check.got != 0 {
			t.Fatalf("%s = %d, want 0", check.name, *check.got)
		}
	}

	var omitted config.AgentDefaults
	if err := json.Unmarshal([]byte(`{}`), &omitted); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if omitted.ProtectFirstN != nil || omitted.ProtectLastN != nil ||
		omitted.MinTailUserMessages != nil || omitted.TailTokenBudget != nil {
		t.Fatal("omitted protection fields must stay nil so agent defaults apply")
	}
}
