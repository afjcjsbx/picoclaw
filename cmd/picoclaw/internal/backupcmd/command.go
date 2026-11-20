package backupcmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	backuppkg "github.com/sipeed/picoclaw/pkg/backup"
	"github.com/sipeed/picoclaw/pkg/config"
)

type backupOptions struct {
	Home           string
	Config         string
	SSHKey         string
	Output         string
	Encrypt        bool
	PassphraseFile string
	AllowRunning   bool
	Force          bool
	DryRun         bool
}

// NewBackupCommand builds the `picoclaw backup` command.
func NewBackupCommand() *cobra.Command {
	opts := backupOptions{}
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a portable snapshot of config, sessions, memory, and skills",
		Long: `Create a gzip-compressed tar snapshot of a PicoClaw installation.

The snapshot contains PICOCLAW_HOME, the config and .security.yml files when
they live outside the home, and the credential SSH key used by enc:// API keys.
Caches, logs, temporary files and sandbox runtime directories are skipped. A
MANIFEST.json inside the archive records per-file hashes, sizes, modes and the
config schema version.

Use --encrypt to protect the archive with a passphrase (AES-256-GCM with a
scrypt-derived key). The passphrase is read from PICOCLAW_BACKUP_PASSPHRASE, from
--passphrase-file, or from the terminal; it is never stored in the archive.`,
		Args: cobra.NoArgs,
		Example: `  picoclaw backup
  picoclaw backup -o /media/usb/picoclaw.tar.gz
  picoclaw backup --encrypt
  picoclaw backup --dry-run`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := resolveHome(opts.Home)
			configPath := resolveConfigPath(home, opts.Config)

			passphrase := ""
			var err error
			if opts.Encrypt && !opts.DryRun {
				passphrase, err = resolvePassphrase(opts.PassphraseFile, true)
				if err != nil {
					return err
				}
			} else if opts.PassphraseFile != "" && !opts.Encrypt {
				return fmt.Errorf("--passphrase-file requires --encrypt")
			}

			result, err := backuppkg.Create(backuppkg.CreateOptions{
				Home:         home,
				ConfigPath:   configPath,
				SSHKeyPath:   opts.SSHKey,
				OutputPath:   opts.Output,
				Passphrase:   passphrase,
				Encrypt:      opts.Encrypt,
				AllowRunning: opts.AllowRunning,
				Force:        opts.Force,
				DryRun:       opts.DryRun,
				Version:      config.GetVersion(),
			})
			if err != nil {
				return err
			}
			printBackupResult(cmd.OutOrStdout(), result)
			return nil
		},
	}

	cmd.Flags().StringVarP(&opts.Output, "output", "o", "",
		"Output archive (default: picoclaw-backup-<timestamp>.tar.gz in the current directory)")
	cmd.Flags().StringVar(&opts.Home, "home", "",
		"PicoClaw home to back up (default: PICOCLAW_HOME or ~/.picoclaw)")
	cmd.Flags().StringVar(&opts.Config, "config", "",
		"Config file to back up (default: <home>/config.json or PICOCLAW_CONFIG)")
	cmd.Flags().StringVar(&opts.SSHKey, "ssh-key", "",
		"Credential SSH key (default: PICOCLAW_SSH_KEY_PATH or ~/.ssh/picoclaw_ed25519.key)")
	cmd.Flags().BoolVar(&opts.Encrypt, "encrypt", false,
		"Encrypt the archive with a passphrase (AES-256-GCM + scrypt)")
	cmd.Flags().StringVar(&opts.PassphraseFile, "passphrase-file", "",
		"Read the archive passphrase from a file instead of the terminal")
	cmd.Flags().BoolVar(&opts.AllowRunning, "allow-running", false,
		"Best-effort backup while the gateway is running (not recommended)")
	cmd.Flags().BoolVarP(&opts.Force, "force", "f", false, "Overwrite an existing output archive")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Show what would be archived without writing")

	return cmd
}

func resolveHome(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	return internal.GetPicoclawHome()
}

func resolveConfigPath(home, flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return flagValue
	}
	if configured := os.Getenv(config.EnvConfig); configured != "" {
		return configured
	}
	return filepath.Join(home, "config.json")
}

func printBackupResult(out io.Writer, result *backuppkg.Result) {
	label := "Backup created"
	if result.DryRun {
		label = "Backup plan (dry run)"
	}
	fmt.Fprintf(out, "%s: %s\n", label, result.OutputPath)
	fmt.Fprintf(out, "  Home:      %s\n", result.Manifest.Home)
	fmt.Fprintf(out, "  Files:     %d (%s)\n", result.Manifest.FileCount, formatBytes(result.Manifest.TotalBytes))
	if result.Manifest.DirCount > 0 {
		fmt.Fprintf(out, "  Dirs:      %d\n", result.Manifest.DirCount)
	}
	if result.Manifest.SymlinkCount > 0 {
		fmt.Fprintf(out, "  Symlinks:  %d\n", result.Manifest.SymlinkCount)
	}
	fmt.Fprintf(out, "  Config:    version %d\n", result.Manifest.ConfigVersion)
	for _, file := range result.Manifest.Files {
		fmt.Fprintf(out, "  File:      %s %s\n", sanitizeMessage(file.Kind), sanitizeMessage(file.Source))
	}
	if result.Encrypted {
		fmt.Fprintln(out, "  Encrypted: yes (AES-256-GCM, scrypt)")
	}
	if !result.DryRun {
		fmt.Fprintf(out, "  Archive:   %s\n", formatBytes(result.Bytes))
	}
	if result.Manifest.SkippedCount > 0 {
		fmt.Fprintf(out, "  Skipped:   %d ephemeral or unsafe entries\n", result.Manifest.SkippedCount)
	}
	printWarnings(out, result.Manifest.Warnings)
}

const maxPrintedWarnings = 50

func printWarnings(out io.Writer, warnings []string) {
	for i, warning := range warnings {
		if i >= maxPrintedWarnings {
			fmt.Fprintf(out, "  warning: ... and %d more\n", len(warnings)-i)
			return
		}
		fmt.Fprintf(out, "  warning: %s\n", sanitizeMessage(warning))
	}
}

func sanitizeMessage(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f:
			return '?'
		default:
			return r
		}
	}, value)
}

func resolvePassphrase(file string, confirm bool) (string, error) {
	if env := os.Getenv(backuppkg.PassphraseEnv); strings.TrimSpace(env) != "" {
		return env, nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read passphrase file: %w", err)
		}
		passphrase := strings.TrimRight(string(data), "\r\n")
		if strings.TrimSpace(passphrase) == "" {
			return "", fmt.Errorf("passphrase file %s is empty", file)
		}
		return passphrase, nil
	}
	return promptPassphrase(confirm)
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
