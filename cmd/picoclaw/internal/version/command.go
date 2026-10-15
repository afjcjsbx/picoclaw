package version

import (
	"github.com/spf13/cobra"

	"github.com/afjcjsbx/picoclaw/cmd/picoclaw/internal"
	"github.com/afjcjsbx/picoclaw/cmd/picoclaw/internal/cliui"
	"github.com/afjcjsbx/picoclaw/pkg/config"
)

func NewVersionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Show version information",
		Run: func(_ *cobra.Command, _ []string) {
			printVersion()
		},
	}

	return cmd
}

func printVersion() {
	build, goVer := config.FormatBuildInfo()
	cliui.PrintVersion(internal.Logo, "picoclaw "+config.FormatVersion(), build, goVer)
}
