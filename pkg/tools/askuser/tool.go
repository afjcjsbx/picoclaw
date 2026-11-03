package askuser

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// Tool exposes the ask_user capability to the model.
type Tool struct {
	adapter Adapter
}

// NewTool builds an ask_user tool around an adapter. A nil adapter is valid: the
// tool reports that the capability is unavailable in the current context.
func NewTool(adapter Adapter) *Tool {
	return &Tool{adapter: adapter}
}

// SetAdapter replaces the interaction adapter. It is safe to call before the
// tool is registered; adapters are not swapped while a request is in flight.
func (t *Tool) SetAdapter(adapter Adapter) {
	t.adapter = adapter
}

func (t *Tool) Name() string {
	return "ask_user"
}

func (t *Tool) Description() string {
	//nolint:misspell // The external contract uses the British spelling "cancelled".
	return "Ask the user one or more clarifying questions and wait for the answer. " +
		"Use this when a decision cannot be inferred from the conversation or the request is genuinely ambiguous. " +
		"Supports open text questions, single-choice and multi-choice questions. " +
		"Batch up to 5 independent questions per call. " +
		"The tool returns a JSON document with one entry per question and an overall outcome " +
		"(submitted, cancelled, timed_out, undelivered). " +
		"Questions whose answer depends on a previous answer must be asked in a separate call."
}

func (t *Tool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    MaxQuestions,
				"description": "One to five independent questions to present to the user.",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"question": map[string]any{
							"type":        "string",
							"description": "Question text. Must not be empty.",
						},
						"choices": map[string]any{
							"type":        "array",
							"description": "Optional selectable options. Omit or leave empty for a free-text question. The first option is shown as recommended when there are at least two. Extra options beyond the first four valid ones are ignored.",
							"items": map[string]any{
								"type":      "string",
								"maxLength": MaxChoiceLength,
							},
						},
						"multi_select": map[string]any{
							"type":        "boolean",
							"description": "Allow selecting more than one option. Ignored when no choices are provided.",
						},
					},
					"required": []string{"question"},
				},
			},
		},
		"required": []string{"questions"},
	}
}

func (t *Tool) Execute(ctx context.Context, args map[string]any) *toolshared.ToolResult {
	raw, exists := args["questions"]
	if !exists {
		return failure("questions is required")
	}

	questions, err := NormalizeQuestions(raw)
	if err != nil {
		return failure(err.Error())
	}

	if t.adapter == nil {
		return failure("ask_user is not available in the current context")
	}

	logger.InfoCF("ask_user", "Ask-user interaction started", map[string]any{
		"questions":    len(questions),
		"multi_select": countMultiSelect(questions),
	})

	result, err := t.collect(ctx, questions)
	if err != nil {
		logger.WarnCF("ask_user", "Ask-user interaction failed", map[string]any{
			"error": err.Error(),
		})
		return failure(fmt.Sprintf("ask_user interaction failed: %v", err))
	}

	document, err := BuildDocument(questions, result)
	if err != nil {
		return failure(err.Error())
	}

	payload, err := MarshalDocument(document)
	if err != nil {
		return failure(fmt.Sprintf("unable to encode ask_user result: %v", err))
	}

	logger.InfoCF("ask_user", "Ask-user interaction finished", map[string]any{
		"outcome":    string(document.Outcome),
		"answered":   countStatus(document, StatusAnswered),
		"skipped":    countStatus(document, StatusSkipped),
		"unanswered": countStatus(document, StatusUnanswered),
	})

	return &toolshared.ToolResult{
		ForLLM: payload,
		Silent: true,
	}
}

func failure(message string) *toolshared.ToolResult {
	return &toolshared.ToolResult{
		ForLLM:  MarshalError(strings.TrimSpace(message)),
		IsError: true,
		Err:     errors.New(message),
	}
}

// collect isolates adapter panics so a faulty adapter cannot crash the agent.
func (t *Tool) collect(ctx context.Context, questions []Question) (result AdapterResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = AdapterResult{}
			err = fmt.Errorf("ask_user adapter panic: %v", recovered)
		}
	}()
	return t.adapter.Collect(ctx, questions)
}

func countMultiSelect(questions []Question) int {
	count := 0
	for _, question := range questions {
		if question.MultiSelect {
			count++
		}
	}
	return count
}

func countStatus(document Document, status Status) int {
	count := 0
	for _, response := range document.Responses {
		if response.Status == status {
			count++
		}
	}
	return count
}
