package media

import (
	"io"
	"os"

	"github.com/sipeed/picoclaw/pkg/instancepath"
)

const TempDirName = "media"

// TempDir returns private media storage for this PicoClaw instance.
func TempDir() (string, error) {
	return instancepath.TempDir(TempDirName)
}

// CreateTemp creates media with an exclusive name and owner-only permissions.
// The caller owns the file until it successfully registers it with a store.
func CreateTemp(pattern string) (*os.File, error) {
	dir, err := TempDir()
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, pattern)
}

// WriteTemp stages a complete media file, removing partial output on any error.
func WriteTemp(pattern string, src io.Reader) (string, error) {
	f, err := CreateTemp(pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := io.Copy(f, src); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
