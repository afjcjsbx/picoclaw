package config

import (
	"encoding/json"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/require"
)

func TestExecConfig_EnvSetAndAllowlistParsing(t *testing.T) {
	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(`{
		"tools": {
			"exec": {
				"env_set": {"APP_ENV": "test"},
				"env_allowlist": ["GOPATH"]
			}
		}
	}`), &cfg))

	require.Equal(t, map[string]string{"APP_ENV": "test"}, cfg.Tools.Exec.EnvSet)
	require.Equal(t, []string{"GOPATH"}, cfg.Tools.Exec.EnvAllowlist)

	// env_set is JSON-only; the allowlist also accepts a comma-separated env
	// override.
	t.Setenv("PICOCLAW_TOOLS_EXEC_ENV_ALLOWLIST", "GOPATH,SSH_AUTH_SOCK")
	require.NoError(t, env.Parse(&cfg))
	require.Equal(t, []string{"GOPATH", "SSH_AUTH_SOCK"}, cfg.Tools.Exec.EnvAllowlist)
	require.Equal(t, map[string]string{"APP_ENV": "test"}, cfg.Tools.Exec.EnvSet)
}
