package plugins

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxPackageFile = 2 << 20

func canonicalRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", root)
	}
	return root, nil
}

// ResolvePath validates the filesystem-resolved target, including symlinks.
func ResolvePath(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes plugin boundary: %s", path)
	}
	return resolved, nil
}

// ReadPackageFile uses a traversal-resistant root handle for the actual open,
// so swapping a symlink after validation cannot redirect the read outside root.
func ReadPackageFile(root, path string) ([]byte, error) {
	resolved, err := ResolvePath(root, path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return nil, err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	pathInfo, err := dir.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	file, err := dir.Open(rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPackageFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPackageFile {
		return nil, fmt.Errorf("package file exceeds %d bytes", maxPackageFile)
	}
	return data, nil
}

func componentExists(root, name string) bool {
	_, err := os.Lstat(filepath.Join(root, name))
	return !os.IsNotExist(err)
}
