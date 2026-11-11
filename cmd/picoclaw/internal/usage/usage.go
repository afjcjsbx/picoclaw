package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal/cliui"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/session"
	usagepkg "github.com/sipeed/picoclaw/pkg/usage"
)

// mainSessionAlias is shared by many sessions, so it never names one of them.
const mainSessionAlias = "agent:main:main"

type sessionRow struct {
	Key   string           `json:"key"`
	Label string           `json:"label"`
	Usage usagepkg.Summary `json:"usage"`
}

type report struct {
	SessionsDir  string          `json:"sessions_dir"`
	SessionCount int             `json:"session_count"`
	Totals       usagepkg.Totals `json:"totals"`
	Sessions     []sessionRow    `json:"sessions"`
}

// runUsage writes the usage report for the sessions stored under sessionsDir.
func runUsage(out io.Writer, sessionsDir string, opts options) error {
	entries, err := memory.ReadSessionUsage(sessionsDir)
	if err != nil {
		return err
	}
	if query := strings.TrimSpace(opts.Session); query != "" {
		return runSessionUsage(out, sessionsDir, entries, query, opts.JSON)
	}
	return runOverview(out, sessionsDir, entries, opts)
}

func runOverview(out io.Writer, sessionsDir string, entries []memory.SessionUsageEntry, opts options) error {
	rows := []sessionRow{}
	var totals usagepkg.Totals
	for _, entry := range entries {
		if entry.Usage.Empty() {
			continue
		}
		totals.Merge(entry.Usage.Totals)
		rows = append(rows, newSessionRow(entry))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].Usage, rows[j].Usage
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.InputTokens != b.InputTokens {
			return a.InputTokens > b.InputTokens
		}
		return rows[i].Key < rows[j].Key
	})

	sessionCount := len(rows)
	limited := opts.Limit > 0 && opts.Limit < sessionCount
	if limited {
		rows = rows[:opts.Limit]
	}

	if opts.JSON {
		return writeJSON(out, report{
			SessionsDir:  sessionsDir,
			SessionCount: sessionCount,
			Totals:       totals,
			Sessions:     rows,
		})
	}
	if sessionCount == 0 {
		fmt.Fprintf(out, "No token usage recorded under %s.\n", sessionsDir)
		return nil
	}

	displayed := make([]cliui.UsageRow, 0, len(rows))
	for _, row := range rows {
		displayed = append(displayed, cliui.UsageRow{Label: row.Label, Totals: row.Usage.Totals})
	}
	cliui.PrintUsageOverview(out, cliui.UsageOverview{
		SessionsDir:  sessionsDir,
		SessionCount: sessionCount,
		Totals:       totals,
		Rows:         displayed,
		Limited:      limited,
	})
	return nil
}

func runSessionUsage(
	out io.Writer,
	sessionsDir string,
	entries []memory.SessionUsageEntry,
	query string,
	asJSON bool,
) error {
	entry, err := matchSession(sessionsDir, entries, query)
	if err != nil {
		return err
	}
	row := newSessionRow(entry)

	if asJSON {
		rep := report{SessionsDir: sessionsDir, Sessions: []sessionRow{}}
		if !row.Usage.Empty() {
			rep.SessionCount = 1
			rep.Totals = row.Usage.Totals
			rep.Sessions = append(rep.Sessions, row)
		}
		return writeJSON(out, rep)
	}
	if row.Usage.Empty() {
		fmt.Fprintf(out, "No token usage recorded for session %q.\n", query)
		return nil
	}
	cliui.PrintUsageSession(out, cliui.UsageSession{
		SessionsDir: sessionsDir,
		Label:       row.Label,
		Totals:      row.Usage.Totals,
		Models:      row.Usage.Models,
	})
	return nil
}

// matchSession resolves the -s argument to one stored session the way the agent
// does. An opaque session key names itself. Otherwise an alias names the
// canonical session that lists it: legacy sessions can list an alias they never
// owned, so only canonical sessions claim aliases. A legacy session key matches
// last.
func matchSession(
	sessionsDir string,
	entries []memory.SessionUsageEntry,
	query string,
) (memory.SessionUsageEntry, error) {
	for _, entry := range entries {
		if entry.Key == query && session.IsOpaqueSessionKey(entry.Key) {
			return entry, nil
		}
	}

	var owners []memory.SessionUsageEntry
	for _, entry := range entries {
		if session.IsOpaqueSessionKey(entry.Key) && slices.Contains(entry.Aliases, query) {
			owners = append(owners, entry)
		}
	}
	switch len(owners) {
	case 0:
	case 1:
		return owners[0], nil
	default:
		keys := make([]string, 0, len(owners))
		for _, owner := range owners {
			keys = append(keys, owner.Key)
		}
		sort.Strings(keys)
		return memory.SessionUsageEntry{}, fmt.Errorf(
			"alias %q matches several sessions (%s); pass a session key",
			query, strings.Join(keys, ", "))
	}

	for _, entry := range entries {
		if entry.Key == query {
			return entry, nil
		}
	}
	return memory.SessionUsageEntry{}, fmt.Errorf("session %q not found under %s", query, sessionsDir)
}

func newSessionRow(entry memory.SessionUsageEntry) sessionRow {
	summary := entry.Usage
	summary.SortModels()
	return sessionRow{
		Key:   entry.Key,
		Label: sessionLabel(entry.Key, entry.Aliases),
		Usage: summary,
	}
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

func writeJSON(out io.Writer, v any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}
