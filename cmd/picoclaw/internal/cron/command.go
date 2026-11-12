package cron

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/config"
)

func NewCronCommand() *cobra.Command {
	var store jobStore

	cmd := &cobra.Command{
		Use:     "cron",
		Aliases: []string{"c"},
		Short:   "Manage scheduled tasks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		// Resolve the store at execution time so it reflects the current config
		// and is shared across all subcommands. While the gateway is running,
		// the CLI goes through its authenticated API; otherwise it edits the
		// file directly.
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := internal.LoadConfig()
			if err != nil {
				return fmt.Errorf("error loading config: %w", err)
			}
			storePath := filepath.Join(cfg.WorkspacePath(), "cron", "jobs.json")

			store = newGatewayStore(config.GetHome())
			if store == nil {
				store = fileStore{path: storePath}
			}
			return nil
		},
	}

	cmd.AddCommand(
		newListCommand(func() jobStore { return store }),
		newAddCommand(func() jobStore { return store }),
		newRemoveCommand(func() jobStore { return store }),
		newEnableCommand(func() jobStore { return store }),
		newDisableCommand(func() jobStore { return store }),
	)

	return cmd
}
