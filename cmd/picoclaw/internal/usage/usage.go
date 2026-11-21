package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal/cliui"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/usage/usagereport"
)

// runUsage writes the usage report for the sessions stored under sessionsDir.
func runUsage(out io.Writer, sessionsDir string, opts options) error {
	if opts.Days < 0 {
		return fmt.Errorf("--days must not be negative, got %d", opts.Days)
	}
	if opts.Days > 0 && strings.TrimSpace(opts.Session) != "" {
		return errors.New("--days cannot be combined with --session")
	}
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
	rep := usagereport.Overview(sessionsDir, entries)
	if opts.Days > 0 {
		if err := rep.AddPeriod(sessionsDir, opts.Days, time.Now()); err != nil {
			return err
		}
	}
	rows := rep.Sessions
	limited := opts.Limit > 0 && opts.Limit < rep.SessionCount
	if limited {
		rows = rows[:opts.Limit]
	}

	if opts.JSON {
		rep.Sessions = rows
		return writeJSON(out, rep)
	}
	if rep.SessionCount == 0 && (rep.Period == nil || rep.Period.TrackedSince == nil) {
		fmt.Fprintf(out, "No token usage recorded under %s.\n", sessionsDir)
		return nil
	}

	displayed := make([]cliui.UsageRow, 0, len(rows))
	for _, row := range rows {
		displayed = append(displayed, cliui.UsageRow{Label: row.Label, Totals: row.Usage.Totals})
	}
	cliui.PrintUsageOverview(out, cliui.UsageOverview{
		SessionsDir:  sessionsDir,
		SessionCount: rep.SessionCount,
		Totals:       rep.Totals,
		Models:       rep.Models,
		Rows:         displayed,
		Limited:      limited,
		Period:       usagePeriod(rep.Period),
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
	row := usagereport.NewRow(entry)

	if asJSON {
		rep := usagereport.Report{SessionsDir: sessionsDir, Sessions: []usagereport.SessionRow{}}
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

func usagePeriod(p *usagereport.Period) *cliui.UsagePeriod {
	if p == nil {
		return nil
	}
	period := &cliui.UsagePeriod{
		Days:   p.Days,
		Since:  p.Since,
		Totals: p.Totals,
		Daily:  p.Daily,
	}
	if p.TrackedSince != nil {
		period.TrackedSince = *p.TrackedSince
	}
	return period
}

func writeJSON(out io.Writer, v any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}
