// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

const midTurnSummary = "MID-TURN-SUMMARY"

// bulkyTool returns a large output so the prompt grows during a tool loop.
// Each call is numbered ("round-N") so a test can tell which rounds a prompt carries.
type bulkyTool struct {
	size  int
	calls atomic.Int32
}

func (t *bulkyTool) Name() string        { return "bulky_tool" }
func (t *bulkyTool) Description() string { return "returns a large output" }
func (t *bulkyTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *bulkyTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	// Plain words: a long run of one character is replaced by a base64 placeholder.
	return tools.SilentResult(
		fmt.Sprintf("round-%d ", t.calls.Add(1)) + strings.Repeat("lorem ipsum dolor ", t.size/18),
	)
}

// midTurnProvider answers summarization prompts with midTurnSummary and drives
// the main loop: it asks for the bulky tool toolRounds times, then answers.
type midTurnProvider struct {
	mu           sync.Mutex
	toolRounds   int
	loopRequests [][]providers.Message
	summaryCalls int
	summaryReqs  []string
}

func (p *midTurnProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(messages) == 1 && (strings.HasPrefix(messages[0].Content, "Provide a concise summary") ||
		strings.HasPrefix(messages[0].Content, "Merge these two")) {
		p.summaryCalls++
		p.summaryReqs = append(p.summaryReqs, messages[0].Content)
		return &providers.LLMResponse{Content: midTurnSummary}, nil
	}

	p.loopRequests = append(p.loopRequests, append([]providers.Message(nil), messages...))
	if len(p.loopRequests) <= p.toolRounds {
		id := fmt.Sprintf("call_%d", len(p.loopRequests))
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID:       id,
			Type:     "function",
			Name:     "bulky_tool",
			Function: &providers.FunctionCall{Name: "bulky_tool", Arguments: "{}"},
		}}}, nil
	}
	return &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *midTurnProvider) GetDefaultModel() string { return "mid-turn-mock" }

func newMidTurnAgentLoop(t *testing.T, provider providers.LLMProvider, toolSize int) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:             t.TempDir(),
				ModelName:             "test-model",
				MaxTokens:             1000,
				MaxToolIterations:     10,
				SummarizeTokenPercent: 75,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	al.RegisterTool(&bulkyTool{size: toolSize})
	return al
}

// setThresholdAbove sizes the context window so the summarize threshold sits
// `headroom` tokens above what the session currently costs.
func setThresholdAbove(t *testing.T, agent *AgentInstance, sessionKey string, headroom int) {
	t.Helper()
	usage := computeContextUsage(agent, sessionKey)
	if usage == nil {
		t.Fatal("expected context usage")
	}
	threshold := usage.UsedTokens + headroom
	agent.ContextWindow = threshold*100/agent.SummarizeTokenPercent + agent.MaxTokens
}

func runCompactionTurn(t *testing.T, al *AgentLoop, agent *AgentInstance, sessionKey string) {
	t.Helper()
	if _, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "run the bulky tool",
		DefaultResponse: defaultResponse,
		EnableSummary:   true,
	}); err != nil {
		t.Fatalf("runAgentLoop: %v", err)
	}
}

func requestContains(msgs []providers.Message, needle string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, needle) {
			return true
		}
		for _, part := range m.SystemParts {
			if strings.Contains(part.Text, needle) {
				return true
			}
		}
	}
	return false
}

