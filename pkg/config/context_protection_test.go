package config

import (
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/require"
)

func TestAgentDefaults_ContextProtectionEnv(t *testing.T) {
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_PROTECT_FIRST_N", "0")
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_PROTECT_LAST_N", "6")
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_MIN_TAIL_USER_MESSAGES", "2")
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_TAIL_TOKEN_BUDGET", "4096")

	var defaults AgentDefaults
	require.NoError(t, env.Parse(&defaults))

	require.NotNil(t, defaults.ProtectFirstN)
	require.Equal(t, 0, *defaults.ProtectFirstN)
	require.NotNil(t, defaults.ProtectLastN)
	require.Equal(t, 6, *defaults.ProtectLastN)
	require.NotNil(t, defaults.MinTailUserMessages)
	require.Equal(t, 2, *defaults.MinTailUserMessages)
	require.NotNil(t, defaults.TailTokenBudget)
	require.Equal(t, 4096, *defaults.TailTokenBudget)
}

func TestAgentDefaults_ContextProtectionEnvUnsetLeavesNil(t *testing.T) {
	var defaults AgentDefaults
	require.NoError(t, env.Parse(&defaults))
	require.Nil(t, defaults.ProtectFirstN)
	require.Nil(t, defaults.ProtectLastN)
	require.Nil(t, defaults.MinTailUserMessages)
	require.Nil(t, defaults.TailTokenBudget)
}
