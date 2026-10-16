package plugin

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newToggleCommand builds enable and disable, which differ only in the value
// they write.
func newToggleCommand(use, short string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			id := args[0]
			entry, exists := cfg.Plugins.Entries[id]
			if !exists {
				return fmt.Errorf("plugin entry %q not found: add it with `picoclaw plugin add`", id)
			}

			entry.Enabled = enabled
			cfg.Plugins.Entries[id] = entry
			if enabled {
				// An enabled entry is inert while the global switch is off.
				cfg.Plugins.Enabled = true
			}

			if err := saveConfig(cfg); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Plugin %q %sd. Restart the gateway to apply.\n", id, use)
			return nil
		},
	}
}
