package commands

import (
	"errors"
	"strings"
	"testing"
)

func TestCompactCommand(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		compacted bool
		err       error
		wantFocus string
		wantReply string
	}{
		{"no focus", "/compact", true, nil, "", "Context compacted."},
		{
			"focus joined", "/compact   focus on  database\nmigration",
			true, nil, "focus on database migration", "Context compacted.",
		},
		{
			"telegram bot suffix", "/compact@picobot keep the API design",
			true, nil, "keep the API design", "Context compacted.",
		},
		{"nothing to compact", "/compact", false, nil, "", "Nothing to compact yet."},
		{"error", "/compact", false, errors.New("boom"), "", "Failed to compact context: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotFocus string
			rt := &Runtime{CompactContext: func(focus string) (bool, error) {
				gotFocus = focus
				return tt.compacted, tt.err
			}}
			var reply string
			res := NewExecutor(NewRegistry(BuiltinDefinitions()), rt).Execute(t.Context(), Request{
				Text:  tt.text,
				Reply: func(s string) error { reply = s; return nil },
			})
			if res.Outcome != OutcomeHandled || res.Command != "compact" {
				t.Fatalf("result=%+v, want handled compact", res)
			}
			if gotFocus != tt.wantFocus {
				t.Fatalf("focus=%q, want=%q", gotFocus, tt.wantFocus)
			}
			if reply != tt.wantReply {
				t.Fatalf("reply=%q, want=%q", reply, tt.wantReply)
			}
		})
	}
}

func TestCompactCommand_Unavailable(t *testing.T) {
	var reply string
	NewExecutor(NewRegistry(BuiltinDefinitions()), &Runtime{}).Execute(t.Context(), Request{
		Text:  "/compact",
		Reply: func(s string) error { reply = s; return nil },
	})
	if !strings.Contains(reply, unavailableMsg) {
		t.Fatalf("reply=%q, want %q", reply, unavailableMsg)
	}
}
