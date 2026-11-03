// Package askuser implements the ask_user tool contract:
// input validation, question normalization, answer normalization and the
// platform-independent result document returned to the agent.
package askuser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxQuestions is the maximum number of questions accepted in one call.
	MaxQuestions = 5
	// MaxChoices is the maximum number of selectable options per question.
	MaxChoices = 4
	// MaxChoiceLength is the maximum length of a single option, in runes,
	// measured after trimming surrounding whitespace.
	MaxChoiceLength = 8000

	// RecommendedSuffix is appended to the first displayed option when a
	// question offers at least two valid choices. It is presentation-only and
	// never appears in the result returned to the model.
	RecommendedSuffix = " (Recommended)"
)

// Outcome is the overall result of an interaction.
type Outcome string

const (
	OutcomeSubmitted Outcome = "submitted"
	//nolint:misspell // The external contract uses the British spelling "cancelled".
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeTimedOut    Outcome = "timed_out"
	OutcomeUndelivered Outcome = "undelivered"
)

func (o Outcome) valid() bool {
	switch o {
	case OutcomeSubmitted, OutcomeCancelled, OutcomeTimedOut, OutcomeUndelivered:
		return true
	default:
		return false
	}
}

// Status is the state of an individual question.
type Status string

const (
	StatusAnswered   Status = "answered"
	StatusSkipped    Status = "skipped"
	StatusUnanswered Status = "unanswered"
)

// Question is a normalized question. Choices is nil for open questions.
type Question struct {
	ID          string
	Text        string
	Choices     []string
	MultiSelect bool
}

// DisplayChoices returns the options as they should be shown by an adapter.
// The first option carries the recommended marker when at least two choices
// are available. The marker is not duplicated when the option already
// contains a "(Recommended)" indication, case-insensitively.
func (q Question) DisplayChoices() []string {
	if len(q.Choices) == 0 {
		return nil
	}
	display := append([]string(nil), q.Choices...)
	if len(display) >= 2 && !containsRecommendedMarker(display[0]) {
		display[0] += RecommendedSuffix
	}
	return display
}

// AdapterResult is the contract an adapter returns to the core.
//
// Answers maps question IDs (q0, q1, ...) to either a string, an array of
// strings, or nil. A missing key means unanswered; an explicit nil or an
// empty value means skipped.
type AdapterResult struct {
	Answers map[string]any
	Outcome Outcome
	Notice  string
}

// Adapter collects user input for a normalized batch of questions.
type Adapter interface {
	Collect(ctx context.Context, questions []Question) (AdapterResult, error)
}

// Response is one entry of the tool result document.
type Response struct {
	Question       string   `json:"question"`
	ChoicesOffered []string `json:"choices_offered"`
	Status         Status   `json:"status"`
	UserResponse   any      `json:"user_response"`
}

// Document is the JSON document returned by the tool.
type Document struct {
	Responses []Response `json:"responses"`
	Outcome   Outcome    `json:"outcome"`
	Notice    string     `json:"notice,omitempty"`
}

// NormalizeQuestions validates and normalizes the raw "questions" argument.
// Validation happens entirely in the core before any adapter is invoked.
func NormalizeQuestions(raw any) ([]Question, error) {
	items, err := questionItems(raw)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("questions must contain at least 1 item")
	}
	if len(items) > MaxQuestions {
		return nil, fmt.Errorf("questions must contain at most %d items", MaxQuestions)
	}

	questions := make([]Question, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("questions[%d] must be an object", i)
		}

		rawText, exists := obj["question"]
		if !exists || rawText == nil {
			return nil, fmt.Errorf("questions[%d].question is required", i)
		}
		text, ok := rawText.(string)
		if !ok {
			return nil, fmt.Errorf("questions[%d].question must be a string", i)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("questions[%d].question must not be empty", i)
		}

		choices, err := normalizeChoices(i, obj["choices"])
		if err != nil {
			return nil, err
		}

		multiSelect := false
		if rawMulti, exists := obj["multi_select"]; exists && rawMulti != nil {
			value, ok := rawMulti.(bool)
			if !ok {
				return nil, fmt.Errorf("questions[%d].multi_select must be a boolean", i)
			}
			multiSelect = value
		}

		questions = append(questions, Question{
			ID:          fmt.Sprintf("q%d", i),
			Text:        text,
			Choices:     choices,
			MultiSelect: multiSelect && len(choices) > 0,
		})
	}

	return questions, nil
}

func questionItems(raw any) ([]any, error) {
	switch value := raw.(type) {
	case []any:
		return value, nil
	case []map[string]any:
		items := make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("questions must be an array")
	}
}

