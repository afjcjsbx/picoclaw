package cron

import "github.com/spf13/cobra"

func newDisableCommand(store func() jobStore) *cobra.Command {
	return newSetEnabledCommand("disable", "Disable a job", false, store)
}
