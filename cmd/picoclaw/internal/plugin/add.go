package plugin

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/plugins"
)

func newAddCommand() *cobra.Command {
	var (
		path   string
		enable bool
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "add <id> [--path <dir>] [--enable]",
		Short: "Add or update a plugin entry in the config",
		Long: `Add or update plugins.entries.<id> in the config.

Without --path the entry selects a package found under the plugin directories
by its manifest name. A relative --path is resolved against the current
directory and stored as an absolute path. The package is validated first; an
invalid manifest is rejected. The entry stays disabled unless --enable is given.

When the entry already exists only the fields you pass are changed; hooks,
agents, MCP overrides and the rest are kept.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !plugins.ValidName(id) {
				return fmt.Errorf("invalid plugin id %q: use 1-64 lowercase letters, digits, '.' or '-'", id)
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if cfg.Plugins.Entries == nil {
				cfg.Plugins.Entries = map[string]config.PluginEntryConfig{}
			}

			entry, exists := cfg.Plugins.Entries[id]
			if exists && !force {
				overwrite, confirmErr := confirmOverwrite(cmd.InOrStdin(), cmd.OutOrStdout(), id)
				if confirmErr != nil {
					return fmt.Errorf("failed to confirm overwrite: %w", confirmErr)
				}
				if !overwrite {
					return fmt.Errorf("aborted: plugin entry %q already exists", id)
				}
			}
			if path != "" {
				if entry.Path, err = filepath.Abs(path); err != nil {
					return err
				}
			}
			entry.Enabled = entry.Enabled || enable

			// Resolve through the same inventory the host uses, so what is saved
			// is what will load.
			cfg.Plugins.Entries[id] = entry
			item, err := resolve(cfg, id)
			if err != nil {
				return fmt.Errorf("%w; pass --path <dir> to select the package", err)
			}
			report, err := plugins.Validate(id, item.Root, entry)
			if err != nil {
				return fmt.Errorf("%s is not a valid plugin: %w", item.Root, err)
			}

			if entry.Enabled {
				cfg.Plugins.Enabled = true
			}
			if err := saveConfig(cfg); err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			printDiagnostics(w, report.Diagnostics)
			if entry.Enabled {
				fmt.Fprintf(w, "✓ Plugin %q saved and enabled. Restart the gateway to load it.\n", id)
			} else {
				fmt.Fprintf(w, "✓ Plugin %q saved, disabled. Run `picoclaw plugin enable %s` to activate it.\n", id, id)
			}
			return nil
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&path, "path", "", "Package directory (default: find <id> under the plugin directories)")
	flags.BoolVar(&enable, "enable", false, "Enable the entry")
	flags.BoolVarP(&force, "force", "f", false, "Overwrite an existing entry without prompting")

	return cmd
}
