package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal/cliui"
	"github.com/sipeed/picoclaw/pkg/plugins"
)

func newListCommand() *cobra.Command {
	var (
		includeStatus bool
		timeout       time.Duration
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured plugins and packages found on disk",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			var statuses []plugins.Status
			if includeStatus {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				statuses = runManager(ctx, cfg.Plugins, cfg.WorkspacePath(), nil)
			}

			items := plugins.Inventory(cfg.Plugins, cfg.WorkspacePath(), statuses)
			if len(items) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No plugins configured or found.")
				return nil
			}
			cliui.PrintPluginList(cmd.OutOrStdout(), listRows(items))
			return nil
		},
	}

	cmd.Flags().BoolVar(&includeStatus, "status", false,
		"Load enabled plugins and show live status (starts their MCP servers)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Time to wait for plugins to load with --status")

	return cmd
}

func listRows(items []plugins.Installed) []cliui.PluginListRow {
	rows := make([]cliui.PluginListRow, len(items))
	for i, item := range items {
		name := item.Name
		if name == "" && item.Root != "" {
			name = manifestName(item.Root)
		}
		rows[i] = cliui.PluginListRow{
			ID:          item.ID,
			Name:        name,
			State:       item.State,
			Root:        shortPath(item.Root),
			Diagnostics: item.Diagnostics,
		}
	}
	return rows
}

// manifestName reads the name without validating the rest of the package; an
// unreadable manifest simply leaves the column empty.
func manifestName(root string) string {
	data, err := plugins.ReadPackageFile(root, "plugin.json")
	if err != nil {
		return ""
	}
	manifest, _, err := plugins.ParseManifest(data)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(manifest.Name)
}