// A tool result that pushes the prompt over the threshold triggers a
// synchronous summarization before the next LLM call, which then continues
// from the compacted context without losing the active turn.
func TestCompactBeforeLLM_MidTurnSummarizesAndContinues(t *testing.T) {
	provider := &midTurnProvider{toolRounds: 1}
	al := newMidTurnAgentLoop(t, provider, 12000) // ~4800 tokens
	agent := al.registry.GetDefaultAgent()
	const sessionKey = "mid-turn"

	var seed []providers.Message
	for i := 0; i < 3; i++ {
		seed = append(
			seed,
			providers.Message{Role: "user", Content: fmt.Sprintf("seed-question-%d %s", i, strings.Repeat("q", 300))},
			providers.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("seed-answer-%d %s", i, strings.Repeat("a", 300)),
			},
		)
	}
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, seed); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	// Threshold above the prompt at turn start, below it once the tool result lands.
	setThresholdAbove(t, agent, sessionKey, 2000)

	runCompactionTurn(t, al, agent, sessionKey)

	if len(provider.loopRequests) != 2 {
		t.Fatalf("LLM loop calls = %d, want 2", len(provider.loopRequests))
	}
	if provider.summaryCalls == 0 {
		t.Fatal("expected a summarization LLM call mid-turn")
	}

	first, second := provider.loopRequests[0], provider.loopRequests[1]
	if !requestContains(first, "seed-question-0") || requestContains(first, midTurnSummary) {
		t.Fatal("first call must see the full history and no summary")
	}
	if requestContains(second, "seed-question-0") {
		t.Fatal("second call still carries history that was summarized away")
	}
	if !requestContains(second, midTurnSummary) {
		t.Fatal("second call must carry the summary")
	}
	if !requestContains(second, "run the bulky tool") ||
		!requestContains(second, "lorem ipsum dolor lorem ipsum dolor") {
		t.Fatal("second call must keep the active turn: user message and tool result")
	}

	summary, err := agent.Sessions.GetSummary(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if summary != midTurnSummary {
		t.Fatalf("stored summary = %q, want %q", summary, midTurnSummary)
	}
	history, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if last := history[len(history)-1]; last.Role != "assistant" || last.Content != "done" {
		t.Fatalf("last stored message = %+v, want the final answer", last)
	}
}

// A long history of small messages stays untouched while the prompt is far
// below the threshold: message count is not a trigger.
func TestCompactBeforeLLM_BelowThresholdDoesNothing(t *testing.T) {
	provider := &midTurnProvider{toolRounds: 1}
	al := newMidTurnAgentLoop(t, provider, 100)
	agent := al.registry.GetDefaultAgent()
	agent.ContextWindow = 1_000_000
	const sessionKey = "below-threshold"

	seed := alternatingHistory(30, "short")
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, seed); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	runCompactionTurn(t, al, agent, sessionKey)

	if provider.summaryCalls != 0 {
		t.Fatalf("summarization calls = %d, want 0", provider.summaryCalls)
	}
	history, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	// seed + user + assistant(tool call) + tool + final assistant
	if want := len(seed) + 4; len(history) != want {
		t.Fatalf("history len = %d, want %d", len(history), want)
	}
}

// At the start of a turn the same check compacts before the first LLM call.
func TestCompactBeforeLLM_TurnStartSummarizes(t *testing.T) {
	provider := &midTurnProvider{toolRounds: 0}
	al := newMidTurnAgentLoop(t, provider, 100)
	agent := al.registry.GetDefaultAgent()
	const sessionKey = "turn-start"

	seed := alternatingHistory(3, "seed "+strings.Repeat("s", 300))
	if err := agent.Sessions.SetHistory(context.Background(), sessionKey, seed); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	// Threshold already below the prompt that the new turn starts with.
	setThresholdAbove(t, agent, sessionKey, -100)

	runCompactionTurn(t, al, agent, sessionKey)

	if provider.summaryCalls == 0 {
		t.Fatal("expected summarization before the first LLM call")
	}
	if len(provider.loopRequests) != 1 || !requestContains(provider.loopRequests[0], midTurnSummary) {
		t.Fatal("the first LLM call must already carry the summary")
	}
}

