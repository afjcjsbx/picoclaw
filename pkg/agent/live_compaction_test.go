// PicoClaw - Ultra-lightweight personal AI agent

package agent

// Live compaction checks against a real model on OpenRouter. They are opt-in:
// every test skips unless PICOCLAW_LIVE_OPENROUTER_KEY is set, and the key is
// only ever read from the environment.
//
//	PICOCLAW_LIVE_OPENROUTER_KEY=sk-or-... \
//	  go test -tags goolm,stdjson ./pkg/agent -run TestLive -v -timeout 15m
//
// The tests only ever call liveModel: the recorder refuses any other model. The
// free upstream often answers 502/503/429 on first contact, so the recorder
// retries those (harness only, so a flaky provider does not mask what is being
// tested). Free models are slow and can time out; run a failing test again
// before reading anything into it.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// The only model these tests may call.
const liveModel = "nvidia/nemotron-3.5-super-vl:free"

const liveRetryAttempts = 4

// liveRetryPause is a variable so the offline tests can skip the wait.
var liveRetryPause = 4 * time.Second

// isRetryableLiveError reports whether an upstream failure is worth another try:
// overloaded (502/503) or rate limited (429).
func isRetryableLiveError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Status: 502") ||
		strings.Contains(msg, "Status: 503") ||
		strings.Contains(msg, "Status: 429")
}

type liveCall struct {
	summary   bool // no tools offered: a summarization request
	msgs      []providers.Message
	reply     string
	toolCalls []string
	dur       time.Duration
	err       error
}

type liveRecorder struct {
	inner providers.LLMProvider
	mu    sync.Mutex
	calls []liveCall
}

func (r *liveRecorder) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	if model != liveModel {
		return nil, fmt.Errorf("live test refuses to call model %q", model)
	}

	start := time.Now()
	var resp *providers.LLMResponse
	var err error
	for attempt := 0; attempt < liveRetryAttempts; attempt++ {
		resp, err = r.inner.Chat(ctx, messages, toolDefs, model, opts)
		if !isRetryableLiveError(err) {
			break
		}
		time.Sleep(liveRetryPause)
	}

	call := liveCall{
		summary: len(toolDefs) == 0,
		msgs:    append([]providers.Message(nil), messages...),
		dur:     time.Since(start),
		err:     err,
	}
	if resp != nil {
		call.reply = resp.Content
		for _, tc := range resp.ToolCalls {
			name := tc.Name
			if tc.Function != nil {
				name = tc.Function.Name
			}
			call.toolCalls = append(call.toolCalls, name)
		}
	}
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	return resp, err
}

func (r *liveRecorder) GetDefaultModel() string { return liveModel }

func (r *liveRecorder) snapshot() (loops, sums []liveCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.summary {
			sums = append(sums, c)
		} else {
			loops = append(loops, c)
		}
	}
	return loops, sums
}

func liveAgentLoop(t *testing.T, contextManager string, maxTokens int) (*AgentLoop, *AgentInstance, *liveRecorder) {
	t.Helper()
	key := os.Getenv("PICOCLAW_LIVE_OPENROUTER_KEY")
	if key == "" {
		t.Skip("PICOCLAW_LIVE_OPENROUTER_KEY not set")
	}
	mc := &config.ModelConfig{
		ModelName: "live",
		Provider:  "openrouter",
		Model:     liveModel,
		APIBase:   "https://openrouter.ai/api/v1",
		Enabled:   true,
	}
	mc.SetAPIKey(key)
	cfg := &config.Config{
		ModelList: config.SecureModelList{mc},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:             t.TempDir(),
				ModelName:             "live",
				MaxTokens:             maxTokens,
				MaxToolIterations:     12,
				SummarizeTokenPercent: 75,
				ContextManager:        contextManager,
			},
		},
	}
	inner, modelID, err := providers.CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if modelID != liveModel {
		t.Fatalf("resolved model %q, want %q", modelID, liveModel)
	}
	rec := &liveRecorder{inner: inner}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), rec)
	t.Cleanup(func() { al.Close() })
	return al, al.registry.GetDefaultAgent(), rec
}

// ---- fixtures -------------------------------------------------------------