func normalizeChoices(questionIndex int, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}

	var values []any
	switch list := raw.(type) {
	case []any:
		values = list
	case []string:
		values = make([]any, 0, len(list))
		for _, item := range list {
			values = append(values, item)
		}
	default:
		return nil, fmt.Errorf("questions[%d].choices must be an array of strings", questionIndex)
	}

	if len(values) == 0 {
		return nil, nil
	}

	choices := make([]string, 0, min(len(values), MaxChoices))
	for i, rawChoice := range values {
		choice, ok := rawChoice.(string)
		if !ok {
			return nil, fmt.Errorf("questions[%d].choices[%d] must be a string", questionIndex, i)
		}
		choice = strings.TrimSpace(choice)
		if utf8.RuneCountInString(choice) > MaxChoiceLength {
			return nil, fmt.Errorf(
				"questions[%d].choices[%d] exceeds %d characters",
				questionIndex, i, MaxChoiceLength,
			)
		}
		if choice == "" {
			continue
		}
		if len(choices) == MaxChoices {
			continue
		}
		choices = append(choices, choice)
	}

	if len(choices) == 0 {
		return nil, fmt.Errorf(
			"questions[%d].choices was provided but contains no non-empty options",
			questionIndex,
		)
	}
	return choices, nil
}

// BuildDocument turns an adapter result into the normalized tool output.
func BuildDocument(questions []Question, result AdapterResult) (Document, error) {
	if !result.Outcome.valid() {
		return Document{}, fmt.Errorf("invalid ask_user outcome %q", result.Outcome)
	}

	document := Document{
		Responses: make([]Response, 0, len(questions)),
		Outcome:   result.Outcome,
		Notice:    result.Notice,
	}

	for _, question := range questions {
		response := Response{
			Question:       question.Text,
			ChoicesOffered: cloneChoices(question.Choices),
			Status:         StatusUnanswered,
		}

		raw, present := result.Answers[question.ID]
		if present {
			var (
				value    any
				answered bool
				err      error
			)
			if question.MultiSelect {
				value, answered, err = normalizeMultiAnswer(question.ID, raw)
			} else {
				value, answered, err = normalizeSingleAnswer(question.ID, raw)
			}
			if err != nil {
				return Document{}, err
			}
			if answered {
				response.Status = StatusAnswered
				response.UserResponse = value
			} else {
				response.Status = StatusSkipped
			}
		}

		document.Responses = append(document.Responses, response)
	}

	return document, nil
}

func normalizeSingleAnswer(questionID string, raw any) (any, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	text, ok := raw.(string)
	if !ok {
		return nil, false, fmt.Errorf("answer for %s must be a string or null", questionID)
	}
	text = normalizeAnswerText(text)
	if text == "" {
		return nil, false, nil
	}
	return text, true, nil
}

func normalizeMultiAnswer(questionID string, raw any) (any, bool, error) {
	var items []string
	switch value := raw.(type) {
	case nil:
		return nil, false, nil
	case string:
		text := strings.TrimSpace(value)
		if text == "" {
			return nil, false, nil
		}
		if parsed, ok := parseJSONStringArray(text); ok {
			items = parsed
		} else {
			items = []string{text}
		}
	case []string:
		items = append(items, value...)
	case []any:
		items = make([]string, 0, len(value))
		for i, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, false, fmt.Errorf(
					"answer for %s[%d] must be a string",
					questionID, i,
				)
			}
			items = append(items, text)
		}
	default:
		return nil, false, fmt.Errorf(
			"answer for %s must be a string, an array of strings, or null",
			questionID,
		)
	}

	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = normalizeAnswerText(item)
		if item == "" {
			continue
		}
		normalized = append(normalized, item)
	}
	if len(normalized) == 0 {
		return nil, false, nil
	}
	return normalized, true, nil
}

func parseJSONStringArray(value string) ([]string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") {
		return nil, false
	}
	var raw []any
	if err := json.Unmarshal([]byte(value), &raw); err != nil {
		return nil, false
	}
	items := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		items = append(items, text)
	}
	return items, true
}

func normalizeAnswerText(value string) string {
	value = strings.TrimSpace(value)
	const marker = "(recommended)"
	lower := strings.ToLower(value)
	if strings.HasSuffix(lower, marker) {
		value = strings.TrimSpace(value[:len(value)-len(marker)])
	}
	return value
}

func containsRecommendedMarker(value string) bool {
	return strings.Contains(strings.ToLower(value), "(recommended)")
}

func cloneChoices(choices []string) []string {
	if len(choices) == 0 {
		return nil
	}
	return append([]string(nil), choices...)
}

// MarshalDocument encodes a result document without HTML escaping so Unicode
// characters and URLs are preserved verbatim.
func MarshalDocument(document Document) (string, error) {
	return marshalJSON(document, true)
}

// MarshalError encodes an error in the recommended {"error": "..."} shape.
func MarshalError(message string) string {
	payload, err := marshalJSON(map[string]string{"error": message}, false)
	if err != nil {
		return `{"error":"ask_user failed"}`
	}
	return payload
}

func marshalJSON(value any, indent bool) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if indent {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}
