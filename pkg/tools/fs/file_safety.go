package fstools

import (
	"errors"
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

// AppendFile applies the write policy, then uses the underlying append
// implementation when available so a long file is not read and rewritten just
// to add a suffix.
func (p *protectedReadFs) AppendFile(path string, data []byte) error {
	// Keep the read policy in front of the append fast path: a path that the
	// read tools refuse must not become writable just because the tool no
	// longer reads it first.
	if err := p.check(path); err != nil {
		return err
	}
	if err := checkSensitiveWritePath(path, p.workspace, p.restrict); err != nil {
		return err
	}
	if appender, ok := p.fileSystem.(fileAppender); ok {
		return appender.AppendFile(path, data)
	}
	content, err := p.fileSystem.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return p.fileSystem.WriteFile(path, append(content, data...))
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

func (p *protectedWriteFs) AppendFile(path string, data []byte) error {
	if err := checkSensitiveWritePath(path, p.workspace, p.restrict); err != nil {
		return err
	}
	if appender, ok := p.fileSystem.(fileAppender); ok {
		return appender.AppendFile(path, data)
	}
	content, err := p.fileSystem.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return p.fileSystem.WriteFile(path, append(content, data...))
}

func checkSensitiveReadPath(path, workspace string, restrict bool) error {
	if err := checkNTNamespacePath(path); err != nil {
		return err
	}
	path = stripExtendedDrivePrefix(path)
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
	path = stripExtendedDrivePrefix(path)
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

// stripExtendedDrivePrefix turns \\?\C:\x into C:\x so the credential checks see
// the same path shape as a plain drive path; otherwise filepath.Rel against
// the protected roots fails on the volume mismatch and the match is skipped.
func stripExtendedDrivePrefix(path string) string {
	p := strings.ReplaceAll(path, "/", `\`)
	if len(p) >= 6 && strings.HasPrefix(p, `\\?\`) && p[5] == ':' &&
		(p[4]|0x20 >= 'a' && p[4]|0x20 <= 'z') {
		return p[4:]
	}
	return path
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
	protected := []string{
		"/etc/sudoers",
		"/etc/passwd",
		"/etc/shadow",
		"/etc/sudoers.d",
		"/etc/systemd",
	}
	for _, target := range protected {
		roots := []string{filepath.Clean(target)}
		if resolved, err := resolvePathAgainstExistingAncestor(target); err == nil && resolved != roots[0] {
			roots = append(roots, resolved)
		}
		for _, root := range roots {
			if matchesProtectedRelativePath(filepath.Dir(root), path, []string{filepath.Base(root)}) {
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
	if matchesProtectedRelativePath(filepath.Dir(root), path, []string{"sessions"}) {
		return true
	}
	resolved, err := resolvePathAgainstExistingAncestor(root)
	return err == nil && isWithinProtectedPath(path, resolved)
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
	for _, protected := range protectedPaths {
		resolved, err := resolvePathAgainstExistingAncestor(filepath.Join(home, filepath.FromSlash(protected)))
		if err == nil && isWithinProtectedPath(path, resolved) {
			return true
		}
	}
	return false
}

func matchesProtectedRelativePath(root, path string, protectedPaths []string) bool {
	rel, within := protectedRelativePath(root, path)
	if !within {
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

func isWithinProtectedPath(path, root string) bool {
	_, within := protectedRelativePath(root, path)
	return within
}

func protectedRelativePath(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return rel, true
	}
	if !isWithinWorkspace(strings.ToLower(path), strings.ToLower(root)) {
		return "", false
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", false
	}
	// Confirm identity before accepting a differently cased root.
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if info, statErr := os.Stat(current); statErr == nil && os.SameFile(rootInfo, info) {
			rel, err = filepath.Rel(current, path)
			return rel, err == nil && (rel == "." || filepath.IsLocal(rel))
		}
		if filepath.Dir(current) == current {
			return "", false
		}
	}
}
