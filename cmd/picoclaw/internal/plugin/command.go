package plugin

import "github.com/spf13/cobra"

func NewPluginCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage Agent Plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newListCommand(),
		newShowCommand(),
		newAddCommand(),
		newRemoveCommand(),
		newToggleCommand("enable", "Enable a plugin entry", true),
		newToggleCommand("disable", "Disable a plugin entry", false),
		newEditCommand(),
		newValidateCommand(),
		newInitCommand(),
		newTestCommand(),
	)

	return cmd
}
