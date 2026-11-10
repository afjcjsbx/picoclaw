package usage

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal/cliui"
	usagepkg "github.com/sipeed/picoclaw/pkg/usage"
)

type sessionRow struct {
	Key   string           `json:"key"`
	Label string           `json:"-"`
	Usage usagepkg.Summary `json:"usage"`
}

type report struct {
	SessionsDir string          `json:"sessions_dir"`
	Totals      usagepkg.Totals `json:"totals"`
	Sessions    []sessionRow    `json:"sessions"`
}

func runUsage(out io.Writer, sessionsDir string, opts options) error {
	filter := strings.TrimSpace(opts.Session)
	detail := filter != ""

	var rows []sessionRow
	var detailSession sessionUsage
	var totals usagepkg.Totals
	found := false

	err := scanSessions(sessionsDir, filter, detail, func(session sessionUsage) {
		found = true
		if session.Summary.Empty() {
			return
		}
		if detail {
			detailSession = session
			detailSession.Summary.SortModels()
			return
		}
		totals.Merge(session.Summary.Totals)
		rows = append(rows, sessionRow{
			Key:   session.Key,
			Label: sessionLabel(session.Key, session.Aliases),
			Usage: usagepkg.Summary{Totals: session.Summary.Totals},
		})
	})
	if err != nil {
		return err
	}
	if detail {
		if !found {
			return fmt.Errorf("session %q not found under %s", filter, sessionsDir)
		}
		totals = detailSession.Summary.Totals
		if !detailSession.Summary.Empty() {
			rows = append(rows, sessionRow{
				Key:   detailSession.Key,
				Label: sessionLabel(detailSession.Key, detailSession.Aliases),
				Usage: detailSession.Summary,
			})
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Usage.TotalTokens != rows[j].Usage.TotalTokens {
			return rows[i].Usage.TotalTokens > rows[j].Usage.TotalTokens
		}
		if rows[i].Usage.InputTokens != rows[j].Usage.InputTokens {
			return rows[i].Usage.InputTokens > rows[j].Usage.InputTokens
		}
		return rows[i].Key < rows[j].Key
	})

	if opts.JSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report{
			SessionsDir: sessionsDir,
			Totals:      totals,
			Sessions:    rows,
		})
	}

	if len(rows) == 0 {
		if detail {
			fmt.Fprintf(out, "No token usage recorded for session %q.\n", filter)
			return nil
		}
		fmt.Fprintf(out, "No token usage recorded under %s.\n", sessionsDir)
		return nil
	}

	if detail {
		cliui.PrintUsageReport(out, cliui.UsageReport{
			SessionsDir: sessionsDir,
			Session:     sessionLabel(detailSession.Key, detailSession.Aliases),
			Totals:      detailSession.Summary.Totals,
			Models:      detailSession.Summary.Models,
		})
		return nil
	}

	limit := opts.Limit
	if limit <= 0 || limit > len(rows) {
		limit = len(rows)
	}
	displayRows := make([]cliui.UsageRow, 0, limit)
	for _, row := range rows[:limit] {
		displayRows = append(displayRows, cliui.UsageRow{
			Label:  row.Label,
			Totals: row.Usage.Totals,
		})
	}

	cliui.PrintUsageReport(out, cliui.UsageReport{
		SessionsDir: sessionsDir,
		Sessions:    len(rows),
		Totals:      totals,
		Rows:        displayRows,
		Limit:       opts.Limit,
	})
	return nil
}

// sessionLabel returns a human-readable session key. Opaque canonical keys
// (sk_v1_*) are replaced by their shortest alias so reports stay readable.
func sessionLabel(key string, aliases []string) string {
	if !strings.HasPrefix(key, "sk_v1_") {
		return key
	}

	best := ""
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == "agent:main:main" {
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
