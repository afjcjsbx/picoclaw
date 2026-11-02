package askuser

import (
	"strings"
	"testing"
)

func normalized(t *testing.T, raw any) []Question {
	t.Helper()
	questions, err := NormalizeQuestions(raw)
	if err != nil {
		t.Fatalf("NormalizeQuestions() error = %v", err)
	}
	return questions
}

func TestNormalizeQuestionsIdentifiersAndDefaults(t *testing.T) {
	questions := normalized(t, []any{
		map[string]any{"question": "  First?  "},
		map[string]any{
			"question":     "Second?",
			"choices":      []any{"  A  ", "", "B"},
			"multi_select": true,
		},
	})

	if len(questions) != 2 {
		t.Fatalf("len = %d, want 2", len(questions))
	}
	if questions[0].ID != "q0" || questions[1].ID != "q1" {
		t.Fatalf("ids = %q, %q", questions[0].ID, questions[1].ID)
	}
	if questions[0].Text != "First?" {
		t.Fatalf("trimmed text = %q", questions[0].Text)
	}
	if questions[0].Choices != nil || questions[0].MultiSelect {
		t.Fatalf("open question should have nil choices and multi-select disabled: %+v", questions[0])
	}
	if got := strings.Join(questions[1].Choices, ","); got != "A,B" {
		t.Fatalf("choices = %q, want A,B", got)
	}
	if !questions[1].MultiSelect {
		t.Fatal("multi-select should be enabled")
	}
}

func TestNormalizeQuestionsTruncatesToFourChoices(t *testing.T) {
	questions := normalized(t, []any{map[string]any{
		"question": "Pick",
		"choices":  []any{"A", "B", "", "C", "D", "E"},
	}})
	if got := strings.Join(questions[0].Choices, ","); got != "A,B,C,D" {
		t.Fatalf("choices = %q, want A,B,C,D", got)
	}
}

func TestRecommendedPresentation(t *testing.T) {
	question := Question{Text: "Pick", Choices: []string{"A", "B"}}
	display := question.DisplayChoices()
	if display[0] != "A"+RecommendedSuffix || display[1] != "B" {
		t.Fatalf("display = %#v", display)
	}
	if question.Choices[0] != "A" {
		t.Fatal("DisplayChoices mutated the source choices")
	}

	single := Question{Text: "Pick", Choices: []string{"Only"}}
	if got := single.DisplayChoices()[0]; got != "Only" {
		t.Fatalf("single choice = %q, want no recommended marker", got)
	}

	already := Question{Text: "Pick", Choices: []string{"A (recommended)", "B"}}
	if got := already.DisplayChoices()[0]; got != "A (recommended)" {
		t.Fatalf("duplicated marker: %q", got)
	}
}

