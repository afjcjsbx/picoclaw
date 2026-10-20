package messageutil

import (
	"slices"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

func TestCloneHistory_EmptyInput(t *testing.T) {
	for name, input := range map[string][]protocoltypes.Message{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			cloned := CloneHistory(input)
			if cloned == nil {
				t.Fatal("CloneHistory returned nil, want non-nil empty slice")
			}
			if len(cloned) != 0 {
				t.Fatalf("len(CloneHistory) = %d, want 0", len(cloned))
			}
		})
	}
}

func TestCloneHistory_IsolatesNestedFields(t *testing.T) {
	original := []protocoltypes.Message{sampleMessage()}

	// Mutating every nested field of the clone must not touch the original.
	cloned := CloneHistory(original)
	mutateMessage(&cloned[0])
	assertSampleMessage(t, original[0])

	// Mutating the original afterwards must not touch a fresh clone.
	fresh := CloneHistory(original)
	mutateMessage(&original[0])
	assertSampleMessage(t, fresh[0])
}

func TestCloneHistory_CopiesSliceHeader(t *testing.T) {
	original := []protocoltypes.Message{{Role: "user", Content: "hello"}}
	cloned := CloneHistory(original)

	cloned[0].Role = "mutated"
	if original[0].Role != "user" {
		t.Fatalf("original[0].Role = %q, want user", original[0].Role)
	}
}

func sampleMessage() protocoltypes.Message {
	createdAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	return protocoltypes.Message{
		Role:      "assistant",
		Content:   "hello",
		CreatedAt: &createdAt,
		Media:     []string{"media-1", "media-2"},
		Attachments: []protocoltypes.Attachment{
			{Type: "image", Filename: "a.png"},
		},
		SystemParts: []protocoltypes.ContentBlock{
			{Type: "text", Text: "system", CacheControl: &protocoltypes.CacheControl{Type: "ephemeral"}},
		},
		ToolCalls: []protocoltypes.ToolCall{
			{
				ID:       "call-1",
				Function: &protocoltypes.FunctionCall{Name: "lookup", Arguments: `{"q":"x"}`},
				Arguments: map[string]any{
					"nested": map[string]any{"deep": "value"},
					"list":   []any{"a", map[string]any{"b": true}},
					"scalar": "keep",
				},
				ExtraContent: &protocoltypes.ExtraContent{
					Google:                  &protocoltypes.GoogleExtra{ThoughtSignature: "sig"},
					ToolFeedbackExplanation: "why",
				},
			},
		},
	}
}

func mutateMessage(msg *protocoltypes.Message) {
	msg.Role = "mutated"
	msg.Content = "mutated"
	if msg.CreatedAt != nil {
		*msg.CreatedAt = msg.CreatedAt.Add(24 * time.Hour)
	}
	msg.Media[0] = "mutated"
	msg.Attachments[0].Filename = "mutated"
	msg.SystemParts[0].Text = "mutated"
	msg.SystemParts[0].CacheControl.Type = "mutated"
	msg.ToolCalls[0].Function.Name = "mutated"
	msg.ToolCalls[0].Function.Arguments = "{}"
	msg.ToolCalls[0].Arguments["scalar"] = "mutated"
	msg.ToolCalls[0].Arguments["nested"].(map[string]any)["deep"] = "mutated"
	msg.ToolCalls[0].Arguments["list"].([]any)[0] = "mutated"
	msg.ToolCalls[0].Arguments["list"].([]any)[1].(map[string]any)["b"] = false
	msg.ToolCalls[0].ExtraContent.Google.ThoughtSignature = "mutated"
	msg.ToolCalls[0].ExtraContent.ToolFeedbackExplanation = "mutated"
}

func assertSampleMessage(t *testing.T, msg protocoltypes.Message) {
	t.Helper()

	if msg.Role != "assistant" || msg.Content != "hello" {
		t.Fatalf("message identity mutated: role=%q content=%q", msg.Role, msg.Content)
	}
	if msg.CreatedAt == nil {
		t.Fatal("CreatedAt = nil, want 2026-02-03T04:05:06Z")
	}
	if want := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC); !msg.CreatedAt.Equal(want) {
		t.Fatalf("CreatedAt = %v, want %v", msg.CreatedAt, want)
	}
	if want := []string{"media-1", "media-2"}; !slices.Equal(msg.Media, want) {
		t.Fatalf("Media = %v, want %v", msg.Media, want)
	}
	if want := []protocoltypes.Attachment{{Type: "image", Filename: "a.png"}}; !slices.Equal(msg.Attachments, want) {
		t.Fatalf("Attachments = %+v, want %+v", msg.Attachments, want)
	}
	if len(msg.SystemParts) != 1 || msg.SystemParts[0].Text != "system" {
		t.Fatalf("SystemParts = %+v, want one text block", msg.SystemParts)
	}
	if msg.SystemParts[0].CacheControl == nil || msg.SystemParts[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("SystemParts[0].CacheControl = %+v, want ephemeral", msg.SystemParts[0].CacheControl)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want one call", msg.ToolCalls)
	}
	call := msg.ToolCalls[0]
	if call.ID != "call-1" || call.Function == nil || call.Function.Name != "lookup" ||
		call.Function.Arguments != `{"q":"x"}` {
		t.Fatalf("ToolCalls[0] = %+v, want call-1 with function lookup", call)
	}
	if got := call.Arguments["scalar"]; got != "keep" {
		t.Fatalf("Arguments[scalar] = %v, want keep", got)
	}
	nested, ok := call.Arguments["nested"].(map[string]any)
	if !ok || nested["deep"] != "value" {
		t.Fatalf("Arguments[nested] = %v, want map with deep=value", call.Arguments["nested"])
	}
	list, ok := call.Arguments["list"].([]any)
	if !ok || len(list) != 2 || list[0] != "a" {
		t.Fatalf("Arguments[list] = %v, want [a, {b:true}]", call.Arguments["list"])
	}
	inner, ok := list[1].(map[string]any)
	if !ok || inner["b"] != true {
		t.Fatalf("Arguments[list][1] = %v, want map with b=true", list[1])
	}
	if call.ExtraContent == nil || call.ExtraContent.ToolFeedbackExplanation != "why" {
		t.Fatalf("ExtraContent = %+v, want explanation why", call.ExtraContent)
	}
	if call.ExtraContent.Google == nil || call.ExtraContent.Google.ThoughtSignature != "sig" {
		t.Fatalf("ExtraContent.Google = %+v, want signature sig", call.ExtraContent.Google)
	}
}
