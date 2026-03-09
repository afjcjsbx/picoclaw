package adaptive

import (
	"context"
	"fmt"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

// mockLLMProvider implements providers.LLMProvider for testing.
type mockLLMProvider struct {
	response *providers.LLMResponse
	err      error
}

func (m *mockLLMProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	return m.response, m.err
}

func (m *mockLLMProvider) GetDefaultModel() string { return "test-model" }

func defaultValidationCfg() config.AdaptiveValidationConfig {
	return config.AdaptiveValidationConfig{
		MinScore:           0.75,
		MaxToolOutputChars: 2000,
		MaxAssistantChars:  4000,
	}
}

func TestNewLLMValidator_NilProvider(t *testing.T) {
	v := NewLLMValidator(nil, "validator-model", defaultValidationCfg())
	if v != nil {
		t.Error("expected nil when provider is nil")
	}
}

func TestNewLLMValidator_EmptyModel(t *testing.T) {
	v := NewLLMValidator(&mockLLMProvider{}, "", defaultValidationCfg())
	if v != nil {
		t.Error("expected nil when validator model is empty")
	}
}

func TestNewLLMValidator_Defaults(t *testing.T) {
	v := NewLLMValidator(&mockLLMProvider{}, "test-model", config.AdaptiveValidationConfig{})
	if v == nil {
		t.Fatal("expected non-nil validator")
	}
	if v.minScore != defaultMinScore {
		t.Errorf("minScore = %f, want %f", v.minScore, defaultMinScore)
	}
	if v.maxToolOutputChars != defaultMaxToolOutputChars {
		t.Errorf("maxToolOutputChars = %d, want %d", v.maxToolOutputChars, defaultMaxToolOutputChars)
	}
	if v.maxAssistantChars != defaultMaxAssistantChars {
		t.Errorf("maxAssistantChars = %d, want %d", v.maxAssistantChars, defaultMaxAssistantChars)
	}
}

func TestLLMValidator_ProviderError(t *testing.T) {
	v := NewLLMValidator(&mockLLMProvider{}, "test-model", defaultValidationCfg())
	vr := v.Validate(context.Background(), nil, fmt.Errorf("connection refused"), "hello")
	if vr.Passed {
		t.Error("expected fail on provider error")
	}
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestLLMValidator_NilResponse(t *testing.T) {
	v := NewLLMValidator(&mockLLMProvider{}, "test-model", defaultValidationCfg())
	vr := v.Validate(context.Background(), nil, nil, "hello")
	if vr.Passed {
		t.Error("expected fail on nil response")
	}
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestLLMValidator_GoodResponse(t *testing.T) {
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.95, "passed": true, "reason": "excellent response"}`,
		},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Hello! I can help you with that.",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "Can you help me?")
	if !vr.Passed {
		t.Errorf("expected pass, got reason: %s", vr.Reason)
	}
	if vr.Score != 0.95 {
		t.Errorf("score = %f, want 0.95", vr.Score)
	}
}

func TestLLMValidator_LowScore(t *testing.T) {
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.3, "passed": false, "reason": "barely addresses the request"}`,
		},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "I don't know.",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "Explain quantum computing")
	if vr.Passed {
		t.Error("expected fail on low score")
	}
	if vr.Score != 0.3 {
		t.Errorf("score = %f, want 0.3", vr.Score)
	}
}

