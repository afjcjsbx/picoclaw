package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/config"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
	"github.com/sipeed/picoclaw/pkg/plugins"
)

var loginOAuth = picomcp.LoginOAuth

func oauthServer(cfg *config.Config, name string) (config.MCPServerConfig, string, string, error) {
	if server, ok := cfg.Tools.MCP.Servers[name]; ok {
		return server, "", "", nil
	}
	pluginID, serverName, qualified := strings.Cut(name, ":")
	if !qualified || pluginID == "" || serverName == "" {
		return config.MCPServerConfig{}, "", "", fmt.Errorf("MCP server %q not found", name)
	}
	server, err := plugins.ResolveOAuthServer(cfg.Plugins, cfg.WorkspacePath(), pluginID, serverName)
	return server, pluginID, serverName, err
}

func newLoginCommand() *cobra.Command {
	var noBrowser bool
	var clientID, issuer string
	var port int
	var scopes []string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use: "login <name>", Short: "Authorize an MCP server using browser OAuth", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			name := args[0]
			server, pluginID, serverName, err := oauthServer(cfg, name)
			if err != nil {
				return err
			}
			oauthCfg := config.MCPOAuthConfig{}
			if server.OAuth != nil {
				oauthCfg = *server.OAuth
			}
			if cmd.Flags().Changed("client-id") {
				oauthCfg.ClientID = clientID
			}
			if cmd.Flags().Changed("issuer") {
				oauthCfg.Issuer = issuer
			}
			if cmd.Flags().Changed("callback-port") {
				oauthCfg.CallbackPort = port
			}
			if cmd.Flags().Changed("scope") {
				oauthCfg.Scopes = scopes
			}
			server.OAuth = &oauthCfg
			if timeout <= 0 {
				return fmt.Errorf("timeout must be positive")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := loginOAuth(
				ctx,
				name,
				server,
				picomcp.OAuthLoginOptions{NoBrowser: noBrowser, Output: cmd.OutOrStdout()},
			); err != nil {
				return err
			}
			if pluginID == "" {
				cfg.Tools.MCP.Servers[name] = server
			} else {
				entry := cfg.Plugins.Entries[pluginID]
				entry.MCPOAuth[serverName] = oauthCfg
				cfg.Plugins.Entries[pluginID] = entry
			}
			if err := saveValidatedConfig(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "MCP server %q authorized.\n", name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the login URL without opening a browser")
	cmd.Flags().
		StringVar(&clientID, "client-id", "", "Pre-registered public OAuth client ID (default: dynamic registration)")
	cmd.Flags().StringVar(&issuer, "issuer", "", "Expected authorization server issuer for a pre-registered client")
	cmd.Flags().IntVar(&port, "callback-port", 0, "Loopback callback port (0 chooses an available port)")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil, "OAuth scopes (comma-separated or repeated)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Login timeout")
	return cmd
}

func newLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use: "logout <name>", Short: "Remove locally stored MCP OAuth credentials", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			server, _, _, err := oauthServer(cfg, args[0])
			if err != nil {
				return err
			}
			if err := picomcp.LogoutOAuth(args[0], server); err != nil {
				return err
			}
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"Local OAuth credentials removed for MCP server %q. Restart running gateways to close existing sessions.\n",
				args[0],
			)
			return nil
		},
	}
}
