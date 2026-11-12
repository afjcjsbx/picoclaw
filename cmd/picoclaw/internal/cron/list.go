package cron

import "github.com/spf13/cobra"

func newListCommand(store func() jobStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all scheduled jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cronListCmd(cmd.Context(), store())
		},
	}

	return cmd
}
