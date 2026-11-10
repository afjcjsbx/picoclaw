package usage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUsageCommand(t *testing.T) {
	cmd := NewUsageCommand()

	require.NotNil(t, cmd)
	assert.Equal(t, "usage", cmd.Use)
	assert.NotNil(t, cmd.RunE)
	assert.False(t, cmd.HasSubCommands())

	for _, name := range []string{"session", "json", "limit"} {
		assert.NotNilf(t, cmd.Flags().Lookup(name), "missing --%s flag", name)
	}
	assert.Equal(t, "s", cmd.Flags().Lookup("session").Shorthand)
	assert.Equal(t, "n", cmd.Flags().Lookup("limit").Shorthand)
}
