package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
)

func TestMCPLoginSavesOAuthOnlyAfterSuccess(t *testing.T) {
	for _, succeeds := range []bool{true, false} {
		name := "failure"
		if succeeds {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			path := setupMCPConfigEnv(t)
			_, err := executeCommand(
				NewMCPCommand(),
				[]string{"add", "remote", "https://mcp.example/mcp", "--transport", "http"},
				"",
			)
			require.NoError(t, err)
			original := loginOAuth
			t.Cleanup(func() { loginOAuth = original })
			loginOAuth = func(ctx context.Context, name string, server config.MCPServerConfig, opts picomcp.OAuthLoginOptions) error {
				require.Equal(t, "remote", name)
				require.True(t, opts.NoBrowser)
				require.Equal(t, "public-client", server.OAuth.ClientID)
				require.Equal(t, "https://auth.example", server.OAuth.Issuer)
				require.Equal(t, 8123, server.OAuth.CallbackPort)
				require.Equal(t, []string{"tools", "offline_access"}, server.OAuth.Scopes)
				_, hasDeadline := ctx.Deadline()
				require.True(t, hasDeadline)
				if !succeeds {
					return errors.New("consent denied")
				}
				return nil
			}
			_, err = executeCommand(
				NewMCPCommand(),
				[]string{
					"login",
					"remote",
					"--no-browser",
					"--client-id",
					"public-client",
					"--issuer",
					"https://auth.example",
					"--callback-port",
					"8123",
					"--scope",
					"tools,offline_access",
				},
				"",
			)
			cfg := readMCPConfig(t, path)
			if succeeds {
				require.NoError(t, err)
				require.NotNil(t, cfg.Tools.MCP.Servers["remote"].OAuth)
			} else {
				require.ErrorContains(t, err, "consent denied")
				require.Nil(t, cfg.Tools.MCP.Servers["remote"].OAuth)
			}
		})
	}
}

func TestPluginMCPLoginUsesConfiguredOverride(t *testing.T) {
	path := setupMCPConfigEnv(t)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"memory"}`), 0o600))
	cfg := readMCPConfig(t, path)
	cfg.Plugins.Enabled = true
	cfg.Plugins.Entries = map[string]config.PluginEntryConfig{"memory": {
		Enabled:      true,
		Path:         root,
		MCPOverrides: map[string]json.RawMessage{"remote": json.RawMessage(`{"type":"streamable-http","url":"https://mcp.example/mcp"}`)},
		MCPOAuth:     map[string]config.MCPOAuthConfig{"remote": {}},
	}}
	require.NoError(t, saveValidatedConfig(cfg))
	original := loginOAuth
	t.Cleanup(func() { loginOAuth = original })
	loginOAuth = func(_ context.Context, name string, server config.MCPServerConfig, _ picomcp.OAuthLoginOptions) error {
		require.Equal(t, "memory:remote", name)
		require.Equal(t, "https://mcp.example/mcp", server.URL)
		require.NotNil(t, server.OAuth)
		return nil
	}
	_, err := executeCommand(NewMCPCommand(), []string{"login", "memory:remote", "--no-browser", "--scope", "tools"}, "")
	require.NoError(t, err)
	updated := readMCPConfig(t, path)
	require.Equal(t, []string{"tools"}, updated.Plugins.Entries["memory"].MCPOAuth["remote"].Scopes)
}

func TestMCPOAuthCommandsRejectUnknownServer(t *testing.T) {
	setupMCPConfigEnv(t)
	for _, command := range []string{"login", "logout"} {
		_, err := executeCommand(NewMCPCommand(), []string{command, "missing"}, "")
		require.ErrorContains(t, err, `MCP server "missing" not found`)
	}
}

func TestMCPOAuthSchemaValidation(t *testing.T) {
	for _, oauth := range []string{`{}`, `{"client_id":"public","scopes":["tools"],"callback_port":8123}`} {
		require.NoError(
			t,
			validateConfigDocument(
				[]byte(
					`{"tools":{"mcp":{"enabled":true,"servers":{"test":{"enabled":true,"url":"https://example.com","oauth":`+oauth+`}}}}}`,
				),
			),
		)
	}
	for _, oauth := range []string{`{"callback_port":-1}`, `{"scopes":"tools"}`, `{"access_token":"secret"}`} {
		require.Error(
			t,
			validateConfigDocument(
				[]byte(
					`{"tools":{"mcp":{"enabled":true,"servers":{"test":{"enabled":true,"url":"https://example.com","oauth":`+oauth+`}}}}}`,
				),
			),
		)
	}
}
