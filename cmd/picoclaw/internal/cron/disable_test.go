package cron

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisableSubcommand(t *testing.T) {
	fn := func() jobStore { return nil }
	cmd := newDisableCommand(fn)

	require.NotNil(t, cmd)

	assert.Equal(t, "disable", cmd.Use)
	assert.Equal(t, "Disable a job", cmd.Short)

	assert.True(t, cmd.HasExample())
}
