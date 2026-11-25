package auth

import "github.com/spf13/cobra"

func newLoginCommand() *cobra.Command {
	var (
		provider      string
		useDeviceCode bool
		useOauth      bool
		noBrowser     bool
		clientID      string
		clientSecret  string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login via OAuth or paste token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return authLoginCmd(provider, useDeviceCode, useOauth, noBrowser, clientID, clientSecret)
		},
	}

	cmd.Flags().StringVarP(
		&provider, "provider", "p", "",
		"Provider to login with (openai, anthropic, google-antigravity, antigravity, gmail)",
	)
	cmd.Flags().BoolVar(&useDeviceCode, "device-code", false, "Use device code flow (for headless environments)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not auto-open a browser during OAuth login")
	cmd.Flags().BoolVar(
		&useOauth, "setup-token", false,
		"Use setup-token flow for Anthropic (from `claude setup-token`)",
	)
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client ID (Gmail; or set PICOCLAW_GMAIL_CLIENT_ID)")
	cmd.Flags().StringVar(
		&clientSecret, "client-secret", "",
		"OAuth client secret (Gmail; prefer PICOCLAW_GMAIL_CLIENT_SECRET to avoid argv exposure)",
	)
	_ = cmd.MarkFlagRequired("provider")

	return cmd
}
