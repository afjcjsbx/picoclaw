package cliui

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// PluginListRow is one row in the plugin list output.
type PluginListRow struct {
	ID          string
	Name        string
	State       string // runtime state, or "disabled", "pending", "not enabled"
	Root        string
	Diagnostics int
}

// PrintPluginList renders the plugin list output (plain or fancy).
func PrintPluginList(w io.Writer, rows []PluginListRow) {
	if !UseFancyLayout() {
		tableRows := make([][]string, len(rows))
		for i, r := range rows {
			tableRows[i] = []string{r.ID, r.Name, r.State, r.Root, strconv.Itoa(r.Diagnostics)}
		}
		printPlainTable(w, []string{"ID", "Name", "State", "Root", "Diag"}, tableRows)
		return
	}

	var b strings.Builder
	b.WriteString(titleBarStyle().Render(fmt.Sprintf("Plugins (%d)", len(rows))))
	b.WriteString("\n")
	contentW := InnerWidth() - 4
	for i, row := range rows {
		if i > 0 {
			b.WriteString(strings.Repeat("─", contentW) + "\n")
		}
		b.WriteString("\n")
		line := "  " + mcpToolNameStyle().Render(row.ID) + "  " + pluginStateStyle(row.State).Render(row.State)
		if row.Diagnostics > 0 {
			line += "  " + mcpTagStyle().Render(fmt.Sprintf("%d diagnostics", row.Diagnostics))
		}
		b.WriteString(line + "\n")
		detail := row.Root
		if row.Name != "" && row.Name != row.ID {
			detail = row.Name + "  " + detail
		}
		b.WriteString("  " + mcpTagStyle().Render(detail) + "\n")
	}
	fmt.Fprintln(w, borderStyle().Width(InnerWidth()).Render(b.String()))
}

func pluginStateStyle(state string) lipgloss.Style {
	switch state {
	case "ready":
		return lipgloss.NewStyle().Foreground(colorOK).Bold(true)
	case "degraded":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#B26A00")).Bold(true)
	case "failed":
		return lipgloss.NewStyle().Foreground(accentRed).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(colorMuted)
	}
}
