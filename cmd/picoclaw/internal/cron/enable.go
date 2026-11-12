package cron

import "github.com/spf13/cobra"

func newEnableCommand(store func() jobStore) *cobra.Command {
	return newSetEnabledCommand("enable", "Enable a job", true, store)
}

func newSetEnabledCommand(name, short string, enabled bool, store func() jobStore) *cobra.Command {
	return &cobra.Command{
		Use:     name,
		Short:   short,
		Args:    cobra.ExactArgs(1),
		Example: "picoclaw cron " + name + " 1",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cronSetJobEnabled(cmd.Context(), store(), args[0], enabled)
		},
	}
}
