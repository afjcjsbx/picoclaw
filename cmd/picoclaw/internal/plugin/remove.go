package plugin

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a plugin entry from the config",
		Long: `Remove plugins.entries.<id> from the config. The package files and the
plugin's data directory are left on disk.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			id := args[0]
			if _, exists := cfg.Plugins.Entries[id]; !exists {
				return fmt.Errorf("plugin entry %q not found", id)
			}

			delete(cfg.Plugins.Entries, id)
			if len(cfg.Plugins.Entries) == 0 {
				cfg.Plugins.Entries = nil
				cfg.Plugins.Enabled = false
			}

			if err := saveConfig(cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Plugin entry %q removed. Package files and plugin data were kept.\n", id)
			return nil
		},
	}
}
