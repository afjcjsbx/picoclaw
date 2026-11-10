package cliui

import (
	"fmt"
	"io"

	"github.com/sipeed/picoclaw/pkg/usage"
)

// UsageReport is the data rendered by PrintUsageReport.
type UsageReport struct {
	SessionsDir string
	// Session is set when the report covers a single session.
	Session  string
	Sessions int
	Totals   usage.Totals
	Models   []usage.ModelTotals
	Rows     []UsageRow
	// Limit is the requested row cap; <= 0 means all rows were shown.
	Limit int
}

// UsageRow is one session row in an overview report.
type UsageRow struct {
	Label  string
	Totals usage.Totals
}

// PrintUsageReport renders a plain, low-allocation usage report. It reuses the
// MCP list table so the CLI has one table implementation.
func PrintUsageReport(w io.Writer, report UsageReport) {
	if report.Session != "" {
		fmt.Fprintf(w, "Usage for session %s\n%s\n\n", report.Session, report.SessionsDir)
		printUsageTotals(w, report.Totals)
		if len(report.Models) > 0 {
			rows := make([][]string, 0, len(report.Models))
			for _, model := range report.Models {
				rows = append(rows, []string{
					model.Model,
					fmt.Sprintf("%d", model.Requests),
					usage.FormatTokens(model.InputTokens),
					usage.FormatTokens(model.OutputTokens),
					usage.FormatTokens(model.TotalTokens),
					formatUsageCostCell(model.CostUSD),
				})
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, "By model")
			printPlainTable(w, []string{"Model", "Calls", "Input", "Output", "Total", "Cost"}, rows)
		}
		return
	}

	fmt.Fprintf(w, "Usage summary\n%s\n\nSessions: %d\n", report.SessionsDir, report.Sessions)
	printUsageTotals(w, report.Totals)
	if len(report.Rows) == 0 {
		return
	}

	rows := make([][]string, 0, len(report.Rows))
	for _, row := range report.Rows {
		rows = append(rows, []string{
			row.Label,
			fmt.Sprintf("%d", row.Totals.Requests),
			usage.FormatTokens(row.Totals.InputTokens),
			usage.FormatTokens(row.Totals.OutputTokens),
			usage.FormatTokens(row.Totals.TotalTokens),
			formatUsageCostCell(row.Totals.CostUSD),
		})
	}

	fmt.Fprintln(w)
	if report.Limit > 0 && report.Sessions > len(report.Rows) {
		fmt.Fprintf(w, "Sessions by total tokens (top %d of %d)\n", len(report.Rows), report.Sessions)
	} else {
		fmt.Fprintf(w, "Sessions by total tokens (%d)\n", len(report.Rows))
	}
	printPlainTable(w, []string{"Session", "Calls", "Input", "Output", "Total", "Cost"}, rows)
	if report.Limit > 0 && report.Sessions > len(report.Rows) {
		fmt.Fprintf(w, "\nUse --limit 0 to list all %d sessions.\n", report.Sessions)
	}
}

func printUsageTotals(w io.Writer, totals usage.Totals) {
	fmt.Fprintf(w, "LLM calls: %d · turns: %d\n", totals.Requests, totals.Turns)
	fmt.Fprintf(w, "Input:  %s\n", totals.InputBreakdown())
	fmt.Fprintf(w, "Output: %s tokens\n", usage.FormatTokens(totals.OutputTokens))
	fmt.Fprintf(w, "Total:  %s tokens\n", usage.FormatTokens(totals.TotalTokens))
	fmt.Fprintf(w, "Cost:   %s\n", formatUsageCostCell(totals.CostUSD))
}

func formatUsageCostCell(cost float64) string {
	if cost <= 0 {
		return "-"
	}
	return usage.FormatCostUSD(cost)
}