func TestLLMValidator_ValidatorCallFails(t *testing.T) {
	mock := &mockLLMProvider{
		err: fmt.Errorf("rate limit exceeded"),
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Some response",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "hello")
	if vr.Passed {
		t.Error("expected fail when validator call fails")
	}
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestLLMValidator_ValidatorReturnsEmpty(t *testing.T) {
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{Content: ""},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Some response",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "hello")
	if vr.Passed {
		t.Error("expected fail on empty validator response")
	}
}

func TestLLMValidator_ValidatorReturnsInvalidJSON(t *testing.T) {
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{Content: "this is not json at all"},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Some response",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "hello")
	if vr.Passed {
		t.Error("expected fail on invalid JSON")
	}
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestLLMValidator_ValidatorReturnsCodeFencedJSON(t *testing.T) {
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: "```json\n{\"score\": 0.9, \"passed\": true, \"reason\": \"good\"}\n```",
		},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Hello!",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "hi")
	if !vr.Passed {
		t.Errorf("expected pass with code-fenced JSON, got reason: %s", vr.Reason)
	}
	if vr.Score != 0.9 {
		t.Errorf("score = %f, want 0.9", vr.Score)
	}
}

func TestLLMValidator_MinScoreOverridesPassedField(t *testing.T) {
	// Validator says passed=true but score is below our min_score threshold
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.6, "passed": true, "reason": "seems ok"}`,
		},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Short answer",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "Explain something complex")
	if vr.Passed {
		t.Error("expected fail because score 0.6 < min_score 0.75, even though passed=true")
	}
}

func TestLLMValidator_ScoreClamping(t *testing.T) {
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 1.5, "passed": true, "reason": "amazing"}`,
		},
	}
	v := NewLLMValidator(mock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "Hello!",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "hi")
	if vr.Score != 1.0 {
		t.Errorf("score = %f, want 1.0 (clamped)", vr.Score)
	}
}

func TestLLMValidator_ToolCallsIncludedInPrompt(t *testing.T) {
	var capturedMessages []providers.Message
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.9, "passed": true, "reason": "good"}`,
		},
	}
	// Override the Chat method to capture the prompt
	capturedMock := &capturingMockProvider{
		mockLLMProvider: mock,
		onChat: func(msgs []providers.Message) {
			capturedMessages = msgs
		},
	}

	v := NewLLMValidator(capturedMock, "validator-model", defaultValidationCfg())

	resp := &protocoltypes.LLMResponse{
		Content:      "I'll read the file.",
		FinishReason: "stop",
		ToolCalls: []protocoltypes.ToolCall{
			{
				Name: "read_file",
				Function: &protocoltypes.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path": "/tmp/test.txt"}`,
				},
			},
		},
	}
	vr := v.Validate(context.Background(), resp, nil, "Read /tmp/test.txt")
	if !vr.Passed {
		t.Errorf("expected pass, got reason: %s", vr.Reason)
	}

	// Check that the validation prompt includes tool call info
	if len(capturedMessages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(capturedMessages))
	}
	userPrompt := capturedMessages[1].Content
	if !contains(userPrompt, "Tool calls made") {
		t.Error("expected validation prompt to include tool call section")
	}
	if !contains(userPrompt, "read_file") {
		t.Error("expected validation prompt to include tool name")
	}
}

func TestLLMValidator_RedactSecrets(t *testing.T) {
	var capturedMessages []providers.Message
	mock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.9, "passed": true, "reason": "good"}`,
		},
	}
	capturedMock := &capturingMockProvider{
		mockLLMProvider: mock,
		onChat: func(msgs []providers.Message) {
			capturedMessages = msgs
		},
	}

	cfg := defaultValidationCfg()
	cfg.RedactSecrets = true
	v := NewLLMValidator(capturedMock, "validator-model", cfg)

	resp := &protocoltypes.LLMResponse{
		Content:      "Your API key is sk-1234567890abcdefghijklmnop",
		FinishReason: "stop",
	}
	v.Validate(context.Background(), resp, nil, "What's my API key?")

	if len(capturedMessages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(capturedMessages))
	}
	userPrompt := capturedMessages[1].Content
	if contains(userPrompt, "sk-1234567890abcdefghijklmnop") {
		t.Error("expected secret to be redacted from validation prompt")
	}
	if !contains(userPrompt, "[REDACTED]") {
		t.Error("expected [REDACTED] placeholder in validation prompt")
	}
}

// capturingMockProvider wraps mockLLMProvider and captures messages.
type capturingMockProvider struct {
	*mockLLMProvider
	onChat func([]providers.Message)
}

func (c *capturingMockProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	if c.onChat != nil {
		c.onChat(messages)
	}
	return c.mockLLMProvider.Chat(ctx, messages, tools, model, opts)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestExtractJSON_PlainJSON(t *testing.T) {
	input := `{"score": 0.8, "passed": true, "reason": "good"}`
	result := extractJSON(input)
	if result != input {
		t.Errorf("extractJSON = %q, want %q", result, input)
	}
}

func TestExtractJSON_CodeFenced(t *testing.T) {
	input := "```json\n{\"score\": 0.8}\n```"
	result := extractJSON(input)
	if result != `{"score": 0.8}` {
		t.Errorf("extractJSON = %q, want %q", result, `{"score": 0.8}`)
	}
}

