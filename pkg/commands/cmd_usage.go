package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/usage"
)

func usageCommand() Definition {
	return Definition{
		Name:        "usage",
		Description: "Show token usage and cost for this session",
		Usage:       "/usage",
		Aliases:     []string{"cost"},
		ReadOnly:    true,
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.GetUsageStats == nil {
				return req.Reply(unavailableMsg)
			}
			summary, err := rt.GetUsageStats()
			if err != nil {
				return req.Reply("Failed to read session usage: " + err.Error())
			}
			return req.Reply(FormatUsageReply(summary))
		},
	}
}

// FormatUsageReply renders a session usage summary for chat channels. The
// breakdown separates fresh prompt tokens from cache reads and cache writes so
// callers can see where the input tokens went.
func FormatUsageReply(summary *usage.Summary) string {
	if summary == nil || summary.Empty() {
		return "No token usage recorded for this session yet."
	}

	var lines []string
	lines = append(lines,
		"Usage for this session",
		fmt.Sprintf("LLM calls: %d · turns: %d", summary.Requests, summary.Turns),
		"Input: "+summary.InputBreakdown(),
		"Output: "+usage.FormatTokens(summary.OutputTokens)+" tokens",
		"Total: "+usage.FormatTokens(summary.TotalTokens)+" tokens",
		"Cost: "+formatUsageCost(summary.CostUSD),
	)

	if len(summary.Models) == 1 {
		lines = append(lines, "Model: `"+summary.Models[0].Model+"`")
	}
	if len(summary.Models) > 1 {
		lines = append(lines, "", "By model:")
		for _, model := range summary.Models {
			lines = append(lines, formatUsageModel(model))
		}
	}

	return strings.Join(lines, "  \n")
}

func formatUsageCost(cost float64) string {
	if cost <= 0 {
		return "not reported by provider"
	}
	return usage.FormatCostUSD(cost)
}

func formatUsageModel(model usage.ModelTotals) string {
	cost := usage.FormatCostUSD(model.CostUSD)
	if model.CostUSD <= 0 {
		cost = "cost not reported"
	}
	return fmt.Sprintf(
		"- `%s` — %s · %s in / %s out · %s",
		model.Model,
		formatUsageCalls(model.Requests),
		usage.FormatTokens(model.InputTokens),
		usage.FormatTokens(model.OutputTokens),
		cost,
	)
}

func formatUsageCalls(calls int) string {
	if calls == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", calls)
}
