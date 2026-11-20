package backupcmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	backuppkg "github.com/sipeed/picoclaw/pkg/backup"
)

type restoreOptions struct {
	Home           string
	Config         string
	SSHKey         string
	PassphraseFile string
	Force          bool
	AllowRunning   bool
	NoSSHKey       bool
	NoSafetyBackup bool
	DryRun         bool
}

// NewRestoreCommand builds the `picoclaw restore` command.
func NewRestoreCommand() *cobra.Command {
	opts := restoreOptions{}
	cmd := &cobra.Command{
		Use:   "restore <archive>",
		Short: "Restore a snapshot created by picoclaw backup",
		Long: `Restore a PicoClaw snapshot.

The archive is validated against its MANIFEST.json before anything is written:
every file hash and size must match, paths must stay inside the target, and the
config schema version must be known to this binary. Restoring over an existing
installation requires --force and creates a pre-restore safety archive first.
Extra files already present in the target are never deleted; restored files
overwrite their counterparts.`,
		Args: cobra.ExactArgs(1),
		Example: `  picoclaw restore picoclaw-backup-20261120-120000.tar.gz
  picoclaw restore backup.tar.gz.enc --dry-run
  picoclaw restore backup.tar.gz --force
  picoclaw restore backup.tar.gz --no-ssh-key`,
		RunE: func(cmd *cobra.Command, args []string) error {
			home := resolveHome(opts.Home)
			configPath := resolveConfigPath(home, opts.Config)

			encrypted, err := backuppkg.IsEncrypted(args[0])
			if err != nil {
				return err
			}
			passphrase := ""
			if encrypted {
				passphrase, err = resolvePassphrase(opts.PassphraseFile, false)
				if err != nil {
					return err
				}
			}

			result, err := backuppkg.Restore(backuppkg.RestoreOptions{
				ArchivePath:    args[0],
				Home:           home,
				ConfigPath:     configPath,
				SSHKeyPath:     opts.SSHKey,
				Passphrase:     passphrase,
				Force:          opts.Force,
				AllowRunning:   opts.AllowRunning,
				NoSSHKey:       opts.NoSSHKey,
				NoSafetyBackup: opts.NoSafetyBackup,
				DryRun:         opts.DryRun,
			})
			if err != nil {
				return err
			}
			printRestoreResult(cmd.OutOrStdout(), result)
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Home, "home", "",
		"Target PicoClaw home (default: PICOCLAW_HOME or ~/.picoclaw)")
	cmd.Flags().StringVar(&opts.Config, "config", "",
		"Target config path (default: <home>/config.json or PICOCLAW_CONFIG)")
	cmd.Flags().StringVar(&opts.SSHKey, "ssh-key", "",
		"Target credential SSH key (default: PICOCLAW_SSH_KEY_PATH or ~/.ssh/picoclaw_ed25519.key)")
	cmd.Flags().StringVar(&opts.PassphraseFile, "passphrase-file", "",
		"Read the archive passphrase from a file instead of the terminal")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false,
		"Restore over an existing installation (creates a safety backup)")
	cmd.Flags().BoolVar(&opts.AllowRunning, "allow-running", false,
		"Best-effort restore while the gateway is running (not recommended)")
	cmd.Flags().BoolVar(&opts.NoSSHKey, "no-ssh-key", false, "Do not restore the credential SSH key")
	cmd.Flags().BoolVar(&opts.NoSafetyBackup, "no-safety-backup", false,
		"Do not create a pre-restore safety archive")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false,
		"Read the manifest and check the target without writing")

	return cmd
}

func printRestoreResult(out io.Writer, result *backuppkg.RestoreResult) {
	label := "Restore completed"
	if result.DryRun {
		label = "Restore plan (dry run)"
	}
	fmt.Fprintf(out, "%s: %s\n", label, result.ArchivePath)
	fmt.Fprintf(out, "  Home:      %s\n", result.Home)
	fmt.Fprintf(out, "  Files:     %d (%s)\n", result.Files, formatBytes(result.Bytes))
	if result.Dirs > 0 {
		fmt.Fprintf(out, "  Dirs:      %d\n", result.Dirs)
	}
	if result.Symlinks > 0 {
		fmt.Fprintf(out, "  Symlinks:  %d\n", result.Symlinks)
	}
	fmt.Fprintf(out, "  Config:    version %d\n", result.ConfigVersion)
	if result.ConfigMigrationNeeded {
		fmt.Fprintln(out, "  Config will be migrated on the next start")
	}
	if result.SSHKeyPath != "" {
		fmt.Fprintf(out, "  SSH key:   %s\n", result.SSHKeyPath)
	}
	if result.SafetyBackup != "" {
		fmt.Fprintf(out, "  Safety:    %s\n", result.SafetyBackup)
	}
	printWarnings(out, result.Warnings)
}