// When the turn itself fills the window and no earlier Turn is left, its middle
// is summarized: the opening user message and the newest rounds stay verbatim,
// the turn goes on, and a later threshold hit folds into the same summary.
func TestCompactBeforeLLM_SummarizesMiddleOfSingleTurn(t *testing.T) {
	provider := &midTurnProvider{toolRounds: 4}
	al := newMidTurnAgentLoop(t, provider, 12000) // ~4800 tokens per round
	agent := al.registry.GetDefaultAgent()
	const sessionKey = "single-turn"

	// Over the threshold once three rounds have piled up.
	setThresholdAbove(t, agent, sessionKey, 11000)

	runCompactionTurn(t, al, agent, sessionKey)

	if len(provider.loopRequests) != 5 {
		t.Fatalf("LLM loop calls = %d, want 5", len(provider.loopRequests))
	}
	if len(provider.summaryReqs) != 2 {
		t.Fatalf("summarization calls = %d, want 2", len(provider.summaryReqs))
	}

	third, fourth, fifth := provider.loopRequests[2], provider.loopRequests[3], provider.loopRequests[4]
	if !requestContains(third, "round-2") || requestContains(third, midTurnSummary) {
		t.Fatal("third call must still see every round and no summary")
	}
	if requestContains(fourth, "round-1") || !requestContains(fourth, midTurnSummary) {
		t.Fatal("fourth call must carry the summary instead of round 1")
	}
	if !requestContains(fourth, "round-2") || !requestContains(fourth, "round-3") {
		t.Fatal("fourth call must keep the two newest rounds verbatim")
	}
	if requestContains(fifth, "round-2") || !requestContains(fifth, "round-3") || !requestContains(fifth, "round-4") {
		t.Fatal("fifth call must have folded round 2 away and kept rounds 3 and 4")
	}
	for i, req := range provider.loopRequests[3:] {
		if !requestContains(req, "run the bulky tool") {
			t.Fatalf("call %d lost the user message that opened the turn", i+4)
		}
	}

	if !strings.Contains(provider.summaryReqs[0], "[called bulky_tool(") ||
		!strings.Contains(provider.summaryReqs[0], "[bulky_tool result] round-1") {
		t.Fatalf("summarizer must see the tool calls and results of the middle:\n%.400s", provider.summaryReqs[0])
	}
	if !strings.Contains(provider.summaryReqs[1], "Existing context: "+midTurnSummary) {
		t.Fatal("the second summary must be rolled into the first")
	}

	history, err := agent.Sessions.GetHistory(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	// user + rounds 3 and 4 (assistant + tool each) + final answer
	assertRoles(t, history, "user", "assistant", "tool", "assistant", "tool", "assistant")
	if history[0].Content != "run the bulky tool" {
		t.Fatalf("history must still open with the user message, got %q", history[0].Content)
	}
}

func activeTurnFixture(t *testing.T, rounds int) (*AgentLoop, *AgentInstance, *turnState, []providers.Message) {
	t.Helper()
	provider := &midTurnProvider{}
	al := newMidTurnAgentLoop(t, provider, 100)
	agent := al.registry.GetDefaultAgent()

	turn := []providers.Message{{Role: "user", Content: "run the bulky tool"}}
	for i := 1; i <= rounds; i++ {
		id := fmt.Sprintf("c%d", i)
		turn = append(turn,
			providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID: id, Type: "function", Name: "bulky_tool",
				Function: &providers.FunctionCall{Name: "bulky_tool", Arguments: `{"q":"x"}`},
			}}},
			providers.Message{
				Role: "tool", ToolCallID: id,
				Content: fmt.Sprintf("round-%d ", i) + strings.Repeat("lorem ipsum dolor ", 150),
			},
		)
	}
	ts := &turnState{agent: agent, sessionKey: "active-turn"}
	for _, msg := range turn {
		ts.recordPersistedMessage(msg)
	}
	return al, agent, ts, turn
}

