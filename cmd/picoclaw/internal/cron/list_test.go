package cron

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewListSubcommand(t *testing.T) {
	fn := func() jobStore { return nil }
	cmd := newListCommand(fn)

	require.NotNil(t, cmd)

	assert.Equal(t, "List all scheduled jobs", cmd.Short)
}
