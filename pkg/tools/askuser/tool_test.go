package askuser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

type recordingAdapter struct {
	calls   int
	last    []Question
	result  AdapterResult
	err     error
	context context.Context
}

func (a *recordingAdapter) Collect(ctx context.Context, questions []Question) (AdapterResult, error) {
	a.calls++
	a.last = append([]Question(nil), questions...)
	a.context = ctx
	return a.result, a.err
}

func TestToolRejectsInvalidInputBeforeAdapter(t *testing.T) {
	adapter := &recordingAdapter{result: AdapterResult{Outcome: OutcomeSubmitted}}
	tool := NewTool(adapter)

	result := tool.Execute(context.Background(), map[string]any{
		"questions": []any{map[string]any{"question": "   "}},
	})
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v, want error", result)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter called %d times, want 0", adapter.calls)
	}
	assertErrorJSON(t, result.ForLLM)
}

func TestToolRequiresQuestions(t *testing.T) {
	tool := NewTool(&recordingAdapter{})
	result := tool.Execute(context.Background(), map[string]any{})
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v", result)
	}
	assertErrorJSON(t, result.ForLLM)
}

func TestToolWithoutAdapterReturnsExplicitError(t *testing.T) {
	tool := NewTool(nil)
	result := tool.Execute(context.Background(), map[string]any{
		"questions": []any{map[string]any{"question": "Hello?"}},
	})
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v, want error", result)
	}
	if !strings.Contains(result.ForLLM, "not available") {
		t.Fatalf("error = %s", result.ForLLM)
	}
	assertErrorJSON(t, result.ForLLM)
}

func TestToolAdapterErrorIsManaged(t *testing.T) {
	tool := NewTool(&recordingAdapter{err: errors.New("boom")})
	result := tool.Execute(context.Background(), map[string]any{
		"questions": []any{map[string]any{"question": "Hello?"}},
	})
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v, want error", result)
	}
	if !strings.Contains(result.ForLLM, "boom") {
		t.Fatalf("error = %s", result.ForLLM)
	}
	assertErrorJSON(t, result.ForLLM)
}

type panicAdapter struct{}

func (panicAdapter) Collect(context.Context, []Question) (AdapterResult, error) {
	panic("adapter exploded")
}

func TestToolAdapterPanicIsManaged(t *testing.T) {
	tool := NewTool(panicAdapter{})
	result := tool.Execute(context.Background(), map[string]any{
		"questions": []any{map[string]any{"question": "Hello?"}},
	})
	if result == nil || !result.IsError {
		t.Fatalf("result = %+v, want error", result)
	}
	if !strings.Contains(result.ForLLM, "adapter exploded") {
		t.Fatalf("error = %s", result.ForLLM)
	}
	assertErrorJSON(t, result.ForLLM)
}

func TestToolBatchCallsAdapterOnceAndPreservesOrder(t *testing.T) {
	adapter := &recordingAdapter{result: AdapterResult{
		Answers: map[string]any{
			"q0": "first",
			"q1": []string{"B"},
		},
		Outcome: OutcomeSubmitted,
	}}
	tool := NewTool(adapter)

	result := tool.Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{"question": "First?"},
			map[string]any{"question": "Second?", "choices": []any{"A", "B"}, "multi_select": true},
		},
	})
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", adapter.calls)
	}
	if len(adapter.last) != 2 || adapter.last[0].ID != "q0" || adapter.last[1].ID != "q1" {
		t.Fatalf("normalized questions = %+v", adapter.last)
	}
	if !result.Silent {
		t.Fatal("ask_user result should be silent to avoid duplicating the interaction")
	}

	var document Document
	if err := json.Unmarshal([]byte(result.ForLLM), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Responses) != 2 {
		t.Fatalf("responses = %+v", document.Responses)
	}
	if document.Responses[0].Question != "First?" || document.Responses[0].UserResponse != "first" {
		t.Fatalf("first = %+v", document.Responses[0])
	}
	if document.Responses[1].Question != "Second?" {
		t.Fatalf("second = %+v", document.Responses[1])
	}
	values := document.Responses[1].UserResponse.([]any)
	if len(values) != 1 || values[0] != "B" {
		t.Fatalf("multi value = %#v", values)
	}
	if document.Outcome != OutcomeSubmitted {
		t.Fatalf("outcome = %s", document.Outcome)
	}
}

func TestToolOutputMatchesSpecShape(t *testing.T) {
	adapter := &recordingAdapter{result: AdapterResult{
		Answers: map[string]any{
			"q0": "JSON",
			"q1": []string{"Autenticazione", "Logging"},
			"q2": nil,
		},
		Outcome: OutcomeSubmitted,
	}}
	tool := NewTool(adapter)
	result := tool.Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{"question": "Quale formato preferisci?", "choices": []any{"JSON", "YAML", "XML"}},
			map[string]any{
				"question":     "Quali funzionalità ti servono?",
				"choices":      []any{"Autenticazione", "Logging", "Caching"},
				"multi_select": true,
			},
			map[string]any{"question": "Hai altri requisiti?"},
		},
	})
	if result == nil || result.IsError {
		t.Fatalf("result = %+v", result)
	}

	var document struct {
		Responses []struct {
			Question       string   `json:"question"`
			ChoicesOffered []string `json:"choices_offered"`
			Status         string   `json:"status"`
			UserResponse   any      `json:"user_response"`
		} `json:"responses"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &document); err != nil {
		t.Fatal(err)
	}
	if document.Outcome != "submitted" || len(document.Responses) != 3 {
		t.Fatalf("document = %+v", document)
	}
	if len(document.Responses[0].ChoicesOffered) != 3 {
		t.Fatalf("choices_offered = %+v", document.Responses[0].ChoicesOffered)
	}
	if document.Responses[2].ChoicesOffered != nil {
		t.Fatalf("open choices_offered = %+v", document.Responses[2].ChoicesOffered)
	}
	if document.Responses[2].Status != "skipped" || document.Responses[2].UserResponse != nil {
		t.Fatalf("skipped = %+v", document.Responses[2])
	}
}

func assertErrorJSON(t *testing.T, payload string) {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		t.Fatalf("error payload is not JSON: %v (%s)", err, payload)
	}
	if _, ok := parsed["error"]; !ok {
		t.Fatalf("error payload missing error key: %s", payload)
	}
	if _, ok := parsed["responses"]; ok {
		t.Fatalf("error payload looks like a success: %s", payload)
	}
}

var _ toolshared.Tool = (*Tool)(nil)