func TestLegacyCompact_Threshold_SummarizesActiveTurnMiddle(t *testing.T) {
	al, agent, ts, turn := activeTurnFixture(t, 4)
	prior := []providers.Message{
		{Role: "user", Content: "earlier question"},
		{Role: "assistant", Content: "earlier answer"},
	}
	if err := agent.Sessions.SetHistory(
		context.Background(),
		"active-turn",
		append(append([]providers.Message(nil), prior...), turn...),
	); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	req := &CompactRequest{SessionKey: "active-turn", Reason: ContextCompressReasonThreshold}
	ctx := withTurnState(context.Background(), ts)

	// First the earlier Turn goes into the summary...
	if err := al.contextManager.Compact(ctx, req); err != nil {
		t.Fatalf("first compaction: %v", err)
	}
	got, _ := agent.Sessions.GetHistory(context.Background(), "active-turn")
	if len(got) != len(turn) || got[0].Content != "run the bulky tool" {
		t.Fatalf("after the first pass history = %d messages, want the %d of the turn", len(got), len(turn))
	}

	// ...then the middle of the turn itself.
	if err := al.contextManager.Compact(ctx, req); err != nil {
		t.Fatalf("second compaction: %v", err)
	}
	got, _ = agent.Sessions.GetHistory(context.Background(), "active-turn")
	// user + the two newest rounds
	assertRoles(t, got, "user", "assistant", "tool", "assistant", "tool")
	if !strings.HasPrefix(got[2].Content, "round-3 ") || !strings.HasPrefix(got[4].Content, "round-4 ") {
		t.Fatalf("kept tool results = %.12q / %.12q, want rounds 3 and 4", got[2].Content, got[4].Content)
	}
	if summary, _ := agent.Sessions.GetSummary(context.Background(), "active-turn"); summary != midTurnSummary {
		t.Fatalf("summary = %q, want %q", summary, midTurnSummary)
	}
	if persisted := ts.persistedMessagesSnapshot(); len(persisted) != len(got) {
		t.Fatalf("persisted record = %d messages, want it to follow the session (%d)", len(persisted), len(got))
	}

	// Nothing left to fold: the two kept rounds are protected.
	if err := al.contextManager.Compact(ctx, req); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("third compaction err = %v, want ErrNothingToCompact", err)
	}
}

// Rolling the turn's pieces into a summary must never split a tool call from
// its results, whatever the number of parallel calls in a round.
func TestSplitToolRounds_KeepsToolResultsWithTheirCall(t *testing.T) {
	msgs := []providers.Message{
		{Role: "assistant"},
		{Role: "tool"},
		{Role: "tool"},
		{Role: "user"},
		{Role: "assistant"},
		{Role: "tool"},
	}
	rounds := splitToolRounds(msgs)
	if len(rounds) != 3 || len(rounds[0]) != 3 || len(rounds[1]) != 1 || len(rounds[2]) != 2 {
		t.Fatalf("rounds = %v, want sizes 3,1,2", rounds)
	}
}

func TestRefreshRestorePoint_FollowsCompactedHistoryLength(t *testing.T) {
	_, agent, ts, turn := activeTurnFixture(t, 1)
	// Earlier history was compacted down to one message since the turn started.
	ts.initialHistoryLength = 6
	if err := agent.Sessions.SetHistory(
		context.Background(),
		"active-turn",
		append([]providers.Message{{Role: "user", Content: "kept"}}, turn...),
	); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	ts.refreshRestorePointFromSession(agent)

	if got := ts.initialHistoryLen(); got != 1 {
		t.Fatalf("initial history length = %d, want 1 so a hard abort rolls back to the compacted history", got)
	}
}

// The session store stamps CreatedAt on what it keeps; the turn's own record of
// the same message has none, and the two must still match.
func TestMatchingTurnMessageTail_IgnoresStoreTimestamps(t *testing.T) {
	now := time.Now()
	stored := []providers.Message{{Role: "user", Content: "hi", CreatedAt: &now}}
	recorded := []providers.Message{{Role: "user", Content: "hi"}}
	if got := matchingTurnMessageTail(stored, recorded); got != 1 {
		t.Fatalf("matched = %d, want 1", got)
	}
}

// summarizerModelProvider answers summarization prompts and notes which model each was sent to.
type summarizerModelProvider struct {
	mu     sync.Mutex
	models []string
}

