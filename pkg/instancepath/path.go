// Package instancepath owns private runtime storage beneath the PicoClaw home.
// It deliberately does not use the process working directory or system temp.
package instancepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg"
)

const EnvHome = "PICOCLAW_HOME"

// Home resolves the instance home without creating it. Missing home information
// is an error: falling back to cwd would mix unrelated instances' private data.
func Home() (string, error) {
	home := os.Getenv(EnvHome)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || userHome == "" {
			return "", fmt.Errorf("cannot resolve PicoClaw home; set %s", EnvHome)
		}
		home = filepath.Join(userHome, pkg.DefaultPicoClawHome)
	}
	if strings.TrimSpace(home) == "" || filepath.Clean(home) == "." {
		return "", fmt.Errorf("invalid PicoClaw home %q", home)
	}
	return filepath.Abs(home)
}

// PrivateDir creates a private directory under the instance home. Existing
// managed directories must belong to this user and may not be symlinks.
func PrivateDir(parts ...string) (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	if err := secureDir(home); err != nil {
		return "", err
	}
	for _, part := range parts {
		if !filepath.IsLocal(part) || part == "." || strings.ContainsAny(part, `/\`) {
			return "", fmt.Errorf("invalid instance directory component %q", part)
		}
		home = filepath.Join(home, part)
		if err := secureDir(home); err != nil {
			return "", err
		}
	}
	return home, nil
}

func secureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory %s: %w", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory %s must be a directory, not a symlink", path)
	}
	if err := checkOwner(info); err != nil {
		return fmt.Errorf("private directory %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure private directory %s: %w", path, err)
	}
	return nil
}

// TempDir returns storage for one class of disposable runtime files.
func TempDir(kind string) (string, error) {
	return PrivateDir("tmp", kind)
}

// CreateTemp creates a private working file. The caller must close and remove it.
func CreateTemp(pattern string) (*os.File, error) {
	dir, err := TempDir("work")
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, pattern)
}

// MkdirTemp creates an operation directory. The caller must remove it when done.
func MkdirTemp(pattern string) (string, error) {
	dir, err := TempDir("work")
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, pattern)
}
