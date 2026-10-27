package mcp

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"go.mau.fi/util/shlex"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
)

func newEditCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Open the PicoClaw config in $EDITOR",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			editor := strings.TrimSpace(os.Getenv("EDITOR"))
			if editor == "" {
				return fmt.Errorf("$EDITOR is not set")
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err = saveValidatedConfig(cfg); err != nil {
				return err
			}

			editorArgs, err := shlex.Split(editor)
			if err != nil {
				return fmt.Errorf("failed to parse $EDITOR: %w", err)
			}
			if len(editorArgs) == 0 {
				return fmt.Errorf("$EDITOR is empty")
			}

			editorArgs = append(editorArgs, internal.GetConfigPath())
			process := editorCommand(editorArgs[0], editorArgs[1:]...)
			process.Stdin = cmd.InOrStdin()
			process.Stdout = cmd.OutOrStdout()
			process.Stderr = cmd.ErrOrStderr()

			if err := process.Run(); err != nil {
				return fmt.Errorf("failed to start editor: %w", err)
			}

			// The editor may have left an invalid document behind; report it
			// now, while the user is still in front of the command, instead of
			// failing on the next start.
			return validateEditedConfig()
		},
	}
}

// validateEditedConfig re-reads the config file written by the editor and
// validates it against the same document schema used before saving.
func validateEditedConfig() error {
	data, err := os.ReadFile(internal.GetConfigPath())
	if err != nil {
		return fmt.Errorf("failed to read edited config: %w", err)
	}
	if err := validateConfigDocument(data); err != nil {
		return fmt.Errorf("edited config is not valid: %w", err)
	}
	return nil
}