func (p *summarizerModelProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = append(p.models, model)
	return &providers.LLMResponse{Content: "summary", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *summarizerModelProvider) GetDefaultModel() string { return "recording" }

// The summarizer must send the model ID of the model_list entry, not the alias
// the agent is configured with; providers reject an alias as an unknown model.
func TestSummarizer_SendsResolvedModelID(t *testing.T) {
	const alias, modelID = "my-alias", "vendor/real-model-id"
	mc := &config.ModelConfig{
		ModelName: alias,
		Provider:  "openai",
		Model:     modelID,
		APIBase:   "http://127.0.0.1:1",
		Enabled:   true,
	}
	mc.SetAPIKey("test-key")
	cfg := &config.Config{
		ModelList: config.SecureModelList{mc},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         alias,
				MaxTokens:         1000,
				MaxToolIterations: 10,
			},
		},
	}
	provider := &summarizerModelProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	agent := al.registry.GetDefaultAgent()
	if err := agent.Sessions.SetHistory(
		context.Background(),
		"alias-session",
		alternatingHistory(3, "hello"),
	); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "alias-session",
		Reason:     ContextCompressReasonManual,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(provider.models) == 0 {
		t.Fatal("no summarization request reached the provider")
	}
	for _, m := range provider.models {
		if m != modelID {
			t.Fatalf("summarizer sent model %q, want %q", m, modelID)
		}
	}
}

// The fallback that clears old tool outputs must spare the newest rounds of the
// turn in progress: a round with many parallel calls is longer than the
// protected tail, and the model has not seen its results yet.
func TestLegacyCompact_Threshold_PruneKeepsNewestActiveRounds(t *testing.T) {
	al, agent, _, _ := activeTurnFixture(t, 0)
	turn := []providers.Message{
		{Role: "user", Content: "run the bulky tool five times"},
		{Role: "assistant"},
	}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("p%d", i)
		turn[1].ToolCalls = append(turn[1].ToolCalls, providers.ToolCall{
			ID: id, Type: "function", Name: "bulky_tool",
			Function: &providers.FunctionCall{Name: "bulky_tool", Arguments: "{}"},
		})
		turn = append(turn, providers.Message{
			Role: "tool", ToolCallID: id,
			Content: fmt.Sprintf("result-%d ", i) + strings.Repeat("lorem ipsum dolor ", 50),
		})
	}
	ts := &turnState{agent: agent, sessionKey: "active-turn"}
	for _, msg := range turn {
		ts.recordPersistedMessage(msg)
	}
	if err := agent.Sessions.SetHistory(context.Background(), "active-turn", turn); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(withTurnState(context.Background(), ts), &CompactRequest{
		SessionKey: "active-turn",
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("compaction err = %v, want ErrNothingToCompact", err)
	}
	got, _ := agent.Sessions.GetHistory(context.Background(), "active-turn")
	for _, msg := range got {
		if msg.Content == prunedToolResult {
			t.Fatalf("a tool result of the newest round was cleared before the model saw it")
		}
	}
}

// Clearing tool outputs inside the turn in progress must keep the turn's record
// in step with the session, so the active turn and the restore point still
// cover the whole turn.
func TestLegacyCompact_Threshold_PruneKeepsTurnRecordInStep(t *testing.T) {
	al, agent, ts, turn := activeTurnFixture(t, 4)
	if err := agent.Sessions.SetHistory(context.Background(), "active-turn", turn); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	// Too small a middle to be worth summarizing, so the prune fallback runs.
	agent.ContextWindow = 1 << 20

	err := al.contextManager.Compact(withTurnState(context.Background(), ts), &CompactRequest{
		SessionKey: "active-turn",
		Reason:     ContextCompressReasonThreshold,
	})
	if err != nil {
		t.Fatalf("compaction: %v", err)
	}
	got, _ := agent.Sessions.GetHistory(context.Background(), "active-turn")
	if got[2].Content != prunedToolResult {
		t.Fatalf("round 1 result = %.12q, want it cleared", got[2].Content)
	}
	if matched := matchingTurnMessageTail(got, ts.persistedMessagesSnapshot()); matched != len(turn) {
		t.Fatalf("turn record matches %d messages of the session, want all %d", matched, len(turn))
	}
	ts.refreshRestorePointFromSession(agent)
	if n := ts.initialHistoryLen(); n != 0 {
		t.Fatalf("restore point = %d messages, want 0 so a rollback drops the whole turn", n)
	}
}