var vaultCodes = []string{
	"AMBER-4817", "BASALT-9035", "CORAL-2264", "DUNE-7713", "EMBER-3390", "FJORD-6082",
	"GLACIER-1549", "HARBOR-8821", "IVORY-5307", "JASPER-2946", "KELP-7168", "LUMEN-4403",
	"MARBLE-9250", "NEBULA-3871", "OPAL-6614", "PRISM-1938", "QUARTZ-8056", "RIDGE-2729",
	"SABLE-5493", "TUNDRA-7305", "UMBER-1162", "VELVET-9847",
}

func vaultTurn(i int) (string, string) {
	return fmt.Sprintf("Please remember: the access code for vault %d is %s. Just acknowledge.", i+1, vaultCodes[i]),
		fmt.Sprintf("Noted: vault %d has access code %s.", i+1, vaultCodes[i])
}

// reportTool returns a long, distinct report with a unique code per number.
type reportTool struct{}

func (reportTool) Name() string { return "fetch_report" }
func (reportTool) Description() string {
	return "Fetch report number n (1-4). Returns a long text containing a CODE."
}

func (reportTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"n": map[string]any{"type": "integer", "description": "report number, 1 to 4"},
		},
		"required": []string{"n"},
	}
}

var reportCodes = map[int]string{1: "ORCHID-1-4817", 2: "ORCHID-2-9035", 3: "ORCHID-3-2264", 4: "ORCHID-4-7713"}

func (reportTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	n := 1
	if v, ok := args["n"].(float64); ok {
		n = int(v)
	}
	// Plain varied words: a long run of one character is replaced by a base64 placeholder.
	words := []string{
		"quarterly", "logistics", "throughput", "variance", "calibration", "harbor", "forecast",
		"inventory", "latency", "backlog", "regional", "supplier", "audit", "pipeline", "capacity", "rollout",
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "REPORT %d. CODE: %s. Details follow. ", n, reportCodes[n])
	for i := 0; sb.Len() < 3500; i++ {
		fmt.Fprintf(&sb, "%s-%d %s ", words[(i*7+n)%len(words)], i, words[(i*3+n*5)%len(words)])
	}
	return tools.SilentResult(sb.String())
}

func liveTurn(t *testing.T, al *AgentLoop, agent *AgentInstance, sessionKey, msg string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	resp, err := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     msg,
		DefaultResponse: defaultResponse,
		EnableSummary:   true,
	})
	if err != nil {
		t.Fatalf("runAgentLoop: %v", err)
	}
	return resp
}

// seedVaultSession puts `turns` vault Q/A pairs in the session (and in the
// ContextManager) without calling the model.
func seedVaultSession(t *testing.T, al *AgentLoop, agent *AgentInstance, key string, turns int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < turns; i++ {
		u, a := vaultTurn(i)
		for _, m := range []providers.Message{{Role: "user", Content: u}, {Role: "assistant", Content: a}} {
			if err := agent.Sessions.AddFullMessage(ctx, key, m); err != nil {
				t.Fatalf("AddFullMessage: %v", err)
			}
			if err := al.contextManager.Ingest(ctx, &IngestRequest{SessionKey: key, Message: m}); err != nil {
				t.Fatalf("Ingest: %v", err)
			}
		}
	}
}

func logLiveCalls(t *testing.T, rec *liveRecorder) (loops, sums []liveCall) {
	t.Helper()
	loops, sums = rec.snapshot()
	t.Logf("LLM calls: %d loop, %d summarization", len(loops), len(sums))
	for i, c := range loops {
		t.Logf("  loop %d: %d msgs, summary_in_prompt=%v, tool_calls=%v, %s err=%.300q reply=%.60q",
			i+1, len(c.msgs), requestContains(c.msgs, "CONTEXT_SUMMARY"), c.toolCalls,
			c.dur.Round(time.Millisecond), fmt.Sprint(c.err), c.reply)
	}
	for i, c := range sums {
		t.Logf("  summarization %d: %s err=%.300q -> %.200q",
			i+1, c.dur.Round(time.Millisecond), fmt.Sprint(c.err), c.reply)
	}
	return loops, sums
}

// ---- tests ----------------------------------------------------------------

func TestLive_Smoke(t *testing.T) {
	_, _, rec := liveAgentLoop(t, "", 512)
	resp, err := rec.Chat(context.Background(),
		[]providers.Message{{Role: "user", Content: "Reply with exactly the word PONG and nothing else."}},
		nil, liveModel, map[string]any{"max_tokens": 512, "temperature": 0.0})
	if err != nil {
		t.Fatalf("smoke call failed: %v", err)
	}
	t.Logf("smoke reply: %q", resp.Content)
}