func TestBuildDocumentNormalizesAnswers(t *testing.T) {
	questions := normalized(t, []any{
		map[string]any{"question": "Open"},
		map[string]any{"question": "Single", "choices": []any{"JSON", "YAML"}},
		map[string]any{"question": "Multi", "choices": []any{"A", "B", "C"}, "multi_select": true},
	})

	document, err := BuildDocument(questions, AdapterResult{
		Answers: map[string]any{
			"q0": " hello ",
			"q1": "JSON (Recommended)",
			"q2": []any{" A ", "", "B (recommended)"},
		},
		Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(document.Responses) != 3 {
		t.Fatalf("responses = %d", len(document.Responses))
	}
	if got := document.Responses[0].UserResponse; got != "hello" {
		t.Fatalf("q0 = %#v", got)
	}
	if got := document.Responses[1].UserResponse; got != "JSON" {
		t.Fatalf("q1 = %#v", got)
	}
	multi, ok := document.Responses[2].UserResponse.([]string)
	if !ok || strings.Join(multi, ",") != "A,B" {
		t.Fatalf("q2 = %#v", document.Responses[2].UserResponse)
	}
	if got := strings.Join(document.Responses[1].ChoicesOffered, ","); got != "JSON,YAML" {
		t.Fatalf("choices_offered = %q", got)
	}
	if document.Responses[0].ChoicesOffered != nil {
		t.Fatalf("open choices_offered = %#v, want nil", document.Responses[0].ChoicesOffered)
	}
}

func TestBuildDocumentStatuses(t *testing.T) {
	questions := normalized(t, []any{
		map[string]any{"question": "Answered"},
		map[string]any{"question": "Skipped"},
		map[string]any{"question": "Missing"},
		map[string]any{"question": "Empty multi", "choices": []any{"A", "B"}, "multi_select": true},
	})

	document, err := BuildDocument(questions, AdapterResult{
		Answers: map[string]any{
			"q0": "value",
			"q1": "",
			"q3": []any{},
		},
		Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}

	if document.Responses[0].Status != StatusAnswered {
		t.Fatalf("q0 status = %s", document.Responses[0].Status)
	}
	if document.Responses[1].Status != StatusSkipped || document.Responses[1].UserResponse != nil {
		t.Fatalf("q1 = %+v", document.Responses[1])
	}
	if document.Responses[2].Status != StatusUnanswered || document.Responses[2].UserResponse != nil {
		t.Fatalf("q2 = %+v", document.Responses[2])
	}
	if document.Responses[3].Status != StatusSkipped || document.Responses[3].UserResponse != nil {
		t.Fatalf("q3 = %+v", document.Responses[3])
	}
}

func TestBuildDocumentMultiSelectJSONArray(t *testing.T) {
	questions := normalized(t, []any{map[string]any{
		"question":     "Pick",
		"choices":      []any{"A", "B", "C"},
		"multi_select": true,
	}})

	document, err := BuildDocument(questions, AdapterResult{
		Answers: map[string]any{"q0": `["A", "C"]`},
		Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}
	values := document.Responses[0].UserResponse.([]string)
	if strings.Join(values, ",") != "A,C" {
		t.Fatalf("values = %#v", values)
	}

	document, err = BuildDocument(questions, AdapterResult{
		Answers: map[string]any{"q0": "custom answer, with comma"},
		Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}
	values = document.Responses[0].UserResponse.([]string)
	if len(values) != 1 || values[0] != "custom answer, with comma" {
		t.Fatalf("values = %#v", values)
	}
}

func TestRecommendedSuffixRemovedWithoutSpace(t *testing.T) {
	questions := normalized(t, []any{map[string]any{
		"question": "Pick",
		"choices":  []any{"A", "B"},
	}})
	document, err := BuildDocument(questions, AdapterResult{
		Answers: map[string]any{"q0": "A(Recommended)"},
		Outcome: OutcomeSubmitted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := document.Responses[0].UserResponse; got != "A" {
		t.Fatalf("answer = %#v", got)
	}
}

func TestBuildDocumentTimeoutPreservesAnswers(t *testing.T) {
	questions := normalized(t, []any{
		map[string]any{"question": "First"},
		map[string]any{"question": "Second"},
	})

	document, err := BuildDocument(questions, AdapterResult{
		Answers: map[string]any{"q0": "saved"},
		Outcome: OutcomeTimedOut,
		Notice:  "[user did not respond within 5m]",
	})
	if err != nil {
		t.Fatal(err)
	}
	if document.Outcome != OutcomeTimedOut {
		t.Fatalf("outcome = %s", document.Outcome)
	}
	if document.Responses[0].Status != StatusAnswered || document.Responses[0].UserResponse != "saved" {
		t.Fatalf("first = %+v", document.Responses[0])
	}
	if document.Responses[1].Status != StatusUnanswered {
		t.Fatalf("second = %+v", document.Responses[1])
	}
	if document.Notice != "[user did not respond within 5m]" {
		t.Fatalf("notice = %q", document.Notice)
	}
}

func TestBuildDocumentCancelledAndUndelivered(t *testing.T) {
	questions := normalized(t, []any{map[string]any{"question": "First"}})

	for _, outcome := range []Outcome{OutcomeCancelled, OutcomeUndelivered} {
		document, err := BuildDocument(questions, AdapterResult{
			Answers: map[string]any{"q0": nil},
			Outcome: outcome,
		})
		if err != nil {
			t.Fatal(err)
		}
		if document.Outcome != outcome || document.Responses[0].Status != StatusSkipped {
			t.Fatalf("outcome=%s document=%+v", outcome, document)
		}
	}
}

func TestNormalizeQuestionsValidationErrors(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"not array", map[string]any{}, "questions must be an array"},
		{"empty", []any{}, "at least 1"},
		{
			"too many",
			[]any{
				map[string]any{"question": "1"},
				map[string]any{"question": "2"},
				map[string]any{"question": "3"},
				map[string]any{"question": "4"},
				map[string]any{"question": "5"},
				map[string]any{"question": "6"},
			},
			"at most 5",
		},
		{"not object", []any{"question"}, "must be an object"},
		{"empty question", []any{map[string]any{"question": "   "}}, "must not be empty"},
		{"missing question", []any{map[string]any{}}, "question is required"},
		{"question not string", []any{map[string]any{"question": 1}}, "must be a string"},
		{
			"choices not array",
			[]any{map[string]any{"question": "q", "choices": "A"}},
			"choices must be an array of strings",
		},
		{
			"choice not string",
			[]any{map[string]any{"question": "q", "choices": []any{"A", 2}}},
			"choices[1] must be a string",
		},
		{
			"all choices empty",
			[]any{map[string]any{"question": "q", "choices": []any{"", "  "}}},
			"contains no non-empty options",
		},
		{
			"choice too long",
			[]any{map[string]any{"question": "q", "choices": []any{strings.Repeat("a", MaxChoiceLength+1)}}},
			"exceeds 8000 characters",
		},
		{
			"multi select not bool",
			[]any{map[string]any{"question": "q", "multi_select": "yes"}},
			"must be a boolean",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeQuestions(test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNormalizeQuestionsAcceptsNilEmptyChoicesAsOpen(t *testing.T) {
	for _, choices := range []any{nil, []any{}} {
		questions := normalized(t, []any{map[string]any{
			"question": "Open",
			"choices":  choices,
		}})
		if questions[0].Choices != nil || questions[0].MultiSelect {
			t.Fatalf("choices=%#v normalized=%+v", choices, questions[0])
		}
	}
}

func TestNormalizeQuestionsMultiSelectWithoutChoicesDisabled(t *testing.T) {
	questions := normalized(t, []any{map[string]any{
		"question":     "Open",
		"multi_select": true,
	}})
	if questions[0].MultiSelect {
		t.Fatal("multi-select without choices must be disabled")
	}
}

func TestNormalizeQuestionsChoiceLengthMeasuredAfterTrim(t *testing.T) {
	choice := "  " + strings.Repeat("a", MaxChoiceLength) + "  "
	questions := normalized(t, []any{map[string]any{"question": "q", "choices": []any{choice}}})
	if len(questions[0].Choices) != 1 {
		t.Fatalf("choices = %#v", questions[0].Choices)
	}
}

func TestBuildDocumentRejectsUnknownOutcomeAndInvalidAnswers(t *testing.T) {
	questions := normalized(t, []any{map[string]any{"question": "q"}})
	if _, err := BuildDocument(questions, AdapterResult{Outcome: "nope"}); err == nil {
		t.Fatal("expected invalid outcome error")
	}
	if _, err := BuildDocument(questions, AdapterResult{
		Answers: map[string]any{"q0": 42},
		Outcome: OutcomeSubmitted,
	}); err == nil {
		t.Fatal("expected invalid answer error")
	}
}

func TestMarshalDocumentPreservesUnicodeAndHTMLCharacters(t *testing.T) {
	document, err := BuildDocument(
		normalized(t, []any{map[string]any{"question": "Perché <tag>?"}}),
		AdapterResult{
			Answers: map[string]any{"q0": "città & mare"},
			Outcome: OutcomeSubmitted,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := MarshalDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, `\u003c`) || strings.Contains(payload, `\u0026`) {
		t.Fatalf("payload HTML-escaped: %s", payload)
	}
	if !strings.Contains(payload, "Perché <tag>?") || !strings.Contains(payload, "città & mare") {
		t.Fatalf("payload lost Unicode: %s", payload)
	}
}

func TestMarshalErrorShape(t *testing.T) {
	if got := MarshalError("bad input"); got != `{"error":"bad input"}` {
		t.Fatalf("got %s", got)
	}
}
