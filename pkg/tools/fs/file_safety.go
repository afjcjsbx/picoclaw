package fstools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// protectedReadFs denies reads from secret stores and delegates writes to a
// policy that protects credentials and application state.
type protectedReadFs struct {
	fileSystem
	workspace string
	restrict  bool
}

type protectedWriteFs struct {
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

func (p *protectedReadFs) WriteFile(path string, data []byte) error {
	if err := checkSensitiveWritePath(path, p.workspace, p.restrict); err != nil {
		return err
	}
	return p.fileSystem.WriteFile(path, data)
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

func (p *protectedWriteFs) Open(path string) (fs.File, error) {
	if err := checkSensitiveWritePath(path, p.workspace, p.restrict); err != nil {
		return nil, err
	}
	return p.fileSystem.Open(path)
}

func (p *protectedWriteFs) WriteFile(path string, data []byte) error {
	if err := checkSensitiveWritePath(path, p.workspace, p.restrict); err != nil {
		return err
	}
	return p.fileSystem.WriteFile(path, data)
}

func checkSensitiveReadPath(path, workspace string, restrict bool) error {
	if err := checkNTNamespacePath(path); err != nil {
		return err
	}
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

func checkSensitiveWritePath(path, workspace string, restrict bool) error {
	if err := checkNTNamespacePath(path); err != nil {
		return err
	}
	if !filepath.IsAbs(path) && restrict {
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	resolved, err := resolvePathAgainstExistingAncestor(abs)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("write denied: could not safely resolve path: %w", err)
	}
	for _, candidate := range []string{abs, resolved} {
		if candidate != "" && (isSensitiveWritePath(candidate) || isProtectedSystemWritePath(candidate) ||
			isWorkspaceSessionPath(candidate, workspace)) {
			return fmt.Errorf("write denied: path targets protected credential, system, or session data")
		}
	}
	return nil
}

func checkNTNamespacePath(path string) error {
	// Inspect raw input first; resolving NT namespace UNC paths can trigger SMB
	// authentication on Windows.
	if !isNTNamespacePath(path) {
		return nil
	}
	return fmt.Errorf("access denied: Windows NT/device namespace paths are blocked before path resolution")
}

func isNTNamespacePath(path string) bool {
	path = strings.ReplaceAll(path, "/", `\`)
	if strings.HasPrefix(path, `\??\`) || strings.HasPrefix(path, `\\.\`) {
		return true
	}
	if strings.HasPrefix(path, `\\?\`) {
		rest := strings.ToUpper(path[4:])
		return strings.HasPrefix(rest, `UNC\`) || strings.HasPrefix(rest, `GLOBALROOT\`)
	}
	return false
}

func isSensitiveReadPath(path string) bool {
	return isSensitivePath(path, true)
}

func isSensitiveWritePath(path string) bool {
	return isSensitivePath(path, false)
}

func isSensitivePath(path string, blockEnvTemplates bool) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i, part := range parts {
		part = strings.ToLower(part)
		if part == ".env" || (strings.HasPrefix(part, ".env.") && (blockEnvTemplates || part != ".env.example")) ||
			part == ".envrc" || part == ".security.yml" || strings.HasPrefix(part, ".security.yml.") ||
			part == ".ssh" || part == ".aws" || part == ".kube" || part == ".gnupg" ||
			part == ".docker" || part == ".azure" || part == ".netrc" || part == ".npmrc" ||
			part == ".pypirc" || part == ".pgpass" || part == ".git-credentials" {
			return true
		}
		if part == ".config" && i+1 < len(parts) {
			next := strings.ToLower(parts[i+1])
			if next == "gh" || next == "gcloud" {
				return true
			}
		}
	}
	return isAppCredentialPath(path)
}

func isProtectedSystemWritePath(path string) bool {
	protected := []struct {
		path string
		dir  bool
	}{
		{path: "/etc/sudoers"},
		{path: "/etc/passwd"},
		{path: "/etc/shadow"},
		{path: "/etc/sudoers.d", dir: true},
		{path: "/etc/systemd", dir: true},
	}
	for _, target := range protected {
		roots := []string{filepath.Clean(target.path)}
		if resolved, err := resolvePathAgainstExistingAncestor(target.path); err == nil && resolved != roots[0] {
			roots = append(roots, resolved)
		}
		for _, root := range roots {
			if (!target.dir && filepath.Clean(path) == root) || (target.dir && isWithinWorkspace(path, root)) {
				return true
			}
		}
	}
	return false
}

func isWorkspaceSessionPath(path, workspace string) bool {
	if workspace == "" {
		return false
	}
	root, err := filepath.Abs(filepath.Join(workspace, "sessions"))
	if err != nil {
		return false
	}
	if isWithinWorkspace(path, root) {
		return true
	}
	resolved, err := resolvePathAgainstExistingAncestor(root)
	return err == nil && isWithinWorkspace(path, resolved)
}

func isAppCredentialPath(path string) bool {
	if isWithinProtectedRoot(path, config.GetHome(), []string{
		"auth.json",
		"auth/mcp",
		"plugin-data",
		"channels/weixin/context-tokens",
	}) {
		return true
	}

	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		codexHome = filepath.Join(userHome, ".codex")
	}
	return isWithinProtectedRoot(path, codexHome, []string{"auth.json"})
}

func isWithinProtectedRoot(path, root string, protectedPaths []string) bool {
	if isNTNamespacePath(root) {
		return false
	}
	home, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if matchesProtectedRelativePath(filepath.Clean(home), path, protectedPaths) {
		return true
	}
	if !mayBeProtectedPath(path, protectedPaths) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(home)
	return err == nil && matchesProtectedRelativePath(resolved, path, protectedPaths)
}

func matchesProtectedRelativePath(root, path string, protectedPaths []string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	rel = strings.ToLower(filepath.ToSlash(rel))
	for _, protected := range protectedPaths {
		if rel == protected || strings.HasPrefix(rel, protected+"/") {
			return true
		}
	}
	return false
}

func mayBeProtectedPath(path string, protectedPaths []string) bool {
	path = "/" + strings.Trim(strings.ToLower(filepath.ToSlash(filepath.Clean(path))), "/") + "/"
	for _, protected := range protectedPaths {
		if strings.Contains(path, "/"+protected+"/") {
			return true
		}
	}
	return false
}
