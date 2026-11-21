// Package usagereport builds the cross-session usage report shown by
// `picoclaw usage` and the web UI from the counters stored with each session.
package usagereport

import (
	"sort"
	"strings"

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
	// Sessions lists the sessions, largest consumer first.
	Sessions []SessionRow `json:"sessions"`
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
	report := Report{SessionsDir: sessionsDir, Sessions: []SessionRow{}}
	for _, entry := range entries {
		if entry.Usage.Empty() {
			continue
		}
		report.Totals.Merge(entry.Usage.Totals)
		report.Sessions = append(report.Sessions, NewRow(entry))
	}
	report.SessionCount = len(report.Sessions)

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
