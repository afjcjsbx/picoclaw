package plugin

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/plugins"
)

func newShowCommand() *cobra.Command {
	var (
		connect bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a plugin's manifest, capabilities and diagnostics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			id := args[0]
			item, err := resolve(cfg, id)
			if err != nil {
				return err
			}
			entry, configured := cfg.Plugins.Entries[id]

			report, err := plugins.Validate(id, item.Root, entry)
			if err != nil {
				return fmt.Errorf("plugin %q is not valid: %w", id, err)
			}

			w := cmd.OutOrStdout()
			m := report.Manifest
			printField(w, "ID", id)
			printField(w, "Name", m.Name)
			printField(w, "Version", m.Version)
			printField(w, "Description", m.Description)
			if m.Author != nil {
				printField(w, "Author", strings.TrimSpace(m.Author.Name+" "+m.Author.Email+" "+m.Author.URL))
			}
			printField(w, "Homepage", m.Homepage)
			printField(w, "Repository", m.Repository)
			printField(w, "License", m.License)
			printField(w, "Keywords", strings.Join(m.Keywords, ", "))
			printField(w, "Schema", m.Schema)
			printField(w, "Root", report.Root)
			printField(w, "State", item.State)
			if configured {
				printField(w, "Allow hooks", boolWord(entry.AllowHooks))
			}
			fmt.Fprintln(w)

			if !connect {
				printList(w, "Skills", report.Skills)
				printList(w, "MCP servers", report.Servers)
				printList(w, "Hooks", report.Hooks)
				if len(report.Hooks) > 0 && !entry.AllowHooks {
					fmt.Fprintln(w, "  (inactive: set plugins.entries."+id+".allow_hooks to true)")
				}
				printDiagnosticSection(w, report.Diagnostics)
				return nil
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			printProbe(w, probe(ctx, cfg, id, entry), timeout)
			return nil
		},
	}

	cmd.Flags().
		BoolVar(&connect, "connect", false, "Load the plugin and list its live capabilities (starts its MCP servers)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Time to wait for the plugin to load with --connect")

	return cmd
}

func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// printProbe reports a dry-run load. It is shared by show --connect and test.
func printProbe(w io.Writer, r probeResult, timeout time.Duration) {
	state := string(r.Status.State)
	if !finished(r.Status.State) {
		state = fmt.Sprintf("unfinished after %s (%s)", timeout, state)
	}
	printField(w, "Live state", state)
	printList(w, "Skills", r.Skills)
	printList(w, "Tools", r.Tools)
	printList(w, "Hooks", r.Hooks)
	printDiagnosticSection(w, r.Status.Diagnostics)
}

func finished(s plugins.State) bool {
	return s == plugins.Ready || s == plugins.Degraded || s == plugins.Failed
}
