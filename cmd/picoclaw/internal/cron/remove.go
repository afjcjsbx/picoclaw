package cron

import "github.com/spf13/cobra"

func newRemoveCommand(store func() jobStore) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove",
		Short:   "Remove a job by ID",
		Args:    cobra.ExactArgs(1),
		Example: `picoclaw cron remove 1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cronRemoveCmd(cmd.Context(), store(), args[0])
		},
	}

	return cmd
}
