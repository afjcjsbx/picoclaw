package backup

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/fileutil"
)

// excludedTopLevelDirs are runtime state that is recreated on the target.
var excludedTopLevelDirs = []ExcludedRule{
	{Path: "cache/", Reason: "cache: recreated automatically"},
	{Path: "logs/", Reason: "logs: machine-specific and recreated automatically"},
	{Path: "runtime-user-env/", Reason: "ephemeral sandbox HOME/TMPDIR/XDG directories"},
	{Path: ".artifacts/", Reason: "ephemeral spilled tool results"},
	{Path: "workspace/.artifacts/", Reason: "ephemeral spilled tool results"},
}

var rotatedLogPattern = regexp.MustCompile(`(?i)\.log(\.[0-9]+)?(\.gz)?$`)

var nonPortableDirs = map[string]string{
	"node_modules":  "reinstallable dependency tree",
	"__pycache__":   "language tool cache",
	".pytest_cache": "language tool cache",
	".mypy_cache":   "language tool cache",
	".ruff_cache":   "language tool cache",
	".venv":         "reinstallable virtual environment",
	"venv":          "reinstallable virtual environment",
	".tox":          "reinstallable virtual environment",
}

func excluded(rel string, isDir bool) (string, bool) {
	clean := path.Clean(rel)
	if clean == "." {
		return "", false
	}
	for _, rule := range excludedTopLevelDirs {
		base := strings.TrimSuffix(rule.Path, "/")
		if clean == base || strings.HasPrefix(clean, base+"/") {
			return rule.Reason, true
		}
	}

	base := path.Base(clean)
	if isDir {
		if reason, ok := nonPortableDirs[base]; ok {
			return reason, true
		}
		return "", false
	}
	switch base {
	case ".DS_Store", "Thumbs.db", "desktop.ini", ".picoclaw.pid":
		return "operating-system metadata", true
	}
	if strings.HasPrefix(base, "._") {
		return "macOS resource fork", true
	}
	if strings.HasPrefix(base, ".tmp-") || strings.HasSuffix(base, ".tmp") ||
		strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, ".swo") ||
		strings.HasSuffix(base, ".sock") || strings.HasSuffix(base, ".pid") ||
		strings.HasSuffix(base, "~") {
		return "temporary or runtime file", true
	}
	if rotatedLogPattern.MatchString(base) {
		return "log file", true
	}
	return "", false
}

type plannedEntry struct {
	entry Entry
	abs   string
}

type plannedFile struct {
	file ArchivedFile
	abs  string
}

type configSnapshot struct {
	Version           int
	HasEncryptedCreds bool
	Valid             bool
}

// readConfigSnapshot reads the two config values the backup needs. A missing
// or malformed config is not fatal: the file is still archived.
func readConfigSnapshot(configPath string) (configSnapshot, []string) {
	var snap configSnapshot
	var warnings []string

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			warnings = append(warnings, fmt.Sprintf("config %s was not found", configPath))
		} else {
			warnings = append(warnings, fmt.Sprintf("cannot read config %s: %v", configPath, err))
		}
		return snap, warnings
	}

	var raw struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		warnings = append(warnings, fmt.Sprintf("cannot parse config %s: %v", configPath, err))
		return snap, warnings
	}
	snap.Version = raw.Version
	if snap.Version < 0 {
		warnings = append(
			warnings,
			fmt.Sprintf("config %s has invalid version %d; recording 0", configPath, snap.Version),
		)
		snap.Version = 0
	}
	snap.HasEncryptedCreds = strings.Contains(string(data), "enc://")
	snap.Valid = true
	return snap, warnings
}

