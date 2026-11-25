package auth

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthLoginGmailRequiresClientCredentials(t *testing.T) {
	t.Setenv("PICOCLAW_GMAIL_CLIENT_ID", "")
	t.Setenv("PICOCLAW_GMAIL_CLIENT_SECRET", "")
	err := authLoginCmd("gmail", false, false, true, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PICOCLAW_GMAIL_CLIENT_ID")
}

func TestNewLoginSubCommand(t *testing.T) {
	cmd := newLoginCommand()

	require.NotNil(t, cmd)

	assert.Equal(t, "Login via OAuth or paste token", cmd.Short)

	assert.True(t, cmd.HasFlags())

	assert.NotNil(t, cmd.Flags().Lookup("device-code"))
	assert.NotNil(t, cmd.Flags().Lookup("no-browser"))
	assert.NotNil(t, cmd.Flags().Lookup("client-id"))
	assert.NotNil(t, cmd.Flags().Lookup("client-secret"))

	providerFlag := cmd.Flags().Lookup("provider")
	require.NotNil(t, providerFlag)

	val, found := providerFlag.Annotations[cobra.BashCompOneRequiredFlag]
	require.True(t, found)
	require.NotEmpty(t, val)
	assert.Equal(t, "true", val[0])
}
