package integrationtools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	AskUserToolName    = "ask_user"
	askUserWaitingNote = "Awaiting user input."
)

type AskUserTool struct{}

func NewAskUserTool() *AskUserTool {
	return &AskUserTool{}
}

func (t *AskUserTool) Name() string {
	return AskUserToolName
}

func (t *AskUserTool) Description() string {
	return "Pause the current turn and ask the user a question when their answer is required before continuing. " +
		"Use options for likely answers, but the user may still reply with free text. " +
		"Call this tool alone in its tool-call batch."
}

func (t *AskUserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "The question to ask before continuing. Use this only when the task needs the user's answer.",
			},
			"options": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Optional choices. The user may still reply with free text.",
			},
		},
		"required": []string{"question"},
	}
}

func (t *AskUserTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	_ = ctx

	question, _ := args["question"].(string)
	question = strings.TrimSpace(question)
	if question == "" {
		return ErrorResult("question is required")
	}

	options := normalizeAskUserOptions(args["options"])

	return &ToolResult{
		ForLLM:         askUserWaitingNote,
		ForUser:        formatAskUserPrompt(question, options),
		AwaitUserInput: true,
		InputOptions:   append([]string(nil), options...),
	}
}

func normalizeAskUserOptions(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return normalizeAskUserOptionStrings(typed)
	case []any:
		options := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			options = append(options, text)
		}
		return normalizeAskUserOptionStrings(options)
	default:
		return nil
	}
}

func normalizeAskUserOptionStrings(options []string) []string {
	if len(options) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(options))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		normalized = append(normalized, option)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func formatAskUserPrompt(question string, options []string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	if len(options) == 0 {
		return question
	}

	lines := make([]string, 0, len(options)+2)
	lines = append(lines, question, "")
	for index, option := range options {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, option))
	}
	return strings.Join(lines, "\n")
}

func normalizeAskUserReply(content string, options []string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(options) == 0 {
		return content
	}

	index, err := strconv.Atoi(content)
	if err != nil {
		return content
	}
	if index < 1 || index > len(options) {
		return content
	}
	return options[index-1]
}
