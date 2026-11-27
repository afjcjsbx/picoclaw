package utils

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestBuildVisibleToolCalls_KeepsFullInputAlongsidePreview(t *testing.T) {
	toolCalls := []providers.ToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: &providers.FunctionCall{
			Name:      "read_file",
			Arguments: `{"path":"README.md","extra":"abcdefghijklmnopqrstuvwxyz"}`,
		},
	}}

	visible := BuildVisibleToolCalls(toolCalls, 20)
	if len(visible) != 1 {
		t.Fatalf("len(visible) = %d, want 1", len(visible))
	}
	if visible[0].Function == nil {
		t.Fatalf("visible function = nil, want full input and preview")
	}
	if visible[0].Function.Arguments == visible[0].Input {
		t.Fatalf(
			"input = %q, want full arguments distinct from preview %q",
			visible[0].Input,
			visible[0].Function.Arguments,
		)
	}
	if !strings.Contains(visible[0].Input, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("input = %q, want complete arguments", visible[0].Input)
	}
}

func TestBuildVisibleToolCalls_DoesNotTruncateExplanation(t *testing.T) {
	explanation := "Read README.md first to confirm the current project structure before editing the config example."
	toolCalls := []providers.ToolCall{{
		ID:   "call_1",
		Type: "function",
		Function: &providers.FunctionCall{
			Name:      "read_file",
			Arguments: `{"path":"README.md","start_line":1,"end_line":10,"extra":"abcdefghijklmnopqrstuvwxyz"}`,
		},
		ExtraContent: &providers.ExtraContent{
			ToolFeedbackExplanation: explanation,
		},
	}}

	visible := BuildVisibleToolCalls(toolCalls, 20)
	if len(visible) != 1 {
		t.Fatalf("len(visible) = %d, want 1", len(visible))
	}
	if visible[0].ExtraContent == nil || visible[0].ExtraContent.ToolFeedbackExplanation != explanation {
		t.Fatalf("visible explanation = %#v, want %q", visible[0].ExtraContent, explanation)
	}
	if visible[0].Function == nil || visible[0].Function.Arguments == "" {
		t.Fatalf("visible function = %#v, want truncated args preview", visible[0].Function)
	}
}
