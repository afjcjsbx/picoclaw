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
		outcome   CompactOutcome
		err       error
		wantFocus string
		wantForce bool
		wantReply string
	}{
		{
			"no focus",
			"/compact",
			CompactOutcome{Compacted: true},
			nil, "", false, "Context compacted.",
		},
		{
			"focus joined",
			"/compact   focus on  database\nmigration",
			CompactOutcome{Compacted: true},
			nil, "focus on database migration", false, "Context compacted.",
		},
		{
			"telegram bot suffix",
			"/compact@picobot keep the API design",
			CompactOutcome{Compacted: true},
			nil, "keep the API design", false, "Context compacted.",
		},
		{
			"force with focus",
			"/compact --force keep the API design",
			CompactOutcome{Compacted: true},
			nil, "keep the API design", true, "Context compacted.",
		},
		{
			"success details",
			"/compact",
			CompactOutcome{
				Compacted: true, LeafSummaries: 2, CondensedSummaries: 1,
				TokensBefore: 12000, TokensAfter: 8000,
			},
			nil, "", false, "Context compacted (2 leaf, 1 condensed, history 12000 -> 8000 tokens).",
		},
		{
			"below target",
			"/compact",
			CompactOutcome{
				Reason: CompactReasonBelowTarget, TokensBefore: 3400,
				TokensAfter: 3300, Target: 3327,
			},
			nil, "", false,
			"Context already within the compaction target (3300/3327 history tokens). " +
				"No new summary was needed. Use /compact --force to compact anyway.",
		},
		{
			"below target without numbers",
			"/compact",
			CompactOutcome{Reason: CompactReasonBelowTarget},
			nil, "", false,
			"Context already within the compaction target. " +
				"No new summary was needed. Use /compact --force to compact anyway.",
		},
		{
			"too short",
			"/compact",
			CompactOutcome{Reason: CompactReasonTooShort},
			nil, "", false, "The conversation is too short to compact.",
		},
		{
			"in progress",
			"/compact",
			CompactOutcome{Reason: CompactReasonInProgress},
			nil, "", false, "A compaction is already running. Try again shortly.",
		},
		{
			"no candidates",
			"/compact",
			CompactOutcome{Reason: CompactReasonNoCandidates},
			nil, "", false, "No compactable history was found.",
		},
		{
			"error",
			"/compact",
			CompactOutcome{},
			errors.New("boom"), "", false, "Failed to compact context: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotFocus string
			var gotForce bool
			rt := &Runtime{CompactContext: func(focus string, force bool) (CompactOutcome, error) {
				gotFocus = focus
				gotForce = force
				return tt.outcome, tt.err
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
			if gotForce != tt.wantForce {
				t.Fatalf("force=%v, want=%v", gotForce, tt.wantForce)
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
