package cron

import "github.com/spf13/cobra"

func newEnableCommand(storePath func() string) *cobra.Command {
	return newSetEnabledCommand("enable", "Enable a job", true, storePath)
}

func newSetEnabledCommand(name, short string, enabled bool, storePath func() string) *cobra.Command {
	return &cobra.Command{
		Use:     name,
		Short:   short,
		Args:    cobra.ExactArgs(1),
		Example: "picoclaw cron " + name + " 1",
		RunE: func(_ *cobra.Command, args []string) error {
			cronSetJobEnabled(storePath(), args[0], enabled)
			return nil
		},
	}
}
