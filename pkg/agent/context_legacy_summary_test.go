package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// summaryRecordingProvider records every summarizer prompt and the options it
// was called with. It returns responses in order, repeating the last one.
type summaryRecordingProvider struct {
	mu        sync.Mutex
	prompts   []string
	options   []map[string]any
	responses []string
	calls     int
}

func (p *summaryRecordingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	prompt := ""
	if len(messages) > 0 {
		prompt = messages[len(messages)-1].Content
	}
	p.prompts = append(p.prompts, prompt)
	copied := make(map[string]any, len(opts))
	for key, value := range opts {
		copied[key] = value
	}
	p.options = append(p.options, copied)

	index := p.calls
	if index >= len(p.responses) {
		index = len(p.responses) - 1
	}
	p.calls++
	response := "summary"
	if len(p.responses) > 0 {
		response = p.responses[index]
	}
	return &providers.LLMResponse{
		Content:   response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (p *summaryRecordingProvider) GetDefaultModel() string {
	return "summary-recording-model"
}

func (p *summaryRecordingProvider) snapshot() ([]string, []map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.prompts...), append([]map[string]any(nil), p.options...)
}

func newSummaryTestLoop(
	t *testing.T,
	provider providers.LLMProvider,
	contextWindow, maxTokens int,
) (*AgentLoop, *AgentInstance) {
	t.Helper()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:             t.TempDir(),
				ModelName:             "test-model",
				MaxTokens:             maxTokens,
				ContextWindow:         contextWindow,
				MaxToolIterations:     10,
				SummarizeTokenPercent: 75,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	return al, agent
}

func TestSummaryMaxTokens(t *testing.T) {
	tests := []struct {
		name  string
		agent *AgentInstance
		want  int
	}{
		{name: "nil agent uses ceiling", agent: nil, want: summaryMaxTokensCeiling},
		{
			name:  "small window caps at 5 percent",
			agent: &AgentInstance{ContextWindow: 8000, MaxTokens: 4096},
			want:  400,
		},
		{
			name:  "large window caps at ceiling",
			agent: &AgentInstance{ContextWindow: 400000, MaxTokens: 32768},
			want:  summaryMaxTokensCeiling,
		},
		{name: "model output limit wins", agent: &AgentInstance{ContextWindow: 400000, MaxTokens: 4096}, want: 4096},
		{name: "unknown window uses output limit", agent: &AgentInstance{MaxTokens: 4096}, want: 4096},
		{name: "never zero", agent: &AgentInstance{ContextWindow: 10}, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summaryMaxTokens(tt.agent); got != tt.want {
				t.Fatalf("summaryMaxTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestClipSummaryTextStrictCap(t *testing.T) {
	text := strings.Repeat("context ", 100)
	for _, limit := range []int{10, 100, 500} {
		got := clipSummaryText(text, limit)
		if utf8.RuneCountInString(got) > limit {
			t.Fatalf("clipSummaryText(%d) returned %d runes, want <= %d", limit, utf8.RuneCountInString(got), limit)
		}
	}
}

func TestBuildSummaryPromptIsStructuredAndIterative(t *testing.T) {
	prompt := buildSummaryPrompt("OLD CHECKPOINT", "user: keep the API stable\nassistant: noted", "database migration")

	required := []string{
		"Use exactly this structure:",
		"## Goal",
		"## Constraints & Preferences",
		"## Progress",
		"## Key Decisions",
		"## Relevant Files",
		"## Commands and Tool Results",
		"## Errors",
		"## Next Steps",
		"## Critical Context",
		"EXISTING CHECKPOINT:\nOLD CHECKPOINT",
		"CONVERSATION SEGMENT:\nuser: keep the API stable",
		"FOCUS: database migration",
		"Prioritize details related to this focus",
	}
	for _, want := range required {
		if !strings.Contains(prompt, want) {
			t.Fatalf("summary prompt missing %q:\n%s", want, prompt)
		}
	}

	if strings.Index(prompt, "FOCUS:") < strings.Index(prompt, "CONVERSATION SEGMENT:") {
		t.Fatal("focus guidance must come after the conversation segment")
	}
}

func TestTurnTranscriptIncludesToolsAndRedactsSecrets(t *testing.T) {
	al, _ := newSummaryTestLoop(t, &summaryRecordingProvider{responses: []string{"ok"}}, 8000, 4096)
	lcm := &legacyContextManager{al: al}

	secretKey := "sk-1234567890abcdef"
	githubToken := "ghp_1234567890abcdefghij"
	batch := []providers.Message{
		{
			Role:    "assistant",
			Content: "I will inspect the file.",
			ToolCalls: []providers.ToolCall{{
				ID: "call-1",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"main.go","api_key":"` + secretKey + `"}`,
				},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			Content:    "package main\nconst token = \"" + githubToken + "\"",
		},
	}

	rendered := lcm.turnTranscript(batch)
	if len(rendered) != 2 {
		t.Fatalf("turnTranscript returned %d messages, want 2", len(rendered))
	}
	if !strings.Contains(rendered[0].Content, "[called read_file(") {
		t.Fatalf("assistant tool call missing from transcript: %q", rendered[0].Content)
	}
	if strings.Contains(rendered[0].Content, secretKey) {
		t.Fatalf("tool call arguments leaked a secret: %q", rendered[0].Content)
	}
	if !strings.Contains(rendered[1].Content, "[read_file result]") {
		t.Fatalf("tool result label missing from transcript: %q", rendered[1].Content)
	}
	if strings.Contains(rendered[1].Content, githubToken) {
		t.Fatalf("tool result leaked a secret: %q", rendered[1].Content)
	}
	if !strings.Contains(rendered[1].Content, "[FILTERED]") {
		t.Fatalf("tool result secret was not marked filtered: %q", rendered[1].Content)
	}
}

func TestSummarizeBatchUsesStructuredPromptToolActivityAndCap(t *testing.T) {
	secret := "sk-1234567890abcdef"
	provider := &summaryRecordingProvider{responses: []string{"SUMMARY " + secret}}
	al, agent := newSummaryTestLoop(t, provider, 8000, 4096)
	lcm := &legacyContextManager{al: al}

	batch := lcm.turnTranscript([]providers.Message{
		{
			Role:    "assistant",
			Content: "Reading the entry point.",
			ToolCalls: []providers.ToolCall{{
				ID: "call-1",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"main.go"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "package main\nfunc main() {}"},
	})

	summary, err := lcm.summarizeBatch(context.Background(), agent, batch, "OLD CHECKPOINT", "")
	if err != nil {
		t.Fatalf("summarizeBatch: %v", err)
	}

	prompts, options := provider.snapshot()
	if len(prompts) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prompts))
	}
	for _, want := range []string{"OLD CHECKPOINT", "[called read_file(", "[read_file result]", "## Next Steps"} {
		if !strings.Contains(prompts[0], want) {
			t.Fatalf("summarizer prompt missing %q:\n%s", want, prompts[0])
		}
	}
	if got := options[0]["max_tokens"]; got != summaryMaxTokens(agent) {
		t.Fatalf("max_tokens = %v, want %d", got, summaryMaxTokens(agent))
	}
	if strings.Contains(summary, secret) {
		t.Fatalf("summary leaked a secret: %q", summary)
	}
	if !strings.Contains(summary, "[FILTERED]") {
		t.Fatalf("summary secret was not filtered: %q", summary)
	}
}

func TestSummarizeBatchClipsOversizedSummary(t *testing.T) {
	huge := "SUMMARY\n" + strings.Repeat("detail ", 2000)
	provider := &summaryRecordingProvider{responses: []string{huge}}
	al, agent := newSummaryTestLoop(t, provider, 8000, 4096)
	lcm := &legacyContextManager{al: al}

	summary, err := lcm.summarizeBatch(
		context.Background(),
		agent,
		[]providers.Message{{Role: "user", Content: "do the thing"}},
		"",
		"",
	)
	if err != nil {
		t.Fatalf("summarizeBatch: %v", err)
	}
	if got, limit := utf8.RuneCountInString(summary), summaryMaxRunes(agent); got > limit {
		t.Fatalf("summary has %d runes, want <= %d", got, limit)
	}
	if !strings.Contains(summary, "summary trimmed") {
		t.Fatalf("oversized summary was not marked as trimmed: %q", summary[:min(len(summary), 200)])
	}
}

func TestFallbackSummaryIsStructuredAndRedacted(t *testing.T) {
	secret := "ghp_1234567890abcdefghij"
	al, _ := newSummaryTestLoop(t, &summaryRecordingProvider{responses: []string{"ok"}}, 8000, 4096)
	lcm := &legacyContextManager{al: al}

	fallback := lcm.fallbackSummary([]providers.Message{
		{Role: "assistant", Content: "Running the tests."},
		{Role: "tool", ToolCallID: "call-1", Content: "error: token " + secret},
	}, "OLD CHECKPOINT")

	for _, want := range []string{
		"## Previous Context",
		"OLD CHECKPOINT",
		"## Recovered Transcript",
		"- tool: error:",
		"## Next Steps",
	} {
		if !strings.Contains(fallback, want) {
			t.Fatalf("fallback summary missing %q:\n%s", want, fallback)
		}
	}
	if strings.Contains(fallback, secret) {
		t.Fatalf("fallback summary leaked a secret: %q", fallback)
	}
	if !strings.Contains(fallback, "[FILTERED]") {
		t.Fatalf("fallback summary secret was not filtered: %q", fallback)
	}
}

func TestSummarizeSessionIncludesToolsAndKeepsSummaryAcrossBatches(t *testing.T) {
	provider := &summaryRecordingProvider{responses: []string{"SUMMARY-1", "SUMMARY-2", "SUMMARY-3"}}
	al, agent := newSummaryTestLoop(t, provider, 8000, 4096)
	lcm := &legacyContextManager{al: al}

	long := strings.Repeat("context ", 125) // ~1000 chars, under the per-message clip.
	history := []providers.Message{
		{Role: "user", Content: "Read main.go. " + long},
		{
			Role:    "assistant",
			Content: "Reading it now. " + long,
			ToolCalls: []providers.ToolCall{{
				ID: "call-1",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"main.go"}`,
				},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "package main " + long},
		{Role: "assistant", Content: "The entry point is clear. " + long},
		{Role: "user", Content: "Continue with the tests. " + long},
		{Role: "assistant", Content: "Running the tests. " + long},
		{Role: "user", Content: "Fix the failure. " + long},
		{Role: "assistant", Content: "The failure is fixed. " + long},
		{Role: "user", Content: "Keep the API stable. " + long},
		{Role: "assistant", Content: "The API is stable. " + long},
		{Role: "user", Content: "Write the summary. " + long},
		{Role: "assistant", Content: "Summary written. " + long},
	}
	if err := agent.Sessions.SetHistory(context.Background(), "session-summary", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	if err := agent.Sessions.SetSummary(context.Background(), "session-summary", "OLD CHECKPOINT"); err != nil {
		t.Fatalf("SetSummary: %v", err)
	}

	if !lcm.summarizeSession(context.Background(), agent, "session-summary", nil, "") {
		t.Fatal("summarizeSession returned false, want true")
	}

	prompts, _ := provider.snapshot()
	if len(prompts) < 2 {
		t.Fatalf("provider calls = %d, want at least 2 batches", len(prompts))
	}
	if !strings.Contains(prompts[0], "OLD CHECKPOINT") {
		t.Fatalf("first batch did not receive the existing checkpoint:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[0], "[called read_file(") || !strings.Contains(prompts[0], "[read_file result]") {
		t.Fatalf("first batch did not include tool activity:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[1], "SUMMARY-1") {
		t.Fatalf("second batch did not receive the first batch summary:\n%s", prompts[1])
	}

	storedSummary, err := agent.Sessions.GetSummary(context.Background(), "session-summary")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if storedSummary != "SUMMARY-2" {
		t.Fatalf("stored summary = %q, want %q", storedSummary, "SUMMARY-2")
	}
	storedHistory, err := agent.Sessions.GetHistory(context.Background(), "session-summary")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(storedHistory) != 4 {
		t.Fatalf("history length = %d, want 4", len(storedHistory))
	}
}
