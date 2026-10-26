package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const ToolFeedbackContinuationHint = "Continuing the current task."

func FormatArgsJSON(args map[string]any, prettyPrint, disableEscapeHTML bool) string {
	// Normalize nil to empty map for consistent output
	if args == nil {
		args = map[string]any{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if prettyPrint {
		enc.SetIndent("", "  ")
	}
	if disableEscapeHTML {
		enc.SetEscapeHTML(false)
	}
	if err := enc.Encode(args); err != nil {
		// Fallback to fmt.Sprintf to preserve visibility of problematic args
		return fmt.Sprintf("%v", args)
	}
	return strings.TrimSpace(buf.String())
}

// FormatToolFeedbackMessage renders a tool feedback message for chat channels.
// It keeps the tool name on the first line for animation and can include both
// a human explanation and the serialized tool arguments in the body.
func FormatToolFeedbackMessage(toolName, explanation, argsPreview string) string {
	toolName = strings.TrimSpace(toolName)
	explanation = strings.TrimSpace(explanation)
	argsPreview = strings.TrimSpace(argsPreview)

	bodyLines := make([]string, 0, 2)
	if explanation != "" {
		bodyLines = append(bodyLines, explanation)
	}
	if argsPreview != "" {
		bodyLines = append(bodyLines, "```json\n"+argsPreview+"\n```")
	}
	body := strings.Join(bodyLines, "\n")

	if toolName == "" {
		return body
	}
	if body == "" {
		return fmt.Sprintf("\U0001f527 `%s`", toolName)
	}

	return fmt.Sprintf("\U0001f527 `%s`\n%s", toolName, body)
}

// ToolFeedbackCall is one tool call listed in a merged tool feedback message.
type ToolFeedbackCall struct {
	Name        string
	ArgsPreview string
}

// FormatMergedToolFeedbackMessage renders all tool calls of one LLM response as
// a single tool feedback message, for channels that post every feedback as a
// new chat message. The first line lists the tool names, the explanation
// appears once, and every call keeps its own arguments block. A single call
// renders exactly like FormatToolFeedbackMessage.
func FormatMergedToolFeedbackMessage(calls []ToolFeedbackCall, explanation string) string {
	switch len(calls) {
	case 0:
		return FormatToolFeedbackMessage("", explanation, "")
	case 1:
		return FormatToolFeedbackMessage(calls[0].Name, explanation, calls[0].ArgsPreview)
	}

	order := make([]string, 0, len(calls))
	counts := make(map[string]int, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		if counts[name] == 0 {
			order = append(order, name)
		}
		counts[name]++
	}
	labels := make([]string, 0, len(order))
	for _, name := range order {
		label := fmt.Sprintf("`%s`", name)
		if counts[name] > 1 {
			label += fmt.Sprintf(" ×%d", counts[name])
		}
		labels = append(labels, label)
	}

	lines := []string{"\U0001f527 " + strings.Join(labels, " · ")}
	if explanation = strings.TrimSpace(explanation); explanation != "" {
		lines = append(lines, explanation)
	}
	for _, call := range calls {
		// The blank line starts a new paragraph, so chat clients that join
		// single newlines don't glue the call label to the explanation.
		lines = append(lines, "", fmt.Sprintf("`%s`", strings.TrimSpace(call.Name)))
		if args := strings.TrimSpace(call.ArgsPreview); args != "" {
			lines = append(lines, "```json\n"+args+"\n```")
		}
	}
	return strings.Join(lines, "\n")
}

// FitToolFeedbackMessage keeps tool feedback within a single outbound message.
// It preserves the first line when possible and truncates the explanation body
// instead of letting the message be split into multiple chunks.
func FitToolFeedbackMessage(content string, maxLen int) string {
	content = strings.TrimSpace(content)
	if content == "" || maxLen <= 0 {
		return ""
	}
	if len([]rune(content)) <= maxLen {
		return content
	}

	firstLine, rest, hasRest := strings.Cut(content, "\n")
	firstLine = strings.TrimSpace(firstLine)
	rest = strings.TrimSpace(rest)

	if !hasRest || rest == "" {
		return Truncate(firstLine, maxLen)
	}

	if len([]rune(firstLine)) >= maxLen {
		return Truncate(firstLine, maxLen)
	}

	remaining := maxLen - len([]rune(firstLine)) - 1
	if remaining <= 0 {
		return Truncate(firstLine, maxLen)
	}

	return firstLine + "\n" + Truncate(rest, remaining)
}