// Once the middle of the turn is folded into the session summary, rolling the
// turn back must restore the summary it started from, not one describing the
// aborted work.
func TestLegacyCompact_Threshold_RollbackRestoresSummaryBeforeTurn(t *testing.T) {
	al, agent, ts, turn := activeTurnFixture(t, 4)
	ctx := context.Background()
	if err := agent.Sessions.SetHistory(ctx, "active-turn", turn); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	if err := agent.Sessions.SetSummary(ctx, "active-turn", "BEFORE-TURN"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}
	ts.captureRestorePoint(nil, "BEFORE-TURN")

	if err := al.contextManager.Compact(withTurnState(ctx, ts), &CompactRequest{
		SessionKey: "active-turn",
		Reason:     ContextCompressReasonThreshold,
	}); err != nil {
		t.Fatalf("compaction: %v", err)
	}
	if summary, _ := agent.Sessions.GetSummary(ctx, "active-turn"); summary != midTurnSummary {
		t.Fatalf("summary = %q, want the turn's middle folded in", summary)
	}
	// What CompactBeforeLLM does after every compaction.
	ts.refreshRestorePointFromSession(agent)

	if err := ts.restoreSession(agent); err != nil {
		t.Fatalf("restoreSession: %v", err)
	}
	if summary, _ := agent.Sessions.GetSummary(ctx, "active-turn"); summary != "BEFORE-TURN" {
		t.Fatalf("summary after rollback = %q, want %q", summary, "BEFORE-TURN")
	}
	if history, _ := agent.Sessions.GetHistory(ctx, "active-turn"); len(history) != 0 {
		t.Fatalf("history after rollback = %d messages, want none", len(history))
	}
}

// A steering message is a user message inside the turn in progress. Summarizing
// earlier Turns must not take it for a Turn boundary and cut the turn there.
func TestLegacyCompact_Threshold_DoesNotCutActiveTurnAtSteering(t *testing.T) {
	al, agent, ts, turn := activeTurnFixture(t, 2)
	steering := providers.Message{Role: "user", Content: "also check the logs"}
	ts.recordPersistedMessage(steering)
	turn = append(turn, steering)
	for _, msg := range append(activeTurnRound(3), activeTurnRound(4)...) {
		ts.recordPersistedMessage(msg)
		turn = append(turn, msg)
	}
	history := append([]providers.Message{
		{Role: "user", Content: "earlier question"},
		{Role: "assistant", Content: "earlier answer"},
	}, turn...)
	ctx := context.Background()
	if err := agent.Sessions.SetHistory(ctx, "active-turn", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	if err := al.contextManager.Compact(withTurnState(ctx, ts), &CompactRequest{
		SessionKey: "active-turn",
		Reason:     ContextCompressReasonThreshold,
	}); err != nil {
		t.Fatalf("compaction: %v", err)
	}
	got, _ := agent.Sessions.GetHistory(ctx, "active-turn")
	if len(got) != len(turn) || got[0].Content != turn[0].Content {
		t.Fatalf("history = %d messages starting %q, want the whole turn (%d) from %q",
			len(got), got[0].Content, len(turn), turn[0].Content)
	}
}

func activeTurnRound(i int) []providers.Message {
	id := fmt.Sprintf("c%d", i)
	return []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{{
			ID: id, Type: "function", Name: "bulky_tool",
			Function: &providers.FunctionCall{Name: "bulky_tool", Arguments: `{"q":"x"}`},
		}}},
		{
			Role: "tool", ToolCallID: id,
			Content: fmt.Sprintf("round-%d ", i) + strings.Repeat("lorem ipsum dolor ", 150),
		},
	}
}
