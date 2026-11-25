package auth

import "github.com/spf13/cobra"

func newLogoutCommand() *cobra.Command {
	var provider string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authLogoutCmd(provider)
		},
	}

	cmd.Flags().StringVarP(
		&provider, "provider", "p", "",
		"Provider or credential key to logout from (e.g. gmail:bot@example.com); empty = all",
	)

	return cmd
}