func TestExtractJSON_SurroundingText(t *testing.T) {
	input := "Here is my evaluation: {\"score\": 0.5, \"passed\": false, \"reason\": \"bad\"} end"
	result := extractJSON(input)
	expected := `{"score": 0.5, "passed": false, "reason": "bad"}`
	if result != expected {
		t.Errorf("extractJSON = %q, want %q", result, expected)
	}
}

func TestRedactSecretPatterns(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "OpenAI key",
			input: "key is sk-abcdefghijklmnopqrstuvwx",
			want:  "key is [REDACTED]",
		},
		{
			name:  "Anthropic key",
			input: "sk-ant-abcdefghijklmnopqrstuvwx",
			want:  "[REDACTED]",
		},
		{
			name:  "GitHub PAT",
			input: "ghp_abcdefghijklmnopqrstuvwxyz0123456789",
			want:  "[REDACTED]",
		},
		{
			name:  "no secret",
			input: "just a normal string",
			want:  "just a normal string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSecretPatterns(tt.input)
			if got != tt.want {
				t.Errorf("redactSecretPatterns(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRunWithLLMValidator_EndToEnd(t *testing.T) {
	// Create a mock provider that returns good validator scores
	validatorMock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.95, "passed": true, "reason": "good response"}`,
		},
	}

	cfg := &config.AdaptiveRoutingConfig{
		Enabled:              true,
		LocalFirstModel:      "local-model",
		CloudEscalationModel: "cloud-model",
		MaxEscalations:       1,
		Validation: config.AdaptiveValidationConfig{
			Mode:           "llm",
			MinScore:       0.75,
			ValidatorModel: "validator-model",
		},
	}

	r := NewRunner(
		[]providers.FallbackCandidate{{Provider: "ollama", Model: "local-model"}},
		[]providers.FallbackCandidate{{Provider: "openai", Model: "cloud-model"}},
		"local-model", "cloud-model", cfg, validatorMock,
	)
	if r == nil {
		t.Fatal("expected non-nil runner")
	}

	result, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			return "Hello from local!", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Escalated {
		t.Error("expected no escalation with good LLM validation score")
	}
	if result.Content != "Hello from local!" {
		t.Errorf("content = %q, want %q", result.Content, "Hello from local!")
	}
}

func TestRunWithLLMValidator_FailTriggersEscalation(t *testing.T) {
	validatorMock := &mockLLMProvider{
		response: &providers.LLMResponse{
			Content: `{"score": 0.2, "passed": false, "reason": "poor quality response"}`,
		},
	}

	cfg := &config.AdaptiveRoutingConfig{
		Enabled:              true,
		LocalFirstModel:      "local-model",
		CloudEscalationModel: "cloud-model",
		MaxEscalations:       1,
		Validation: config.AdaptiveValidationConfig{
			Mode:           "llm",
			MinScore:       0.75,
			ValidatorModel: "validator-model",
		},
	}

	r := NewRunner(
		[]providers.FallbackCandidate{{Provider: "ollama", Model: "local-model"}},
		[]providers.FallbackCandidate{{Provider: "openai", Model: "cloud-model"}},
		"local-model", "cloud-model", cfg, validatorMock,
	)

	calls := 0
	result, err := r.Run(
		context.Background(),
		"explain quantum computing",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			calls++
			if calls == 1 {
				return "idk", 1, nil
			}
			return "Quantum computing uses qubits...", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Escalated {
		t.Error("expected escalation when LLM validator fails")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (local + cloud)", calls)
	}
}
