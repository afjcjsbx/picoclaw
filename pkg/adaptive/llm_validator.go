package adaptive

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// LLMValidator uses a small LLM to judge whether a response is good enough.
// It sends a structured prompt to the validator model and expects a JSON response
// with { "score": 0..1, "passed": true/false, "reason": "..." }.
// Invalid JSON or validator errors are treated as fail, triggering escalation.
type LLMValidator struct {
	provider           providers.LLMProvider
	validatorModel     string
	minScore           float64
	maxToolOutputChars int
	maxAssistantChars  int
	redactSecrets      bool
}

// NewLLMValidator creates an LLM-based validator.
// Returns nil if the provider is nil or the validator model is empty.
func NewLLMValidator(
	provider providers.LLMProvider,
	validatorModel string,
	cfg config.AdaptiveValidationConfig,
) *LLMValidator {
	if provider == nil || validatorModel == "" {
		return nil
	}

	minScore := cfg.MinScore
	if minScore <= 0 {
		minScore = defaultMinScore
	}

	maxToolOutput := cfg.MaxToolOutputChars
	if maxToolOutput <= 0 {
		maxToolOutput = defaultMaxToolOutputChars
	}

	maxAssistant := cfg.MaxAssistantChars
	if maxAssistant <= 0 {
		maxAssistant = defaultMaxAssistantChars
	}

	return &LLMValidator{
		provider:           provider,
		validatorModel:     validatorModel,
		minScore:           minScore,
		maxToolOutputChars: maxToolOutput,
		maxAssistantChars:  maxAssistant,
		redactSecrets:      cfg.RedactSecrets,
	}
}

const (
	defaultMaxToolOutputChars = 2000
	defaultMaxAssistantChars  = 4000
	validatorTimeout          = 30 * time.Second
)

// validatorResponse is the expected JSON schema from the validator LLM.
type validatorResponse struct {
	Score  float64 `json:"score"`
	Passed bool    `json:"passed"`
	Reason string  `json:"reason"`
}

const validatorSystemPrompt = `You are a response quality validator. Your job is to judge whether an AI assistant's response adequately addresses the user's request.

You will receive:
1. The user's original message
2. The assistant's response (possibly truncated)
3. Any tool call outputs (possibly truncated)

Evaluate the response on these criteria:
- Relevance: Does it address the user's request?
- Completeness: Does it appear to fully answer/handle the request?
- Coherence: Is the response well-formed and makes sense?
- Error-free: Are there signs of errors, hallucinations, or failures?

Respond with ONLY a JSON object in this exact format (no markdown, no extra text):
{"score": <0.0 to 1.0>, "passed": <true or false>, "reason": "<brief explanation>"}

Score guidelines:
- 1.0: Excellent, fully addresses the request
- 0.8-0.9: Good, mostly addresses the request with minor gaps
- 0.5-0.7: Partial, addresses some aspects but has notable gaps
- 0.2-0.4: Poor, barely addresses the request
- 0.0-0.1: Failed, does not address the request at all`

func (v *LLMValidator) Validate(ctx context.Context, resp *protocoltypes.LLMResponse, err error, userMessage string) ValidationResult {
	// Provider/runtime error: automatic fail without calling the validator.
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

	// Build the validation prompt.
	userPrompt := v.buildValidationPrompt(userMessage, resp)

	// Call the validator model with a dedicated timeout.
	validatorCtx, cancel := context.WithTimeout(ctx, validatorTimeout)
	defer cancel()

	messages := []providers.Message{
		{Role: "system", Content: validatorSystemPrompt},
		{Role: "user", Content: userPrompt},
	}

	opts := map[string]any{
		"max_tokens":  512,
		"temperature": 0.0,
	}

	logger.DebugCF("adaptive", "Calling LLM validator", map[string]any{
		"model":        v.validatorModel,
		"user_msg_len": len(userMessage),
		"resp_len":     len(resp.Content),
	})

	llmResp, llmErr := v.provider.Chat(validatorCtx, messages, nil, v.validatorModel, opts)
	if llmErr != nil {
		logger.WarnCF("adaptive", "LLM validator call failed, treating as validation failure", map[string]any{
			"error": llmErr.Error(),
			"model": v.validatorModel,
		})
		return ValidationResult{
			Score:  0.0,
			Passed: false,
			Reason: "validator error: " + llmErr.Error(),
		}
	}

	if llmResp == nil || llmResp.Content == "" {
		return ValidationResult{
			Score:  0.0,
			Passed: false,
			Reason: "validator returned empty response",
		}
	}

	// Parse the validator response.
	vr, parseErr := parseValidatorResponse(llmResp.Content)
	if parseErr != nil {
		logger.WarnCF("adaptive", "LLM validator returned invalid JSON, treating as validation failure", map[string]any{
			"error":   parseErr.Error(),
			"content": utils.Truncate(llmResp.Content, 200),
		})
		return ValidationResult{
			Score:  0.0,
			Passed: false,
			Reason: "validator returned invalid JSON: " + parseErr.Error(),
		}
	}

	// Apply our own min_score threshold on top of the validator's passed field.
	passed := vr.Passed && vr.Score >= v.minScore

	logger.InfoCF("adaptive", "LLM validator result", map[string]any{
		"score":      vr.Score,
		"passed":     passed,
		"reason":     vr.Reason,
		"raw_passed": vr.Passed,
		"min_score":  v.minScore,
	})

	return ValidationResult{
		Score:  vr.Score,
		Passed: passed,
		Reason: vr.Reason,
	}
}

