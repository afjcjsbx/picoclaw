package cron

import "github.com/spf13/cobra"

func newRemoveCommand(storePath func() string) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "remove",
		Short:   "Remove a job by ID",
		Args:    cobra.ExactArgs(1),
		Example: `picoclaw cron remove 1`,
		RunE: func(_ *cobra.Command, args []string) error {
			cronRemoveCmd(storePath(), args[0], force)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Remove a plugin-managed job anyway")

	return cmd
}
