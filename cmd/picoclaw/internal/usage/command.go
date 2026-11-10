package usage

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
)

type options struct {
	Session string
	JSON    bool
	Limit   int
}

// NewUsageCommand builds the `picoclaw usage` command.
func NewUsageCommand() *cobra.Command {
	var opts options

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show token usage and cost per session",
		Long: `Summarize provider-reported token usage and cost for stored sessions.

The command reads <workspace>/sessions without modifying it, so it can run
while the gateway is active. Session files are streamed and reduced to
per-session totals, so memory stays bounded. Input tokens are split into fresh
prompt tokens, cache reads, and cache writes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := internal.LoadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			sessionsDir := filepath.Join(cfg.WorkspacePath(), "sessions")
			return runUsage(cmd.OutOrStdout(), sessionsDir, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Session, "session", "s", "", "Show only this session key or alias")
	cmd.Flags().BoolVar(&opts.JSON, "json", false, "Emit machine-readable JSON")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "n", 20, "Maximum sessions to list (0 shows all)")

	return cmd
}
