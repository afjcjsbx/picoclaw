package cliui

import (
	"fmt"
	"io"
	"time"

	"github.com/sipeed/picoclaw/pkg/usage"
)

// UsageOverview is the data of a report covering every session.
type UsageOverview struct {
	SessionsDir string
	// SessionCount is the number of sessions that recorded usage.
	SessionCount int
	Totals       usage.Totals
	// Models splits Totals by model, largest first.
	Models []usage.ModelTotals
	// Rows lists the sessions shown, largest consumer first.
	Rows []UsageRow
	// Limited is true when Rows is only the first part of the sessions.
	Limited bool
	// Period is the recent history from the usage ledger, or nil when not asked for.
	Period *UsagePeriod
}

// UsagePeriod is the usage of the last Days calendar days.
type UsagePeriod struct {
	Days  int
	Since time.Time
	// TrackedSince is when the usage ledger started; zero while it is empty.
	TrackedSince time.Time
	Totals       usage.Totals
	// Daily has one row per day with usage, oldest first.
	Daily []usage.DayTotals
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
	printModelTable(w, overview.Models)
	if len(overview.Rows) > 0 {
		printUsageSessions(w, overview)
	}
	if overview.Period != nil {
		printUsagePeriod(w, overview.Period)
	}
}

func printUsageSessions(w io.Writer, overview UsageOverview) {
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

func printUsagePeriod(w io.Writer, period *UsagePeriod) {
	fmt.Fprintf(w, "\nLast %d days (since %s)\n", period.Days, period.Since.Format(usage.DateLayout))
	if period.TrackedSince.IsZero() {
		fmt.Fprintln(w, "No history recorded yet; it starts with the next message that reports usage.")
		return
	}
	fmt.Fprintf(w, "History recorded since %s\n", period.TrackedSince.Format(usage.DateLayout))
	if period.Totals.Empty() {
		fmt.Fprintln(w, "No token usage in this period.")
		return
	}
	printUsageTotals(w, period.Totals)

	rows := make([][]string, 0, len(period.Daily))
	for _, day := range period.Daily {
		rows = append(rows, usageCells(day.Date, day.Totals))
	}
	fmt.Fprintln(w)
	printPlainTable(w, usageHeaders("Day"), rows)
}

// PrintUsageSession renders one session as plain text, with its per-model split.
func PrintUsageSession(w io.Writer, session UsageSession) {
	fmt.Fprintf(w, "Usage for session %s\n%s\n\n", session.Label, session.SessionsDir)
	printUsageTotals(w, session.Totals)
	printModelTable(w, session.Models)
}

func printModelTable(w io.Writer, models []usage.ModelTotals) {
	if len(models) == 0 {
		return
	}
	rows := make([][]string, 0, len(models))
	for _, model := range models {
		rows = append(rows, usageCells(model.Model, model.Totals))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "By model")
	printPlainTable(w, usageHeaders("Model"), rows)
}

func printUsageTotals(w io.Writer, totals usage.Totals) {
	fmt.Fprintf(w, "LLM calls: %d · turns: %d\n", totals.Requests, totals.Turns)
	fmt.Fprintf(w, "Input:  %s\n", totals.InputBreakdown())
	fmt.Fprintf(w, "Output: %s\n", totals.OutputBreakdown())
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
		formatCostCell(totals.CostUSD),
	}
}

// formatCostCell keeps the table compact: a dash marks a provider that reported
// no cost.
func formatCostCell(cost float64) string {
	if cost <= 0 {
		return "-"
	}
	return usage.FormatCost(cost)
}
