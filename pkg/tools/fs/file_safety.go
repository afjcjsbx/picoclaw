package fstools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// credentialRootsCacheTTL bounds how long the resolved credential roots are
// reused. The roots only change when the app home or a protected symlink is
// retargeted, which is rare; a short TTL keeps a retarget visible without
// paying the resolution cost on every path check.
const credentialRootsCacheTTL = 30 * time.Second

// protectedRootRule is one "path must not be inside base/rel" rule. Keeping
// the base and the relative components separate preserves the case-insensitive
// fallback in matchesProtectedRelativePath, which a plain prefix check would
// lose for paths that do not exist yet.
type protectedRootRule struct {
	base string
	rels []string
}

// credentialRootsSnapshot holds the resolved protected roots shared by the
// read and write policies. Entries are immutable once published.
type credentialRootsSnapshot struct {
	key         string
	built       time.Time
	appRules    []protectedRootRule
	systemRules []protectedRootRule
}

var credentialRootsCache atomic.Pointer[credentialRootsSnapshot]

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

// absolutePath mirrors the workspace join that checkSensitiveReadPath applies,
// so entry paths are compared against the same root the directory check used.
func (p *protectedReadFs) absolutePath(path string) (string, error) {
	path = stripExtendedDrivePrefix(path)
	if !filepath.IsAbs(path) && p.restrict {
		path = filepath.Join(p.workspace, path)
	}
	return filepath.Abs(path)
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
	if len(entries) == 0 {
		return visible, nil
	}

	// The directory itself passed the full check above, so its resolved form
	// is safe to reuse for every child. A non-symlink child resolves to
	// resolvedDir/name; only symlink entries need their own EvalSymlinks.
	absDir, err := p.absolutePath(path)
	if err != nil {
		for _, entry := range entries {
			if p.check(filepath.Join(path, entry.Name())) == nil {
				visible = append(visible, entry)
			}
		}
		return visible, nil
	}
	resolvedDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		resolvedDir = absDir
	}
	sameDir := resolvedDir == absDir

	for _, entry := range entries {
		child := filepath.Join(absDir, entry.Name())
		if isSensitiveReadPath(child) {
			continue
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if resolved, err := filepath.EvalSymlinks(child); err == nil && isSensitiveReadPath(resolved) {
				continue
			}
		} else if !sameDir && isSensitiveReadPath(filepath.Join(resolvedDir, entry.Name())) {
			continue
		}
		visible = append(visible, entry)
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
	return isSensitiveNamePath(path, blockEnvTemplates) || isAppCredentialPath(path)
}

// isSensitiveNamePath blocks well-known secret file and directory names. It
// runs on every listed entry, so it lowercases the cleaned path once and
// splits the result instead of allocating a lowercased copy per segment.
func isSensitiveNamePath(path string, blockEnvTemplates bool) bool {
	parts := strings.Split(strings.ToLower(filepath.ToSlash(filepath.Clean(path))), "/")
	for i, part := range parts {
		if part == ".env" || part == ".envrc" || part == ".security.yml" ||
			strings.HasPrefix(part, ".security.yml.") ||
			part == ".ssh" || part == ".aws" || part == ".kube" || part == ".gnupg" ||
			part == ".docker" || part == ".azure" || part == ".netrc" || part == ".npmrc" ||
			part == ".pypirc" || part == ".pgpass" || part == ".git-credentials" {
			return true
		}
		if strings.HasPrefix(part, ".env.") && (blockEnvTemplates || part != ".env.example") {
			return true
		}
		if part == ".config" && i+1 < len(parts) {
			next := parts[i+1]
			if next == "gh" || next == "gcloud" {
				return true
			}
		}
	}
	return false
}

