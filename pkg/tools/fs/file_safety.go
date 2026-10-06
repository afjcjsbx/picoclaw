package fstools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// protectedReadFs denies file content and metadata access to common secret
// stores. Writes are passed through so users can still create these files.
type protectedReadFs struct {
	fileSystem
	workspace string
	restrict  bool
}

func (p *protectedReadFs) check(path string) error {
	return checkSensitiveReadPath(path, p.workspace, p.restrict)
}

func (p *protectedReadFs) ReadFile(path string) ([]byte, error) {
	if err := p.check(path); err != nil {
		return nil, err
	}
	return p.fileSystem.ReadFile(path)
}

func (p *protectedReadFs) Open(path string) (fs.File, error) {
	if err := p.check(path); err != nil {
		return nil, err
	}
	return p.fileSystem.Open(path)
}

func (p *protectedReadFs) Stat(path string) (fs.FileInfo, error) {
	if err := p.check(path); err != nil {
		return nil, err
	}
	return p.fileSystem.Stat(path)
}

func (p *protectedReadFs) ReadDir(path string) ([]os.DirEntry, error) {
	if err := p.check(path); err != nil {
		return nil, err
	}
	entries, err := p.fileSystem.ReadDir(path)
	if err != nil {
		return nil, err
	}
	visible := entries[:0]
	for _, entry := range entries {
		if p.check(filepath.Join(path, entry.Name())) == nil {
			visible = append(visible, entry)
		}
	}
	return visible, nil
}

func checkSensitiveReadPath(path, workspace string, restrict bool) error {
	if !filepath.IsAbs(path) && restrict {
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if isSensitiveReadPath(abs) {
		return fmt.Errorf("access denied: path contains a protected secret file or directory")
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && isSensitiveReadPath(resolved) {
		return fmt.Errorf("access denied: path resolves to a protected secret file or directory")
	}
	return nil
}

func isSensitiveReadPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		part = strings.ToLower(part)
		if part == ".env" || strings.HasPrefix(part, ".env.") ||
			part == ".envrc" || part == ".security.yml" || strings.HasPrefix(part, ".security.yml.") ||
			part == ".ssh" || part == ".aws" || part == ".kube" || part == ".gnupg" ||
			part == ".netrc" || part == ".npmrc" || part == ".pypirc" {
			return true
		}
	}
	return false
}
