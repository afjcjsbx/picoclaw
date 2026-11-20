package backupcmd

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	backuppkg "github.com/sipeed/picoclaw/pkg/backup"
)

func promptPassphrase(confirm bool) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf(
			"passphrase required: set %s or pass --passphrase-file",
			backuppkg.PassphraseEnv,
		)
	}

	fmt.Fprint(os.Stderr, "Archive passphrase: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	if len(first) == 0 {
		return "", errors.New("passphrase must not be empty")
	}
	if !confirm {
		return string(first), nil
	}
	if len(first) < 8 {
		return "", errors.New("backup passphrase must be at least 8 characters")
	}

	fmt.Fprint(os.Stderr, "Confirm passphrase: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read passphrase confirmation: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("passphrases do not match")
	}
	return string(first), nil
}
