// Package dashboardsetup provisions the one-time grant used to claim an
// uninitialized launcher dashboard.
package dashboardsetup

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// FileName is the owner-only file containing the current setup token.
	FileName   = "dashboard-setup-token"
	tokenBytes = 32
)

// TokenPath returns the setup token path inside the PicoClaw home directory.
func TokenPath(home string) string {
	return filepath.Join(home, FileName)
}

// NewToken generates a 256-bit, URL-safe setup token.
func NewToken() (string, error) {
	var buf [tokenBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate dashboard setup token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// WriteToken atomically stores token in an owner-readable file. Existing token
// files are replaced, including their permissions, so a stale permissive file
// cannot expose a newly generated token.
func WriteToken(path, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("dashboard setup token must not be empty")
	}
	if err := writeTokenFile(path, []byte(token+"\n")); err != nil {
		return fmt.Errorf("write dashboard setup token: %w", err)
	}
	return nil
}
