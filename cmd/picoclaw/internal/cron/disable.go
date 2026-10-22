package cron

import "github.com/spf13/cobra"

func newDisableCommand(storePath func() string) *cobra.Command {
	return newSetEnabledCommand("disable", "Disable a job", false, storePath)
}
