package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/plugins"
)

func newTestCommand() *cobra.Command {
	var (
		timeout time.Duration
		strict  bool
	)

	cmd := &cobra.Command{
		Use:   "test <id>",
		Short: "Dry-run load a plugin and report what it publishes",
		Long: `Load one plugin exactly as the gateway would and print its final state, the
skills, MCP tools and hooks it publishes, and its diagnostics. The entry does not
need to be enabled. Nothing is registered with an agent, but the plugin's MCP
servers are started, so only test packages you trust.

Exit status is 1 when the plugin fails or does not finish loading within
--timeout. A degraded plugin (some component skipped) passes unless --strict.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			id := args[0]
			item, err := resolve(cfg, id)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			result := probe(ctx, cfg, id, cfg.Plugins.Entries[id])

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Testing %s (%s)\n", id, item.Root)
			printProbe(w, result, timeout)

			switch {
			case !finished(result.Status.State):
				return fmt.Errorf("plugin %q did not finish loading within %s", id, timeout)
			case result.Status.State == plugins.Failed:
				return fmt.Errorf("plugin %q failed to load", id)
			case strict && result.Status.State == plugins.Degraded:
				return fmt.Errorf("plugin %q is degraded (--strict)", id)
			}
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "Time to wait for the plugin to load")
	cmd.Flags().BoolVar(&strict, "strict", false, "Fail when the plugin loads degraded")

	return cmd
}
