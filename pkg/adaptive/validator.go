package adaptive

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

// ValidationResult holds the outcome of validating an LLM response.
type ValidationResult struct {
	Score  float64 // 0.0–1.0 quality score
	Passed bool    // whether the response met the threshold
	Reason string  // human-readable explanation
}

// Validator checks whether an LLM response is good enough to return.
type Validator interface {
	Validate(ctx context.Context, resp *protocoltypes.LLMResponse, err error, userMessage string) ValidationResult
}

// HeuristicValidator scores LLM responses based on structural signals.
// It starts from 1.0 and deducts penalties for known failure indicators.
type HeuristicValidator struct {
	MinScore float64 // pass threshold (default 0.75)
}

// NewHeuristicValidator creates a heuristic validator with the given threshold.
// If minScore is <= 0, the default of 0.75 is used.
func NewHeuristicValidator(minScore float64) *HeuristicValidator {
	if minScore <= 0 {
		minScore = defaultMinScore
	}
	return &HeuristicValidator{MinScore: minScore}
}

const defaultMinScore = 0.75

// Penalty constants for the heuristic validator.
// Provider/runtime errors are handled by early return (score 0.0) rather
// than a penalty deduction, matching the spec's -1.0 behavior.
const (
	penaltyToolError    = 0.6
	penaltyEmptyOutput  = 0.4
	penaltyPendingTools = 0.4
	penaltyTruncation   = 0.3
)

func (h *HeuristicValidator) Validate(_ context.Context, resp *protocoltypes.LLMResponse, err error, _ string) ValidationResult {
	// Provider/runtime error: automatic fail.
	if err != nil {
		return ValidationResult{
			Score:  0.0,
			Passed: false,
			Reason: "provider error: " + err.Error(),
		}
	}

	if resp == nil {
		return ValidationResult{
			Score:  0.0,
			Passed: false,
			Reason: "nil response",
		}
	}

	score := 1.0
	var reasons []string

	// Check for tool execution errors in tool call results.
	// Tool errors are indicated by tool calls that resulted in error content.
	// We check FinishReason for tool-related failure signals.
	if hasToolExecutionError(resp) {
		score -= penaltyToolError
		reasons = append(reasons, "tool execution error detected")
	}

	// Empty assistant output (no content and no tool calls).
	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		score -= penaltyEmptyOutput
		reasons = append(reasons, "empty assistant output")
	}

	// Pending (unresolved) tool calls: the model requested tool calls
	// but we're validating the final response — this means tools weren't resolved.
	if len(resp.ToolCalls) > 0 && resp.Content == "" {
		score -= penaltyPendingTools
		reasons = append(reasons, "pending tool calls without content")
	}

	// Timeout / truncation: check finish_reason.
	if resp.FinishReason == "length" || resp.FinishReason == "max_tokens" {
		score -= penaltyTruncation
		reasons = append(reasons, "response truncated ("+resp.FinishReason+")")
	}

	// Clamp score to [0, 1].
	if score < 0 {
		score = 0
	}

	passed := score >= h.MinScore && len(reasons) == 0

	reason := "passed"
	if !passed {
		reason = joinReasons(reasons)
	}

	return ValidationResult{
		Score:  score,
		Passed: passed,
		Reason: reason,
	}
}

// hasToolExecutionError checks if the response indicates a tool execution failure.
// Since we validate the raw LLM response (before tool execution), this checks
// for responses where the model itself reports an error pattern.
func hasToolExecutionError(resp *protocoltypes.LLMResponse) bool {
	if resp.FinishReason == "error" || resp.FinishReason == "tool_error" {
		return true
	}
	return false
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "passed"
	}
	result := reasons[0]
	for i := 1; i < len(reasons); i++ {
		result += "; " + reasons[i]
	}
	return result
}
