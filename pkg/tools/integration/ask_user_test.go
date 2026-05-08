package integrationtools

import (
	"context"
	"testing"
)

func TestAskUserTool_Execute(t *testing.T) {
	tool := NewAskUserTool()

	result := tool.Execute(context.Background(), map[string]any{
		"question": "Which output do you prefer?",
		"options":  []any{"short", "", "detailed"},
	})

	if result == nil {
		t.Fatal("expected tool result")
	}
	if !result.AwaitUserInput {
		t.Fatal("expected AwaitUserInput=true")
	}
	if result.ForLLM != askUserWaitingNote {
		t.Fatalf("ForLLM = %q, want %q", result.ForLLM, askUserWaitingNote)
	}
	wantUser := "Which output do you prefer?\n\n1. short\n2. detailed"
	if result.ForUser != wantUser {
		t.Fatalf("ForUser = %q, want %q", result.ForUser, wantUser)
	}
	if len(result.InputOptions) != 2 || result.InputOptions[0] != "short" || result.InputOptions[1] != "detailed" {
		t.Fatalf("InputOptions = %#v", result.InputOptions)
	}
}

func TestAskUserTool_Execute_MissingQuestion(t *testing.T) {
	tool := NewAskUserTool()

	result := tool.Execute(context.Background(), map[string]any{
		"question": "   ",
	})

	if result == nil {
		t.Fatal("expected tool result")
	}
	if !result.IsError {
		t.Fatal("expected IsError=true")
	}
	if result.ForLLM != "question is required" {
		t.Fatalf("ForLLM = %q, want %q", result.ForLLM, "question is required")
	}
}

func TestNormalizeAskUserReply(t *testing.T) {
	options := []string{"short", "detailed"}

	if got := normalizeAskUserReply("2", options); got != "detailed" {
		t.Fatalf("normalizeAskUserReply() = %q, want detailed", got)
	}
	if got := normalizeAskUserReply("custom", options); got != "custom" {
		t.Fatalf("normalizeAskUserReply() = %q, want custom", got)
	}
}
