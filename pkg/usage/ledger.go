package usage

import (
	"sort"
	"time"
)

// LedgerEntry is the usage one assistant message recorded for one model. The
// ledger is the only record of when usage happened: session counters hold
// running totals without timestamps.
type LedgerEntry struct {
	Time    time.Time `json:"ts"`
	Session string    `json:"session"`
	Model   string    `json:"model"`
	Totals
}

// DayTotals is the usage of one calendar day.
type DayTotals struct {
	// Date is the day as YYYY-MM-DD in the location passed to DailyTotals.
	Date string `json:"date"`
	Totals
}

// DateLayout formats the Date of a DayTotals.
const DateLayout = "2006-01-02"

// DailyTotals sums entries per calendar day in loc, oldest day first. Days
// without entries are left out.
func DailyTotals(entries []LedgerEntry, loc *time.Location) []DayTotals {
	byDate := make(map[string]*DayTotals)
	for _, e := range entries {
		date := e.Time.In(loc).Format(DateLayout)
		day, ok := byDate[date]
		if !ok {
			day = &DayTotals{Date: date}
			byDate[date] = day
		}
		day.Merge(e.Totals)
	}

	days := make([]DayTotals, 0, len(byDate))
	for _, day := range byDate {
		days = append(days, *day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Date < days[j].Date })
	return days
}

// ModelsOf sums entries per model, largest consumer first.
func ModelsOf(entries []LedgerEntry) []ModelTotals {
	var summary Summary
	for _, e := range entries {
		summary.row(e.Model).Merge(e.Totals)
	}
	summary.SortModels()
	return summary.Models
}