// Legacy: earlier Turns are summarized by the real model before the first call
// and a fact that only lives in the summary is still answered correctly.
func TestLive_Legacy_TurnStart(t *testing.T) {
	al, agent, rec := liveAgentLoop(t, "", 2048)
	const key = "live-legacy-start"
	seedVaultSession(t, al, agent, key, 8)
	setThresholdAbove(t, agent, key, -300)
	t.Logf("window=%d threshold=%d", agent.ContextWindow, summarizeThresholdTokens(agent))

	answer := liveTurn(t, al, agent, key, "What is the access code for vault 2? Answer with the code only.")
	t.Logf("ANSWER: %q", answer)

	_, sums := logLiveCalls(t, rec)
	summary, _ := agent.Sessions.GetSummary(context.Background(), key)
	history, _ := agent.Sessions.GetHistory(context.Background(), key)
	t.Logf("stored summary (%d chars): %q", len(summary), summary)
	t.Logf("stored history: %d messages", len(history))

	if len(sums) == 0 {
		t.Error("no summarization happened")
	}
	if !strings.Contains(answer, vaultCodes[1]) {
		t.Errorf("answer %q lacks %s: the summary lost the fact", answer, vaultCodes[1])
	}
}

// Legacy: a single turn whose own tool loop crosses the threshold. The middle of
// the turn is summarized, the turn continues, and the final answer still has a
// code that only the summary carries.
func TestLive_Legacy_MidTurn(t *testing.T) {
	al, agent, rec := liveAgentLoop(t, "", 2048)
	al.RegisterTool(reportTool{})
	const key = "live-legacy-mid"
	// ~1450 tokens per report: the threshold is crossed once three are in the prompt.
	setThresholdAbove(t, agent, key, 3500)
	t.Logf("window=%d threshold=%d", agent.ContextWindow, summarizeThresholdTokens(agent))

	answer := liveTurn(t, al, agent, key,
		"Call the fetch_report tool with n=1 and wait for its result. Then call it with n=2, then n=3, then n=4: "+
			"strictly one call per step, never several at once. After the fourth report, reply with the four "+
			"CODE values you found, one per line, and nothing else.")
	t.Logf("ANSWER: %q", answer)

	loops, sums := logLiveCalls(t, rec)
	summary, _ := agent.Sessions.GetSummary(context.Background(), key)
	history, _ := agent.Sessions.GetHistory(context.Background(), key)
	t.Logf("stored summary (%d chars): %q", len(summary), summary)
	roles := make([]string, len(history))
	for i, m := range history {
		roles[i] = m.Role
	}
	t.Logf("stored history: %v", roles)

	if len(sums) == 0 {
		t.Error("no mid-turn summarization happened")
	}
	for n, code := range reportCodes {
		if !strings.Contains(answer, code) {
			t.Errorf("answer lacks code of report %d (%s)", n, code)
		}
	}
	// once compacted, a later loop call must no longer carry report 1 verbatim
	compactedSeen := false
	for _, c := range loops {
		if !requestContains(c.msgs, "CONTEXT_SUMMARY") {
			continue
		}
		compactedSeen = true
		if requestContains(c.msgs, "REPORT 1.") {
			t.Error("a compacted prompt still carries report 1 verbatim")
		}
		if !requestContains(c.msgs, "fetch_report") {
			t.Error("a compacted prompt lost the user's request")
		}
	}
	if !compactedSeen {
		t.Error("no loop call ever carried a summary")
	}
}

