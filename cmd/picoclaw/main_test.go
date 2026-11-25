package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestNewPicoclawCommand(t *testing.T) {
	cmd := NewPicoclawCommand()

	require.NotNil(t, cmd)

	short := fmt.Sprintf("%s PicoClaw — personal AI assistant", internal.Logo)
	longHas := strings.Contains(cmd.Long, config.FormatVersion())

	assert.Equal(t, "picoclaw", cmd.Use)
	assert.Equal(t, short, cmd.Short)
	assert.True(t, longHas)

	assert.True(t, cmd.HasSubCommands())
	assert.True(t, cmd.HasAvailableSubCommands())

	assert.True(t, cmd.PersistentFlags().Lookup("no-color") != nil)

	assert.Nil(t, cmd.Run)
	assert.Nil(t, cmd.RunE)

	assert.NotNil(t, cmd.PersistentPreRun)
	assert.Nil(t, cmd.PersistentPostRun)

	allowedCommands := []string{
		"agent",
		"auth",
		"config",
		"cron",
		"gateway",
		"mcp",
		"migrate",
		"model",
		"onboard",
		"plugin",
		"skills",
		"status",
		"update",
		"usage",
		"version",
	}

	subcommands := cmd.Commands()
	assert.Len(t, subcommands, len(allowedCommands))

	for _, subcmd := range subcommands {
		found := slices.Contains(allowedCommands, subcmd.Name())
		assert.True(t, found, "unexpected subcommand %q", subcmd.Name())

		assert.False(t, subcmd.Hidden)
	}
}

func TestMachineOutputRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "json flag", args: []string{"usage", "--json"}, want: true},
		{name: "json true", args: []string{"usage", "--json=true"}, want: true},
		{name: "json one", args: []string{"usage", "--json=1"}, want: true},
		{name: "json false", args: []string{"usage", "--json=false"}, want: false},
		{name: "no json", args: []string{"usage"}, want: false},
		{name: "json as value of session", args: []string{"usage", "--session", "--json"}, want: false},
		{name: "root flag before command", args: []string{"--no-color", "usage", "--json"}, want: true},
		{name: "command without json flag", args: []string{"agent", "-m", "--json"}, want: false},
		{name: "unknown command", args: []string{"nope", "--json"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, machineOutputRequested(tt.args))
		})
	}
}
