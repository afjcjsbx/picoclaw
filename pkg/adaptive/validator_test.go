package adaptive

import (
	"context"
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

func TestHeuristicValidator_ProviderError(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	vr := v.Validate(context.Background(), nil, errors.New("rate limit exceeded"), "")
	if vr.Passed {
		t.Error("expected fail on provider error")
	}
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestHeuristicValidator_NilResponse(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	vr := v.Validate(context.Background(), nil, nil, "")
	if vr.Passed {
		t.Error("expected fail on nil response")
	}
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestHeuristicValidator_GoodResponse(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "Hello! How can I help you?",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if !vr.Passed {
		t.Errorf("expected pass, got reason: %s", vr.Reason)
	}
	if vr.Score != 1.0 {
		t.Errorf("score = %f, want 1.0", vr.Score)
	}
}

func TestHeuristicValidator_EmptyOutput(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if vr.Passed {
		t.Error("expected fail on empty output")
	}
	// 1.0 - 0.4 (empty) = 0.6 < 0.75
	if vr.Score != 0.6 {
		t.Errorf("score = %f, want 0.6", vr.Score)
	}
}

func TestHeuristicValidator_Truncated(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "Some long response that was cut off...",
		FinishReason: "length",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if vr.Passed {
		t.Error("expected fail on truncated response")
	}
	// 1.0 - 0.3 (truncation) = 0.7, and reasons is non-empty so passed=false
	if vr.Score != 0.7 {
		t.Errorf("score = %f, want 0.7", vr.Score)
	}
}

func TestHeuristicValidator_MaxTokensTruncation(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "Response hit max tokens",
		FinishReason: "max_tokens",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if vr.Passed {
		t.Error("expected fail on max_tokens truncation")
	}
	if vr.Score != 0.7 {
		t.Errorf("score = %f, want 0.7", vr.Score)
	}
}

func TestHeuristicValidator_PendingToolCalls(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "", // no content, only tool calls
		FinishReason: "stop",
		ToolCalls: []protocoltypes.ToolCall{
			{Name: "read_file", ID: "call_1"},
		},
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if vr.Passed {
		t.Error("expected fail on pending tool calls without content")
	}
	// 1.0 - 0.4 (pending) = 0.6 (empty output check doesn't fire because ToolCalls > 0)
	if vr.Score != 0.6 {
		t.Errorf("score = %f, want 0.6", vr.Score)
	}
}

func TestHeuristicValidator_ToolCallsWithContent(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "I'll read the file for you.",
		FinishReason: "stop",
		ToolCalls: []protocoltypes.ToolCall{
			{Name: "read_file", ID: "call_1"},
		},
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	// Has content + tool calls = valid multi-step response
	if !vr.Passed {
		t.Errorf("expected pass for tool calls with content, got reason: %s", vr.Reason)
	}
}

func TestHeuristicValidator_ToolExecutionError(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "An error occurred",
		FinishReason: "error",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if vr.Passed {
		t.Error("expected fail on tool execution error")
	}
	// 1.0 - 0.6 = 0.4
	if vr.Score != 0.4 {
		t.Errorf("score = %f, want 0.4", vr.Score)
	}
}

func TestHeuristicValidator_MultiplePenalties(t *testing.T) {
	v := NewHeuristicValidator(0.75)
	resp := &protocoltypes.LLMResponse{
		Content:      "",
		FinishReason: "error",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if vr.Passed {
		t.Error("expected fail on multiple penalties")
	}
	// 1.0 - 0.6 (tool error) - 0.4 (empty) = 0.0 (clamped)
	if vr.Score != 0.0 {
		t.Errorf("score = %f, want 0.0", vr.Score)
	}
}

func TestHeuristicValidator_DefaultMinScore(t *testing.T) {
	v := NewHeuristicValidator(0)
	if v.MinScore != 0.75 {
		t.Errorf("default MinScore = %f, want 0.75", v.MinScore)
	}
}

func TestHeuristicValidator_CustomMinScore(t *testing.T) {
	v := NewHeuristicValidator(0.9)
	resp := &protocoltypes.LLMResponse{
		Content:      "Good response",
		FinishReason: "stop",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	if !vr.Passed {
		t.Errorf("expected pass with score 1.0 >= threshold 0.9, got reason: %s", vr.Reason)
	}
}

func TestHeuristicValidator_LowThresholdPassesTruncated(t *testing.T) {
	v := NewHeuristicValidator(0.5)
	resp := &protocoltypes.LLMResponse{
		Content:      "Some content",
		FinishReason: "length",
	}
	vr := v.Validate(context.Background(), resp, nil, "")
	// Score 0.7 >= 0.5 but reasons is non-empty, so passed is still false
	// because the spec says: score >= minScore AND no failure conditions
	if vr.Passed {
		t.Error("expected fail because there are failure reasons even though score >= threshold")
	}
}