// Seahorse: a leaf summary replaces the oldest messages before the first call
// and a fact that only lives in the summary is still answered correctly.
func TestLive_Seahorse(t *testing.T) {
	al, agent, rec := liveAgentLoop(t, "seahorse", 2048)
	if _, ok := al.contextManager.(*seahorseContextManager); !ok {
		t.Fatalf("context manager is %T, want seahorse", al.contextManager)
	}
	const key = "live-seahorse"
	seedVaultSession(t, al, agent, key, 22) // 44 messages: 32 protected + 12 compactable
	setThresholdAbove(t, agent, key, -300)
	t.Logf("window=%d threshold=%d", agent.ContextWindow, summarizeThresholdTokens(agent))

	answer := liveTurn(t, al, agent, key, "What is the access code for vault 3? Answer with the code only.")
	t.Logf("ANSWER: %q", answer)

	_, sums := logLiveCalls(t, rec)

	cm := al.contextManager.(*seahorseContextManager)
	store := cm.engine.GetRetrieval().Store()
	conv, err := store.GetOrCreateConversation(context.Background(), key)
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	items, err := store.GetContextItems(context.Background(), conv.ConversationID)
	if err != nil {
		t.Fatalf("context items: %v", err)
	}
	nSummaries, nMessages := 0, 0
	for _, it := range items {
		if it.ItemType != "summary" {
			nMessages++
			continue
		}
		nSummaries++
		if s, err := store.GetSummary(context.Background(), it.SummaryID); err == nil {
			t.Logf("seahorse summary %s (kind=%s depth=%d): %q", it.SummaryID, s.Kind, s.Depth, s.Content)
		}
	}
	t.Logf("seahorse context items: %d summaries, %d messages", nSummaries, nMessages)

	if len(sums) == 0 || nSummaries == 0 {
		t.Error("seahorse did not compact")
	}
	if !strings.Contains(answer, vaultCodes[2]) {
		t.Errorf("answer %q lacks %s", answer, vaultCodes[2])
	}
}

// Seahorse compacts at turn start, then the turn runs a tool loop on the rebuilt
// prompt and still answers from both the summary and the tool results.
func TestLive_Seahorse_ToolLoop(t *testing.T) {
	al, agent, rec := liveAgentLoop(t, "seahorse", 2048)
	al.RegisterTool(reportTool{})
	const key = "live-seahorse-tools"
	seedVaultSession(t, al, agent, key, 22)
	setThresholdAbove(t, agent, key, -300)

	answer := liveTurn(t, al, agent, key,
		"First call the fetch_report tool with n=1, wait for the result, then call it with n=2 (one call at a time). "+
			"Then answer on two lines: the CODE of report 1 and the access code of vault 4 (from our earlier conversation). "+
			"Codes only.")
	t.Logf("ANSWER: %q", answer)
	loops, sums := logLiveCalls(t, rec)

	if len(sums) == 0 {
		t.Error("seahorse did not compact")
	}
	if !strings.Contains(answer, reportCodes[1]) || !strings.Contains(answer, vaultCodes[3]) {
		t.Errorf("answer %q lacks %s and/or %s", answer, reportCodes[1], vaultCodes[3])
	}
	for i, c := range loops {
		if c.err == nil && i > 0 && len(c.toolCalls) == 0 && !requestContains(c.msgs, "REPORT 1.") {
			t.Errorf("final call lost the tool result after the rebuild")
		}
	}
}

// ---- offline checks of the harness itself (always run) --------------------

type scriptedProvider struct {
	errs  []error // returned in order, then success
	calls int
}

func (p *scriptedProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls <= len(p.errs) {
		return nil, p.errs[p.calls-1]
	}
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (p *scriptedProvider) GetDefaultModel() string { return liveModel }

func TestLiveRecorder_RetriesOnlyOverloadAndRateLimit(t *testing.T) {
	defer func(d time.Duration) { liveRetryPause = d }(liveRetryPause)
	liveRetryPause = 0

	status := func(code int) error { return fmt.Errorf("API request failed:\n  Status: %d\n  Body:   {}", code) }
	cases := []struct {
		name      string
		errs      []error
		wantCalls int
		wantErr   bool
	}{
		{"502 then success", []error{status(502)}, 2, false},
		{"503 and 429 then success", []error{status(503), status(429)}, 3, false},
		{
			"gives up after the attempt cap",
			[]error{status(502), status(502), status(502), status(502), status(502)},
			liveRetryAttempts,
			true,
		},
		{"400 is not retried", []error{status(400)}, 1, true},
		{"timeout is not retried", []error{context.DeadlineExceeded}, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := &scriptedProvider{errs: tc.errs}
			rec := &liveRecorder{inner: inner}
			_, err := rec.Chat(context.Background(), nil, nil, liveModel, nil)
			if inner.calls != tc.wantCalls {
				t.Fatalf("provider calls = %d, want %d", inner.calls, tc.wantCalls)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestLiveRecorder_RefusesAnyOtherModel(t *testing.T) {
	inner := &scriptedProvider{}
	rec := &liveRecorder{inner: inner}
	if _, err := rec.Chat(context.Background(), nil, nil, "some/other-model", nil); err == nil {
		t.Fatal("expected the recorder to refuse a model other than liveModel")
	}
	if inner.calls != 0 {
		t.Fatalf("the provider was called %d times for a refused model", inner.calls)
	}
}