// protectedSystemWriteRoots are the absolute system paths a tool must never
// write to. They are resolved once per cache window instead of once per write.
func isProtectedSystemWritePath(path string) bool {
	for _, rule := range credentialRoots().systemRules {
		if protectedRuleMatches(path, rule) {
			return true
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
	for _, rule := range credentialRoots().appRules {
		if protectedRuleMatches(path, rule) {
			return true
		}
	}
	return false
}

// credentialRoots returns the cached protected-root snapshot, rebuilding it
// when the app home or CODEX_HOME changes or the entry expires.
func credentialRoots() *credentialRootsSnapshot {
	appHome := config.GetHome()
	codexHome := codexHomeDir()
	key := appHome + "\x00" + codexHome
	if cached := credentialRootsCache.Load(); cached != nil &&
		cached.key == key && time.Since(cached.built) < credentialRootsCacheTTL {
		return cached
	}
	fresh := &credentialRootsSnapshot{
		key:         key,
		built:       time.Now(),
		appRules:    buildAppCredentialRules(appHome, codexHome),
		systemRules: buildProtectedSystemWriteRules(),
	}
	credentialRootsCache.Store(fresh)
	return fresh
}

func codexHomeDir() string {
	if codexHome := os.Getenv("CODEX_HOME"); codexHome != "" {
		return codexHome
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(userHome, ".codex")
}

// buildAppCredentialRules expands each protected relative path under the app
// and codex homes into a lexical rule plus a rule for the symlink-resolved
// target. Resolving a protected root is only needed once per cache window: the
// final path check still resolves the path being accessed.
func buildAppCredentialRules(appHome, codexHome string) []protectedRootRule {
	rules := make([]protectedRootRule, 0, 6)
	add := func(root string, protectedPaths []string) {
		if root == "" || isNTNamespacePath(root) {
			return
		}
		home, err := filepath.Abs(root)
		if err != nil {
			return
		}
		home = filepath.Clean(home)
		rules = append(rules, protectedRootRule{base: home, rels: protectedPaths})
		for _, protected := range protectedPaths {
			resolved, err := resolvePathAgainstExistingAncestor(filepath.Join(home, filepath.FromSlash(protected)))
			if err != nil {
				continue
			}
			resolved = filepath.Clean(resolved)
			rules = append(rules, protectedRootRule{
				base: filepath.Dir(resolved),
				rels: []string{filepath.Base(resolved)},
			})
		}
	}
	add(appHome, []string{
		"auth.json",
		"auth/mcp",
		"plugin-data",
		"channels/weixin/context-tokens",
	})
	add(codexHome, []string{"auth.json"})
	return rules
}

func buildProtectedSystemWriteRules() []protectedRootRule {
	protected := []string{
		"/etc/sudoers",
		"/etc/passwd",
		"/etc/shadow",
		"/etc/sudoers.d",
		"/etc/systemd",
	}
	rules := make([]protectedRootRule, 0, len(protected)*2)
	add := func(target string) {
		target = filepath.Clean(target)
		rules = append(rules, protectedRootRule{
			base: filepath.Dir(target),
			rels: []string{filepath.Base(target)},
		})
	}
	for _, target := range protected {
		add(target)
		if resolved, err := resolvePathAgainstExistingAncestor(target); err == nil {
			add(resolved)
		}
	}
	return rules
}

func protectedRuleMatches(path string, rule protectedRootRule) bool {
	return hasPathPrefixFold(path, rule.base) &&
		matchesProtectedRelativePath(rule.base, path, rule.rels)
}

// hasPathPrefixFold is a cheap case-insensitive prefix guard that avoids a
// filepath.Rel call for the overwhelmingly common non-matching path.
func hasPathPrefixFold(path, prefix string) bool {
	if len(prefix) == 1 && os.IsPathSeparator(prefix[0]) {
		return len(path) > 0 && os.IsPathSeparator(path[0])
	}
	if len(path) < len(prefix) || !strings.EqualFold(path[:len(prefix)], prefix) {
		return false
	}
	return len(path) == len(prefix) || os.IsPathSeparator(path[len(prefix)])
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
