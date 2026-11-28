package acp

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	acpprotocol "github.com/sipeed/picoclaw/pkg/acp"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ExitError carries the stable process exit code for the ACP command.
type ExitError struct {
	Code int
	Err  error
}

// Error implements error.
func (e *ExitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewACPCommand creates the standalone Agent Client Protocol command.
func NewACPCommand() *cobra.Command {
	var (
		cwd             string
		model           string
		debug           bool
		maxSessions     int
		maxMessageBytes int64
	)

	cmd := &cobra.Command{
		Use:         "acp",
		Short:       "Run PicoClaw as an Agent Client Protocol stdio agent",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{internal.StdioProtocolAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger.ConfigureFromEnv()
			if debug {
				logger.SetLevel(logger.DEBUG)
			}

			cfg, err := internal.LoadConfig()
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			workspace := cwd
			if workspace == "" {
				workspace, err = os.Getwd()
				if err != nil {
					return &ExitError{Code: 2, Err: fmt.Errorf("get working directory: %w", err)}
				}
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			peer := acpprotocol.NewPeer(cmd.InOrStdin(), cmd.OutOrStdout(), maxMessageBytes)
			runtime, err := acpprotocol.NewRuntime(acpprotocol.RuntimeOptions{
				Config:       cfg,
				Workspace:    workspace,
				Version:      config.FormatVersion(),
				Peer:         peer,
				DefaultModel: model,
				MaxSessions:  maxSessions,
			})
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			if err := runtime.Serve(ctx); err != nil && !errors.Is(err, acpprotocol.ErrPeerClosed) {
				return &ExitError{Code: 3, Err: err}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&cwd, "cwd", "", "Workspace root (defaults to the current directory)")
	cmd.Flags().StringVar(&model, "model", "", "Initial model alias for new sessions")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging on stderr")
	cmd.Flags().IntVar(&maxSessions, "max-sessions", 32, "Maximum concurrent ACP sessions")
	cmd.Flags().Int64Var(&maxMessageBytes, "max-message-bytes", 1<<20, "Maximum incoming ACP message size")
	return cmd
}
