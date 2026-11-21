// Package usagereport builds the cross-session usage report shown by
// `picoclaw usage` and the web UI from the counters stored with each session.
package usagereport

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/usage"
)

// mainSessionAlias is shared by many sessions, so it never names one of them.
const mainSessionAlias = "agent:main:main"

// SessionRow is the usage of one stored session.
type SessionRow struct {
	Key   string        `json:"key"`
	Label string        `json:"label"`
	Usage usage.Summary `json:"usage"`
}

// Report is the usage of every stored session that recorded any.
type Report struct {
	SessionsDir  string       `json:"sessions_dir"`
	SessionCount int          `json:"session_count"`
	Totals       usage.Totals `json:"totals"`
	// Models splits Totals by the model that served each call, largest first.
	Models []usage.ModelTotals `json:"models"`
	// Sessions lists the sessions, largest consumer first.
	Sessions []SessionRow `json:"sessions"`
	// Period is the recent history from the usage ledger. AddPeriod sets it.
	Period *Period `json:"period,omitempty"`
}

// Period is the usage of the last Days calendar days, today included.
type Period struct {
	Days int `json:"days"`
	// Since is the start of the first day of the period.
	Since time.Time `json:"since"`
	// TrackedSince is when the ledger started. Earlier usage is in the report
	// totals only. It is unset while the ledger is empty.
	TrackedSince *time.Time          `json:"tracked_since,omitempty"`
	Totals       usage.Totals        `json:"totals"`
	Models       []usage.ModelTotals `json:"models"`
	// Daily has one row per day with usage, oldest first.
	Daily []usage.DayTotals `json:"daily"`
}

// AddPeriod attaches the ledger history of the last days days, counted back
// from now in now's location, to the report.
func (r *Report) AddPeriod(sessionsDir string, days int, now time.Time) error {
	if days < 1 {
		return fmt.Errorf("days must be at least 1, got %d", days)
	}
	year, month, day := now.Date()
	since := time.Date(year, month, day-(days-1), 0, 0, 0, 0, now.Location())

	entries, first, err := memory.ReadUsageLedger(sessionsDir, since)
	if err != nil {
		return err
	}
	period := &Period{
		Days:   days,
		Since:  since,
		Models: []usage.ModelTotals{},
		Daily:  usage.DailyTotals(entries, now.Location()),
	}
	for _, e := range entries {
		period.Totals.Merge(e.Totals)
	}
	if models := usage.ModelsOf(entries); len(models) > 0 {
		period.Models = models
	}
	if !first.IsZero() {
		period.TrackedSince = &first
	}
	r.Period = period
	return nil
}

// NewRow converts one stored session into a report row.
func NewRow(entry memory.SessionUsageEntry) SessionRow {
	summary := entry.Usage
	summary.SortModels()
	return SessionRow{
		Key:   entry.Key,
		Label: sessionLabel(entry.Key, entry.Aliases),
		Usage: summary,
	}
}

// Overview aggregates entries into a report. Sessions without usage are left out.
func Overview(sessionsDir string, entries []memory.SessionUsageEntry) Report {
	report := Report{SessionsDir: sessionsDir, Models: []usage.ModelTotals{}, Sessions: []SessionRow{}}
	var all usage.Summary
	for _, entry := range entries {
		if entry.Usage.Empty() {
			continue
		}
		all.Merge(entry.Usage)
		report.Sessions = append(report.Sessions, NewRow(entry))
	}
	all.SortModels()
	report.Totals = all.Totals
	report.SessionCount = len(report.Sessions)
	if len(all.Models) > 0 {
		report.Models = all.Models
	}

	sort.SliceStable(report.Sessions, func(i, j int) bool {
		a, b := report.Sessions[i].Usage, report.Sessions[j].Usage
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.InputTokens != b.InputTokens {
			return a.InputTokens > b.InputTokens
		}
		return report.Sessions[i].Key < report.Sessions[j].Key
	})
	return report
}

// sessionLabel returns a readable name for a session. Opaque keys are shown by
// their shortest alias.
func sessionLabel(key string, aliases []string) string {
	if !session.IsOpaqueSessionKey(key) {
		return key
	}

	best := ""
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == mainSessionAlias {
			continue
		}
		if best == "" || len(alias) < len(best) || (len(alias) == len(best) && alias < best) {
			best = alias
		}
	}
	if best == "" {
		return key
	}
	return best
}
