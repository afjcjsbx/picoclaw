package cliui

import (
	"fmt"
	"io"

	"github.com/sipeed/picoclaw/pkg/usage"
)

// UsageOverview is the data of a report covering every session.
type UsageOverview struct {
	SessionsDir string
	// SessionCount is the number of sessions that recorded usage.
	SessionCount int
	Totals       usage.Totals
	// Rows lists the sessions shown, largest consumer first.
	Rows []UsageRow
	// Limited is true when Rows is only the first part of the sessions.
	Limited bool
}

// UsageRow is one session row of an overview.
type UsageRow struct {
	Label  string
	Totals usage.Totals
}

// UsageSession is the data of a report covering one session.
type UsageSession struct {
	SessionsDir string
	Label       string
	Totals      usage.Totals
	Models      []usage.ModelTotals
}

// PrintUsageOverview renders the overview as plain text.
func PrintUsageOverview(w io.Writer, overview UsageOverview) {
	fmt.Fprintf(w, "Usage summary\n%s\n\nSessions: %d\n", overview.SessionsDir, overview.SessionCount)
	printUsageTotals(w, overview.Totals)
	if len(overview.Rows) == 0 {
		return
	}

	rows := make([][]string, 0, len(overview.Rows))
	for _, row := range overview.Rows {
		rows = append(rows, usageCells(row.Label, row.Totals))
	}
	fmt.Fprintln(w)
	if overview.Limited {
		fmt.Fprintf(w, "Sessions by total tokens (top %d of %d)\n", len(overview.Rows), overview.SessionCount)
	} else {
		fmt.Fprintf(w, "Sessions by total tokens (%d)\n", len(overview.Rows))
	}
	printPlainTable(w, usageHeaders("Session"), rows)
	if overview.Limited {
		fmt.Fprintf(w, "\nUse --limit 0 to list all %d sessions.\n", overview.SessionCount)
	}
}

// PrintUsageSession renders one session as plain text, with its per-model split.
func PrintUsageSession(w io.Writer, session UsageSession) {
	fmt.Fprintf(w, "Usage for session %s\n%s\n\n", session.Label, session.SessionsDir)
	printUsageTotals(w, session.Totals)
	if len(session.Models) == 0 {
		return
	}

	rows := make([][]string, 0, len(session.Models))
	for _, model := range session.Models {
		rows = append(rows, usageCells(model.Model, model.Totals))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "By model")
	printPlainTable(w, usageHeaders("Model"), rows)
}

func printUsageTotals(w io.Writer, totals usage.Totals) {
	fmt.Fprintf(w, "LLM calls: %d · turns: %d\n", totals.Requests, totals.Turns)
	fmt.Fprintf(w, "Input:  %s\n", totals.InputBreakdown())
	fmt.Fprintf(w, "Output: %s tokens\n", usage.FormatTokens(totals.OutputTokens))
	fmt.Fprintf(w, "Total:  %s tokens\n", usage.FormatTokens(totals.TotalTokens))
	fmt.Fprintf(w, "Cost:   %s\n", usage.FormatCost(totals.CostUSD))
}

func usageHeaders(first string) []string {
	return []string{first, "Calls", "Input", "Output", "Total", "Cost"}
}

func usageCells(label string, totals usage.Totals) []string {
	return []string{
		label,
		fmt.Sprintf("%d", totals.Requests),
		usage.FormatTokens(totals.InputTokens),
		usage.FormatTokens(totals.OutputTokens),
		usage.FormatTokens(totals.TotalTokens),
		usage.FormatCost(totals.CostUSD),
	}
}