// buildValidationPrompt constructs the user message sent to the validator model.
func (v *LLMValidator) buildValidationPrompt(userMessage string, resp *protocoltypes.LLMResponse) string {
	var sb strings.Builder

	sb.WriteString("## User's original message\n\n")
	sb.WriteString(userMessage)
	sb.WriteString("\n\n")

	sb.WriteString("## Assistant's response\n\n")
	assistantContent := resp.Content
	if v.redactSecrets {
		assistantContent = redactSecretPatterns(assistantContent)
	}
	sb.WriteString(utils.Truncate(assistantContent, v.maxAssistantChars))
	sb.WriteString("\n\n")

	// Include tool call information if present.
	if len(resp.ToolCalls) > 0 {
		sb.WriteString("## Tool calls made\n\n")
		for i, tc := range resp.ToolCalls {
			name := tc.Name
			if name == "" && tc.Function != nil {
				name = tc.Function.Name
			}
			sb.WriteString(fmt.Sprintf("- Tool %d: %s", i+1, name))

			args := ""
			if tc.Function != nil {
				args = tc.Function.Arguments
			}
			if args != "" {
				truncated := utils.Truncate(args, v.maxToolOutputChars)
				if v.redactSecrets {
					truncated = redactSecretPatterns(truncated)
				}
				sb.WriteString(fmt.Sprintf(" — args: %s", truncated))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Metadata\n\n")
	sb.WriteString(fmt.Sprintf("- finish_reason: %s\n", resp.FinishReason))
	if resp.Usage != nil {
		sb.WriteString(fmt.Sprintf("- tokens used: %d prompt + %d completion = %d total\n",
			resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens))
	}

	return sb.String()
}

// parseValidatorResponse extracts the JSON object from the validator LLM output.
// It handles cases where the LLM wraps the JSON in markdown code fences.
func parseValidatorResponse(content string) (*validatorResponse, error) {
	cleaned := extractJSON(content)

	var vr validatorResponse
	if err := json.Unmarshal([]byte(cleaned), &vr); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w (content: %s)", err, utils.Truncate(content, 100))
	}

	// Validate score range.
	if vr.Score < 0 {
		vr.Score = 0
	}
	if vr.Score > 1 {
		vr.Score = 1
	}

	return &vr, nil
}

// extractJSON tries to extract a JSON object from text that may contain
// markdown code fences or other surrounding text.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	// Strip markdown code fences if present.
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) > 1 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx > 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}

	// Find the first { and last } to extract the JSON object.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		s = s[start : end+1]
	}

	return s
}

// secretPattern matches common secret-like strings (API keys, tokens, passwords).
var secretPattern = regexp.MustCompile(
	`(?i)(sk-[a-zA-Z0-9]{20,}|` + // OpenAI keys
		`sk-ant-[a-zA-Z0-9-]{20,}|` + // Anthropic keys
		`xox[bprs]-[a-zA-Z0-9-]{10,}|` + // Slack tokens
		`ghp_[a-zA-Z0-9]{36}|` + // GitHub PATs
		`gsk_[a-zA-Z0-9]{20,}|` + // Groq keys
		`nvapi-[a-zA-Z0-9-]{20,}|` + // NVIDIA keys
		`(?:password|secret|token|api_key|apikey|auth)\s*[:=]\s*["']?[^\s"',]{8,})`, // generic key=value
)

// redactSecretPatterns replaces common secret patterns with [REDACTED].
func redactSecretPatterns(s string) string {
	return secretPattern.ReplaceAllString(s, "[REDACTED]")
}
