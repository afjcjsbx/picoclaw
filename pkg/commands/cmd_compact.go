package commands

import (
	"context"
	"fmt"
	"strings"
)

func compactCommand() Definition {
	return Definition{
		Name:        "compact",
		Description: "Compact the conversation context, optionally focusing the summary on given details",
		Usage:       "/compact [--force] [focus]",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.CompactContext == nil {
				return req.Reply(unavailableMsg)
			}
			args := strings.Fields(req.Text)[1:]
			force := false
			focusArgs := make([]string, 0, len(args))
			for _, arg := range args {
				if arg == "--force" {
					force = true
					continue
				}
				focusArgs = append(focusArgs, arg)
			}
			focus := strings.Join(focusArgs, " ")
			outcome, err := rt.CompactContext(focus, force)
			if err != nil {
				return req.Reply("Failed to compact context: " + err.Error())
			}
			if !outcome.Compacted {
				return req.Reply(compactNoopReply(outcome))
			}
			return req.Reply(compactSuccessReply(outcome))
		},
	}
}

func compactNoopReply(outcome CompactOutcome) string {
	switch outcome.Reason {
	case CompactReasonBelowTarget:
		tokens := outcome.TokensAfter
		if tokens <= 0 {
			tokens = outcome.TokensBefore
		}
		prefix := "Context already within the compaction target"
		if outcome.Target > 0 && tokens > 0 && tokens <= outcome.Target {
			prefix = fmt.Sprintf("Context already within the compaction target (%d/%d history tokens)",
				tokens, outcome.Target)
		}
		return prefix + ". No new summary was needed. Use /compact --force to compact anyway."
	case CompactReasonTooShort:
		return "The conversation is too short to compact."
	case CompactReasonInProgress:
		return "A compaction is already running. Try again shortly."
	case CompactReasonNoCandidates:
		return "No compactable history was found."
	default:
		return "Nothing to compact yet."
	}
}

func compactSuccessReply(outcome CompactOutcome) string {
	var details []string
	if outcome.LeafSummaries > 0 {
		details = append(details, fmt.Sprintf("%d leaf", outcome.LeafSummaries))
	}
	if outcome.CondensedSummaries > 0 {
		details = append(details, fmt.Sprintf("%d condensed", outcome.CondensedSummaries))
	}
	if outcome.TokensBefore > 0 && outcome.TokensAfter > 0 && outcome.TokensAfter < outcome.TokensBefore {
		details = append(details, fmt.Sprintf("history %d -> %d tokens",
			outcome.TokensBefore, outcome.TokensAfter))
	}
	if len(details) == 0 {
		return "Context compacted."
	}
	return "Context compacted (" + strings.Join(details, ", ") + ")."
}
