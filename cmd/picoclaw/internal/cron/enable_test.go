package cron

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnableSubcommand(t *testing.T) {
	fn := func() jobStore { return nil }
	cmd := newEnableCommand(fn)

	require.NotNil(t, cmd)

	assert.Equal(t, "enable", cmd.Use)
	assert.Equal(t, "Enable a job", cmd.Short)

	assert.True(t, cmd.HasExample())
}