// DefaultSSHKeyPath returns the key used by credential encryption:
// PICOCLAW_SSH_KEY_PATH when set, otherwise ~/.ssh/picoclaw_ed25519.key.
func DefaultSSHKeyPath() string {
	if configured := strings.TrimSpace(os.Getenv("PICOCLAW_SSH_KEY_PATH")); configured != "" {
		return fileutil.ExpandHome(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "picoclaw_ed25519.key")
}

func pathWithin(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// secureArchivePath converts a slash-separated archive name into a relative
// filesystem path, rejecting absolute paths, traversal and non-portable names.
func secureArchivePath(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty archive path")
	}
	if strings.ContainsRune(name, 0) || strings.ContainsAny(name, `\:`) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return filepath.FromSlash(clean), nil
}

func safeLinkTarget(rel, target string) error {
	if target == "" {
		return fmt.Errorf("symlink %q has an empty target", rel)
	}
	if strings.ContainsRune(target, 0) || strings.ContainsAny(target, `\:`) || !utf8.ValidString(target) {
		return fmt.Errorf("symlink %q has an unsafe target %q", rel, target)
	}
	if path.IsAbs(target) {
		return fmt.Errorf("symlink %q points outside the backup root", rel)
	}
	resolved := path.Clean(path.Join(path.Dir(path.Clean(rel)), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("symlink %q escapes the backup root", rel)
	}
	return nil
}

func validRelativePath(rel string) bool {
	return utf8.ValidString(rel) && !strings.ContainsAny(rel, `\:`) && !strings.ContainsRune(rel, 0)
}

// walkHome selects every migratable entry under home.
func walkHome(home string, skip map[string]bool) ([]plannedEntry, []SkippedEntry, int, error) {
	var entries []plannedEntry
	var skipped []SkippedEntry
	skippedCount := 0
	source := home
	if info, err := os.Lstat(home); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(home)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("resolve %s: %w", home, err)
		}
		source = resolved
	}

	err := filepath.WalkDir(source, func(abs string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", abs, walkErr)
		}
		if abs == source {
			return nil
		}
		if skip[abs] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(source, abs)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !validRelativePath(rel) {
			skippedCount++
			if len(skipped) < maxSkippedEntries {
				skipped = append(skipped, SkippedEntry{Path: abs, Reason: "path is not portable or not valid UTF-8"})
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", abs, err)
		}
		if reason, ok := excluded(rel, d.IsDir()); ok {
			skippedCount++
			if len(skipped) < maxSkippedEntries {
				skipped = append(skipped, SkippedEntry{Path: abs, Reason: reason})
			}
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		entry := Entry{Path: rel, Mode: uint32(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime()}
		switch {
		case info.Mode().IsRegular():
			entry.Type = EntryFile
		case info.IsDir():
			entry.Type = EntryDir
			entry.Size = 0
		case info.Mode()&os.ModeSymlink != 0:
			target, readErr := os.Readlink(abs)
			if readErr != nil {
				return fmt.Errorf("readlink %s: %w", abs, readErr)
			}
			target = filepath.ToSlash(target)
			if linkErr := safeLinkTarget(rel, target); linkErr != nil {
				skippedCount++
				if len(skipped) < maxSkippedEntries {
					skipped = append(skipped, SkippedEntry{Path: abs, Reason: linkErr.Error()})
				}
				return nil
			}
			entry.Type = EntrySymlink
			entry.Size = 0
			entry.LinkTarget = target
		default:
			skippedCount++
			if len(skipped) < maxSkippedEntries {
				skipped = append(skipped, SkippedEntry{Path: abs, Reason: "unsupported file type"})
			}
			return nil
		}
		entries = append(entries, plannedEntry{entry: entry, abs: abs})
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}
	return entries, skipped, skippedCount, nil
}

func planFile(kind, source, archivePath string) (*plannedFile, error) {
	if !validRelativePath(archivePath) {
		return nil, fmt.Errorf("%s has a path that cannot be represented in the archive", source)
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", source)
	}
	return &plannedFile{
		file: ArchivedFile{
			Kind:        kind,
			Source:      source,
			ArchivePath: archivePath,
			Entry: Entry{
				Path: archivePath, Type: EntryFile,
				Mode: uint32(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime(),
			},
		},
		abs: source,
	}, nil
}

// needsFileExtra reports whether a file outside the home tree must be archived.
func needsFileExtra(home, file string) bool {
	if !pathWithin(home, file) {
		return true
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		realHome = home
	}
	realFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		return false
	}
	if !pathWithin(realHome, realFile) {
		return true
	}
	info, err := os.Lstat(file)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}
