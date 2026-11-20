package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
)

// RestoreOptions controls a restore run.
type RestoreOptions struct {
	ArchivePath    string
	Home           string
	ConfigPath     string
	SSHKeyPath     string
	Passphrase     string
	Force          bool
	AllowRunning   bool
	NoSSHKey       bool
	NoSafetyBackup bool
	DryRun         bool
	Now            time.Time
}

// RestoreResult describes a completed or planned restore.
type RestoreResult struct {
	ArchivePath           string
	Home                  string
	Files                 int
	Dirs                  int
	Symlinks              int
	Bytes                 int64
	SafetyBackup          string
	SSHKeyPath            string
	ConfigVersion         int
	ConfigMigrationNeeded bool
	Warnings              []string
	DryRun                bool
}

type extractedInfo struct {
	kind   string
	size   int64
	sha256 string
	link   string
}

// Inspect reads the manifest without extracting the archive.
func Inspect(archivePath, passphrase string) (*Manifest, error) {
	tr, closeFn, err := openArchive(archivePath, passphrase)
	if err != nil {
		return nil, err
	}
	defer closeFn()

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if path.Clean(header.Name) != ManifestName {
			continue
		}
		data, readErr := readLimited(tr, maxManifestSize)
		if readErr != nil {
			return nil, readErr
		}
		return parseManifest(data)
	}
	return nil, fmt.Errorf("archive has no %s", ManifestName)
}

// Restore validates and writes a snapshot to a target installation.
func Restore(opts RestoreOptions) (*RestoreResult, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	archivePath, err := absolutePath(opts.ArchivePath)
	if err != nil {
		return nil, fmt.Errorf("resolve archive: %w", err)
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("archive %s: %w", archivePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("archive %s is not a regular file", archivePath)
	}

	home, err := absolutePath(opts.Home)
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	if homeErr := validateHomePath(home); homeErr != nil {
		return nil, homeErr
	}
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = filepath.Join(home, "config.json")
	}
	if configPath, err = absolutePath(configPath); err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	sshKeyPath := opts.SSHKeyPath
	if sshKeyPath == "" {
		sshKeyPath = DefaultSSHKeyPath()
	}
	if sshKeyPath != "" {
		if sshKeyPath, err = absolutePath(sshKeyPath); err != nil {
			return nil, fmt.Errorf("resolve SSH key path: %w", err)
		}
	}

	encrypted, err := IsEncrypted(archivePath)
	if err != nil {
		return nil, err
	}
	if encrypted && strings.TrimSpace(opts.Passphrase) == "" {
		return nil, ErrPassphraseRequired
	}
	if !opts.AllowRunning && gatewayRunning(home) {
		return nil, fmt.Errorf(
			"the PicoClaw gateway is running for %s; stop it before restoring (or pass --allow-running)",
			home,
		)
	}

	if opts.DryRun {
		manifest, inspectErr := Inspect(archivePath, opts.Passphrase)
		if inspectErr != nil {
			return nil, inspectErr
		}
		result := resultFromManifest(manifest, archivePath, home)
		result.DryRun = true
		result.Warnings = append(result.Warnings, manifest.Warnings...)
		if !opts.NoSSHKey {
			result.SSHKeyPath = targetSSHKey(manifest, sshKeyPath)
		}
		if versionErr := checkConfigVersion(manifest, "", opts.Force, result); versionErr != nil {
			return nil, versionErr
		}
		targetExists, existsErr := targetInstallationExists(home, configPath)
		if existsErr != nil {
			return nil, existsErr
		}
		if targetExists && !opts.Force {
			return nil, fmt.Errorf("target %s already contains a PicoClaw installation (use --force)", home)
		}
		return result, nil
	}

	parent := filepath.Dir(home)
	if mkdirErr := os.MkdirAll(parent, 0o755); mkdirErr != nil {
		return nil, fmt.Errorf("create target parent: %w", mkdirErr)
	}
	staging, stagingErr := os.MkdirTemp(parent, ".picoclaw-restore-*")
	if stagingErr != nil {
		return nil, fmt.Errorf("create staging directory: %w", stagingErr)
	}
	defer os.RemoveAll(staging)

	manifest, extracted, extractErr := extractArchive(archivePath, opts.Passphrase, staging)
	if extractErr != nil {
		return nil, extractErr
	}
	if validateErr := validateEntries(manifest, extracted); validateErr != nil {
		return nil, validateErr
	}

	result := resultFromManifest(manifest, archivePath, home)
	result.Warnings = append(result.Warnings, manifest.Warnings...)
	if !opts.NoSSHKey {
		result.SSHKeyPath = targetSSHKey(manifest, sshKeyPath)
	} else if hasFileKind(manifest, FileKindSSHKey) {
		result.Warnings = append(result.Warnings, "credential SSH key will not be restored (--no-ssh-key)")
	}

	stagedConfig, stagedErr := stagedConfigPath(manifest, staging)
	if stagedErr != nil {
		return nil, stagedErr
	}
	if versionErr := checkConfigVersion(manifest, stagedConfig, opts.Force, result); versionErr != nil {
		return nil, versionErr
	}

	exists, existsErr := targetInstallationExists(home, configPath)
	if existsErr != nil {
		return nil, existsErr
	}
	if exists && !opts.Force {
		return nil, fmt.Errorf("target %s already contains a PicoClaw installation (use --force)", home)
	}
	if exists && !opts.NoSafetyBackup {
		safety, safetyErr := createSafetyBackup(home, configPath, sshKeyPath, encrypted, opts.Passphrase, now)
		if safetyErr != nil {
			return nil, fmt.Errorf("create pre-restore safety backup: %w", safetyErr)
		}
		result.SafetyBackup = safety
	}

	cfgSnap, cfgWarnings := configSnapshotFromStaged(stagedConfig)
	result.Warnings = append(result.Warnings, cfgWarnings...)
	if preflightErr := preflightFiles(staging, manifest, configPath, sshKeyPath, opts); preflightErr != nil {
		return nil, preflightErr
	}

	rootFiles, dirs, symlinks, restoreErr := restoreHome(staging, home, manifest.Entries, opts.Force, &result.Warnings)
	if restoreErr != nil {
		return nil, restoreErr
	}
	restoredFiles, sshPath, filesErr := restoreFiles(staging, manifest, configPath, sshKeyPath, opts)
	if filesErr != nil {
		return nil, filesErr
	}
	result.Files = rootFiles + restoredFiles
	result.Dirs = dirs
	result.Symlinks = symlinks
	result.SSHKeyPath = sshPath

	if cfgSnap.HasEncryptedCreds && !fileExists(sshKeyPath) {
		result.Warnings = append(result.Warnings,
			"config uses enc:// credentials but no SSH key was restored; set the key and PICOCLAW_KEY_PASSPHRASE")
	}
	rewriteConfigHome(configPath, manifest.Home, home, &result.Warnings)
	hardenRestoredPermissions(home, configPath, sshKeyPath, &result.Warnings)
	return result, nil
}

func resultFromManifest(manifest *Manifest, archivePath, home string) *RestoreResult {
	return &RestoreResult{
		ArchivePath: archivePath, Home: home,
		Files: manifest.FileCount, Dirs: manifest.DirCount, Symlinks: manifest.SymlinkCount,
		Bytes: manifest.TotalBytes, ConfigVersion: manifest.ConfigVersion,
	}
}

func targetSSHKey(manifest *Manifest, configured string) string {
	if configured != "" && hasFileKind(manifest, FileKindSSHKey) {
		return configured
	}
	return ""
}

func hasFileKind(manifest *Manifest, kind string) bool {
	for _, file := range manifest.Files {
		if file.Kind == kind {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func openArchive(archivePath, passphrase string) (*tar.Reader, func(), error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, err
	}
	closeFile := func() { _ = file.Close() }
	buffered := bufio.NewReaderSize(file, 64*1024)
	head, _ := buffered.Peek(len(cryptMagic))
	var reader io.Reader = buffered
	if len(head) == len(cryptMagic) && bytes.Equal(head, cryptMagic) {
		if passphrase == "" {
			closeFile()
			return nil, nil, ErrPassphraseRequired
		}
		decrypted, decryptErr := newDecryptReader(buffered, passphrase)
		if decryptErr != nil {
			closeFile()
			return nil, nil, decryptErr
		}
		reader = decrypted
	}
	gz, err := gzip.NewReader(reader)
	if err != nil {
		closeFile()
		if errors.Is(err, ErrDecrypt) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("open gzip stream: %w", err)
	}
	return tar.NewReader(gz), func() { _ = gz.Close(); closeFile() }, nil
}

func extractArchive(archivePath, passphrase, staging string) (*Manifest, map[string]extractedInfo, error) {
	tr, closeFn, err := openArchive(archivePath, passphrase)
	if err != nil {
		return nil, nil, err
	}
	defer closeFn()

	extracted := make(map[string]extractedInfo)
	seen := make(map[string]bool)
	type pendingLink struct{ rel, target string }
	var links []pendingLink
	type pendingDir struct {
		rel   string
		entry Entry
	}
	var dirs []pendingDir
	var manifest *Manifest
	totalBytes := int64(0)
	items := 0

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read archive: %w", err)
		}
		items++
		if items > maxArchiveItems {
			return nil, nil, fmt.Errorf("archive exceeds the %d-entry safety limit", maxArchiveItems)
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			totalBytes += header.Size
			if totalBytes > maxArchiveBytes {
				return nil, nil, fmt.Errorf("archive exceeds the %d-byte safety limit", maxArchiveBytes)
			}
		}

		cleanName := path.Clean(header.Name)
		if cleanName == ManifestName {
			if manifest != nil || seen[ManifestName] {
				return nil, nil, fmt.Errorf("duplicate %s entry", ManifestName)
			}
			seen[ManifestName] = true
			data, readErr := readLimited(tr, maxManifestSize)
			if readErr != nil {
				return nil, nil, readErr
			}
			manifest, err = parseManifest(data)
			if err != nil {
				return nil, nil, err
			}
			continue
		}
		if seen[cleanName] {
			return nil, nil, fmt.Errorf("duplicate archive entry %q", header.Name)
		}
		seen[cleanName] = true
		if !strings.HasPrefix(cleanName, ArchiveHomePrefix) && !strings.HasPrefix(cleanName, ArchiveExtraPrefix) {
			return nil, nil, fmt.Errorf("archive entry %q is outside the PicoClaw tree", header.Name)
		}
		rel, err := secureArchivePath(header.Name)
		if err != nil {
			return nil, nil, err
		}
		target := filepath.Join(staging, rel)
		if !pathWithin(staging, target) {
			return nil, nil, fmt.Errorf("archive entry %q escapes the staging directory", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return nil, nil, fmt.Errorf("create directory %s: %w", target, err)
			}
			dirs = append(dirs, pendingDir{rel: cleanName, entry: Entry{
				Path: cleanName, Mode: uint32(header.Mode) & 0o777, ModTime: header.ModTime,
			}})
			extracted[cleanName] = extractedInfo{kind: EntryDir}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return nil, nil, fmt.Errorf("create parent for %s: %w", target, err)
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return nil, nil, fmt.Errorf("create %s: %w", target, err)
			}
			hasher := sha256.New()
			size, copyErr := io.Copy(io.MultiWriter(file, hasher), tr)
			closeErr := file.Close()
			if copyErr != nil {
				return nil, nil, fmt.Errorf("extract %s: %w", target, copyErr)
			}
			if closeErr != nil {
				return nil, nil, fmt.Errorf("close %s: %w", target, closeErr)
			}
			if size != header.Size {
				return nil, nil, fmt.Errorf("short read for %s", header.Name)
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)&0o777); err != nil {
				return nil, nil, fmt.Errorf("chmod %s: %w", target, err)
			}
			if !header.ModTime.IsZero() {
				_ = os.Chtimes(target, header.ModTime, header.ModTime)
			}
			extracted[cleanName] = extractedInfo{
				kind: EntryFile, size: size, sha256: hex.EncodeToString(hasher.Sum(nil)),
			}
		case tar.TypeSymlink:
			if err := safeLinkTarget(cleanName, header.Linkname); err != nil {
				return nil, nil, err
			}
			links = append(links, pendingLink{rel: cleanName, target: header.Linkname})
			extracted[cleanName] = extractedInfo{kind: EntrySymlink, link: filepath.ToSlash(header.Linkname)}
		default:
			return nil, nil, fmt.Errorf("unsupported archive entry type for %q", header.Name)
		}
	}
	if manifest == nil {
		return nil, nil, fmt.Errorf("archive has no %s", ManifestName)
	}

	for _, link := range links {
		target := filepath.Join(staging, filepath.FromSlash(link.rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, nil, fmt.Errorf("create symlink parent: %w", err)
		}
		if err := os.Symlink(link.target, target); err != nil {
			return nil, nil, fmt.Errorf("create symlink %s: %w", target, err)
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		target := filepath.Join(staging, filepath.FromSlash(dirs[i].rel))
		if err := applyMetadata(target, dirs[i].entry); err != nil {
			return nil, nil, fmt.Errorf("apply directory metadata %s: %w", target, err)
		}
	}
	return manifest, extracted, nil
}

func validateEntries(manifest *Manifest, extracted map[string]extractedInfo) error {
	expected := make(map[string]Entry, len(manifest.Entries)+len(manifest.Files))
	for _, entry := range manifest.Entries {
		rel, err := secureArchivePath(entry.Path)
		if err != nil {
			return fmt.Errorf("manifest entry %q: %w", entry.Path, err)
		}
		archivePath := ArchiveHomePrefix + filepath.ToSlash(rel)
		if _, duplicate := expected[archivePath]; duplicate {
			return fmt.Errorf("duplicate manifest entry %q", archivePath)
		}
		expected[archivePath] = entry
	}
	for _, file := range manifest.Files {
		if _, duplicate := expected[file.ArchivePath]; duplicate {
			return fmt.Errorf("duplicate manifest file %q", file.ArchivePath)
		}
		expected[file.ArchivePath] = file.Entry
	}

	for archivePath := range expected {
		for parent := path.Dir(archivePath); parent != "." && parent != "/"; parent = path.Dir(parent) {
			parentEntry, ok := expected[parent]
			if !ok {
				continue
			}
			if parentEntry.Type == EntrySymlink {
				return fmt.Errorf("archive entry %q is below symlink %q", archivePath, parent)
			}
			if parentEntry.Type != EntryDir {
				return fmt.Errorf("archive entry %q is below non-directory %q", archivePath, parent)
			}
		}
	}

	for archivePath, entry := range expected {
		info, ok := extracted[archivePath]
		if !ok {
			return fmt.Errorf("archive is missing %q", archivePath)
		}
		if info.kind != entry.Type {
			return fmt.Errorf("archive entry %q has type %s, manifest says %s", archivePath, info.kind, entry.Type)
		}
		switch entry.Type {
		case EntryFile:
			if info.size != entry.Size || !strings.EqualFold(info.sha256, entry.SHA256) {
				return fmt.Errorf("checksum mismatch for %q", archivePath)
			}
		case EntrySymlink:
			if err := safeLinkTarget(entry.Path, entry.LinkTarget); err != nil {
				return err
			}
			if info.link != filepath.ToSlash(entry.LinkTarget) {
				return fmt.Errorf("symlink target mismatch for %q", archivePath)
			}
		case EntryDir:
		default:
			return fmt.Errorf("manifest entry %q has unknown type %q", archivePath, entry.Type)
		}
	}
	for archivePath := range extracted {
		if _, ok := expected[archivePath]; !ok {
			return fmt.Errorf("archive contains unexpected entry %q", archivePath)
		}
	}
	return nil
}

func parseManifest(data []byte) (*Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if manifest.Format != FormatName {
		return nil, fmt.Errorf("not a PicoClaw backup (format %q)", manifest.Format)
	}
	if manifest.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("backup format version %d is not supported (expected %d)",
			manifest.FormatVersion, FormatVersion)
	}
	if manifest.Home == "" || !filepath.IsAbs(manifest.Home) {
		return nil, fmt.Errorf("backup manifest has no absolute home")
	}
	if manifest.ConfigVersion < 0 {
		return nil, fmt.Errorf("backup manifest has invalid config version %d", manifest.ConfigVersion)
	}
	seenKinds := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		switch file.Kind {
		case FileKindConfig, FileKindSecurity, FileKindSSHKey:
		default:
			return nil, fmt.Errorf("backup manifest has unknown file kind %q", file.Kind)
		}
		if seenKinds[file.Kind] {
			return nil, fmt.Errorf("backup manifest has duplicate %q file", file.Kind)
		}
		seenKinds[file.Kind] = true
		if _, err := secureArchivePath(file.ArchivePath); err != nil {
			return nil, fmt.Errorf("backup manifest file %q: %w", file.ArchivePath, err)
		}
		if file.Entry.Type != EntryFile {
			return nil, fmt.Errorf("backup manifest file %q is not a regular file", file.ArchivePath)
		}
	}
	return &manifest, nil
}

func stagedConfigPath(manifest *Manifest, staging string) (string, error) {
	if hasFileKind(manifest, FileKindConfig) {
		for _, file := range manifest.Files {
			if file.Kind == FileKindConfig {
				rel, err := secureArchivePath(file.ArchivePath)
				if err != nil {
					return "", err
				}
				return filepath.Join(staging, rel), nil
			}
		}
	}
	if !pathWithin(manifest.Home, manifest.ConfigPath) {
		return "", nil
	}
	rel, err := filepath.Rel(manifest.Home, manifest.ConfigPath)
	if err != nil {
		return "", err
	}
	rel, err = secureArchivePath(filepath.ToSlash(rel))
	if err != nil {
		return "", err
	}
	return filepath.Join(staging, filepath.FromSlash(strings.TrimSuffix(ArchiveHomePrefix, "/")), rel), nil
}

func configSnapshotFromStaged(stagedConfig string) (configSnapshot, []string) {
	if stagedConfig == "" {
		return configSnapshot{}, nil
	}
	return readConfigSnapshot(stagedConfig)
}

func checkConfigVersion(manifest *Manifest, stagedConfig string, force bool, result *RestoreResult) error {
	actual := manifest.ConfigVersion
	if stagedConfig != "" {
		snap, warnings := readConfigSnapshot(stagedConfig)
		result.Warnings = append(result.Warnings, warnings...)
		if snap.Valid {
			if snap.Version != manifest.ConfigVersion {
				message := fmt.Sprintf("config version mismatch: manifest says %d, file says %d",
					manifest.ConfigVersion, snap.Version)
				if !force {
					return fmt.Errorf("%s (use --force to proceed)", message)
				}
				result.Warnings = append(result.Warnings, message)
			}
			actual = snap.Version
		}
	}
	result.ConfigVersion = actual
	if actual > config.CurrentVersion {
		message := fmt.Sprintf(
			"backup config version %d is newer than this binary supports (%d); upgrade PicoClaw before restoring",
			actual,
			config.CurrentVersion,
		)
		if !force {
			return errors.New(message)
		}
		result.Warnings = append(result.Warnings, message)
	}
	if actual > 0 && actual < config.CurrentVersion {
		result.ConfigMigrationNeeded = true
		result.Warnings = append(result.Warnings, fmt.Sprintf("config version %d will be migrated to %d on first start",
			actual, config.CurrentVersion))
	}
	if actual == 0 && stagedConfig != "" {
		result.ConfigMigrationNeeded = true
		result.Warnings = append(
			result.Warnings,
			"legacy config without a version field will be migrated on first start",
		)
	}
	return nil
}

func targetInstallationExists(home, configPath string) (bool, error) {
	info, err := os.Stat(home)
	if err != nil {
		if os.IsNotExist(err) {
			if fileExists(configPath) {
				return true, nil
			}
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("target %s is not a directory", home)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return false, err
	}
	return len(entries) > 0 || fileExists(configPath), nil
}

func createSafetyBackup(
	home, configPath, sshKeyPath string,
	encrypted bool,
	passphrase string,
	now time.Time,
) (string, error) {
	if !fileExists(home) {
		if err := os.MkdirAll(home, 0o700); err != nil {
			return "", fmt.Errorf("create target home for safety backup: %w", err)
		}
	}
	parent := filepath.Dir(home)
	base := filepath.Base(home)
	output := filepath.Join(parent, fmt.Sprintf("%s.pre-restore-%s.tar.gz", base, now.Format("20060102-150405")))
	for i := 1; ; i++ {
		if !preRestoreExists(output, encrypted) {
			break
		}
		output = filepath.Join(
			parent,
			fmt.Sprintf("%s.pre-restore-%s-%d.tar.gz", base, now.Format("20060102-150405"), i),
		)
	}
	result, err := Create(CreateOptions{
		Home: home, ConfigPath: configPath, SSHKeyPath: sshKeyPath,
		OutputPath: output, Encrypt: encrypted, Passphrase: passphrase,
		AllowRunning: true, Version: config.GetVersion(), Now: now,
	})
	if err != nil {
		return "", err
	}
	return result.OutputPath, nil
}

func preRestoreExists(output string, encrypted bool) bool {
	if fileExists(output) {
		return true
	}
	return encrypted && fileExists(output+".enc")
}

func restoreHome(staging, target string, entries []Entry, force bool, warnings *[]string) (int, int, int, error) {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return 0, 0, 0, fmt.Errorf("create target %s: %w", target, err)
	}
	if err := os.Chmod(target, 0o700); err != nil {
		return 0, 0, 0, err
	}

	files, dirs, symlinks := 0, 0, 0
	for _, entry := range entries {
		rel, err := secureArchivePath(entry.Path)
		if err != nil {
			return 0, 0, 0, err
		}
		dst := filepath.Join(target, rel)
		switch entry.Type {
		case EntryDir:
			if err := safeDir(target, dst, force); err != nil {
				return 0, 0, 0, err
			}
			dirs++
		case EntryFile:
			if err := safeDir(target, filepath.Dir(dst), force); err != nil {
				return 0, 0, 0, err
			}
			if err := copyFileAtomic(
				filepath.Join(staging, ArchiveHomePrefix, rel),
				dst,
				os.FileMode(entry.Mode)&0o777,
				entry.ModTime,
				force,
			); err != nil {
				return 0, 0, 0, err
			}
			files++
		case EntrySymlink:
			// Created after regular files and directories.
		default:
			return 0, 0, 0, fmt.Errorf("unknown root entry type %q", entry.Type)
		}
	}
	for _, entry := range entries {
		if entry.Type != EntrySymlink {
			continue
		}
		rel, err := secureArchivePath(entry.Path)
		if err != nil {
			return 0, 0, 0, err
		}
		if err := safeLinkTarget(entry.Path, entry.LinkTarget); err != nil {
			return 0, 0, 0, err
		}
		dst := filepath.Join(target, rel)
		if err := makeSymlink(target, dst, entry.LinkTarget, force); err != nil {
			if runtime.GOOS == "windows" || os.IsPermission(err) {
				*warnings = append(*warnings, fmt.Sprintf("cannot create symlink %s: %v", dst, err))
				continue
			}
			return 0, 0, 0, err
		}
		symlinks++
	}
	for _, entry := range entries {
		if entry.Type != EntryDir {
			continue
		}
		rel, err := secureArchivePath(entry.Path)
		if err != nil {
			return 0, 0, 0, err
		}
		if err := applyMetadata(filepath.Join(target, rel), entry); err != nil {
			return 0, 0, 0, err
		}
	}
	return files, dirs, symlinks, nil
}

func preflightFiles(staging string, manifest *Manifest, configPath, sshKeyPath string, opts RestoreOptions) error {
	for _, file := range manifest.Files {
		var target string
		switch file.Kind {
		case FileKindConfig:
			target = configPath
		case FileKindSecurity:
			target = filepath.Join(filepath.Dir(configPath), config.SecurityConfigFile)
		case FileKindSSHKey:
			if opts.NoSSHKey {
				continue
			}
			target = sshKeyPath
		default:
			return fmt.Errorf("unknown archived file kind %q", file.Kind)
		}
		if target == "" {
			return fmt.Errorf("cannot restore %s: no target path", file.Kind)
		}
		info, err := os.Lstat(target)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", target)
		}
		if file.Kind != FileKindSSHKey || opts.Force {
			continue
		}
		rel, err := secureArchivePath(file.ArchivePath)
		if err != nil {
			return err
		}
		existing, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		incoming, readErr := os.ReadFile(filepath.Join(staging, rel))
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, incoming) {
			return fmt.Errorf("SSH key %s already exists with different content (use --force or --no-ssh-key)", target)
		}
	}
	return nil
}

func restoreFiles(
	staging string,
	manifest *Manifest,
	configPath, sshKeyPath string,
	opts RestoreOptions,
) (int, string, error) {
	restored := 0
	restoredSSH := ""
	for _, file := range manifest.Files {
		rel, err := secureArchivePath(file.ArchivePath)
		if err != nil {
			return 0, "", err
		}
		source := filepath.Join(staging, rel)
		switch file.Kind {
		case FileKindConfig:
			if err := copyFileAtomic(source, configPath, 0o600, file.Entry.ModTime, true); err != nil {
				return 0, "", err
			}
		case FileKindSecurity:
			target := filepath.Join(filepath.Dir(configPath), config.SecurityConfigFile)
			if err := copyFileAtomic(source, target, 0o600, file.Entry.ModTime, true); err != nil {
				return 0, "", err
			}
		case FileKindSSHKey:
			if opts.NoSSHKey {
				continue
			}
			if err := restoreSSHKey(source, sshKeyPath, opts.Force); err != nil {
				return 0, "", err
			}
			restoredSSH = sshKeyPath
		default:
			return 0, "", fmt.Errorf("unknown archived file kind %q", file.Kind)
		}
		restored++
	}
	return restored, restoredSSH, nil
}

func restoreSSHKey(source, target string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create SSH key directory: %w", err)
	}
	if existing, err := os.ReadFile(target); err == nil {
		incoming, readErr := os.ReadFile(source)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(existing, incoming) {
			return nil
		}
		if !force {
			return fmt.Errorf("SSH key %s already exists with different content (use --force or --no-ssh-key)", target)
		}
		backup := target + ".pre-restore-" + time.Now().Format("20060102-150405")
		for i := 1; fileExists(backup); i++ {
			backup = fmt.Sprintf("%s.pre-restore-%s-%d", target, time.Now().Format("20060102-150405"), i)
		}
		if renameErr := os.Rename(target, backup); renameErr != nil {
			return fmt.Errorf("backup existing SSH key: %w", renameErr)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return copyFileAtomic(source, target, 0o600, time.Time{}, true)
}

func rewriteConfigHome(configPath, oldHome, newHome string, warnings *[]string) {
	oldHome, newHome = filepath.Clean(oldHome), filepath.Clean(newHome)
	if configPath == "" || oldHome == newHome || oldHome == "." || newHome == "." {
		return
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			*warnings = append(*warnings, fmt.Sprintf("cannot read restored config %s: %v", configPath, err))
		}
		return
	}
	if !json.Valid(data) {
		*warnings = append(
			*warnings,
			fmt.Sprintf("restored config %s is not valid JSON; paths were not rewritten", configPath),
		)
		return
	}
	oldEscaped, err := json.Marshal(oldHome)
	if err != nil {
		return
	}
	newEscaped, err := json.Marshal(newHome)
	if err != nil {
		return
	}
	rewritten := replacePathPrefix(data, oldEscaped[1:len(oldEscaped)-1], newEscaped[1:len(newEscaped)-1])
	if bytes.Equal(rewritten, data) {
		return
	}
	if !json.Valid(rewritten) {
		*warnings = append(
			*warnings,
			fmt.Sprintf("config %s would become invalid; paths were not rewritten", configPath),
		)
		return
	}
	if err := fileutil.WriteFileAtomic(configPath, rewritten, 0o600); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("cannot rewrite paths in %s: %v", configPath, err))
	}
}

func replacePathPrefix(data, old, replacement []byte) []byte {
	if len(old) == 0 {
		return data
	}
	result := make([]byte, 0, len(data))
	for {
		index := bytes.Index(data, old)
		if index < 0 {
			return append(result, data...)
		}
		end := index + len(old)
		result = append(result, data[:index]...)
		if end == len(data) || data[end] == '"' || data[end] == '/' || data[end] == '\\' {
			result = append(result, replacement...)
		} else {
			result = append(result, old...)
		}
		data = data[end:]
	}
}

func hardenRestoredPermissions(home, configPath, sshKeyPath string, warnings *[]string) {
	for _, path := range []string{
		configPath,
		filepath.Join(filepath.Dir(configPath), config.SecurityConfigFile),
		filepath.Join(home, "auth.json"),
		filepath.Join(home, "launcher-auth.db"),
		sshKeyPath,
	} {
		hardenPath(path, 0o600, warnings)
	}
	for _, dir := range []string{
		filepath.Join(home, "auth"),
		filepath.Join(home, "auth", "mcp"),
		filepath.Join(home, "channels", "weixin", "context-tokens"),
		filepath.Join(home, "plugin-data"),
	} {
		hardenPath(dir, 0o700, warnings)
	}
}

func hardenPath(path string, mode os.FileMode, warnings *[]string) {
	if path == "" {
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			*warnings = append(*warnings, fmt.Sprintf("cannot inspect %s: %v", path, err))
		}
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return
	}
	if err := os.Chmod(path, mode); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("cannot secure %s: %v", path, err))
	}
}

func safeDir(root, dir string, force bool) error {
	if err := checkNoSymlinkParents(root, dir); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err == nil {
		if info.IsDir() {
			return nil
		}
		if !force {
			return fmt.Errorf("%s exists and is not a directory (use --force)", dir)
		}
		if removeErr := os.Remove(dir); removeErr != nil {
			return removeErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}

func checkNoSymlinkParents(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside %s", target, root)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink %s", current)
		}
	}
	return nil
}

func makeSymlink(root, target, linkTarget string, force bool) error {
	if err := checkNoSymlinkParents(root, filepath.Dir(target)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() || !force {
			return fmt.Errorf("%s already exists (use --force)", target)
		}
		if removeErr := os.Remove(target); removeErr != nil {
			return removeErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(linkTarget, target)
}

func copyFileAtomic(source, target string, mode os.FileMode, modTime time.Time, force bool) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory", target)
		}
		if !force {
			return fmt.Errorf("%s already exists (use --force)", target)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".picoclaw-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	input, err := os.Open(source)
	if err != nil {
		tmp.Close()
		return err
	}
	_, copyErr := io.Copy(tmp, input)
	input.Close()
	if copyErr != nil {
		tmp.Close()
		return copyErr
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode&0o777); err != nil {
		return err
	}
	if !modTime.IsZero() {
		_ = os.Chtimes(tmpName, modTime, modTime)
	}
	if force && runtime.GOOS == "windows" {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(tmpName, target)
}

func applyMetadata(target string, entry Entry) error {
	if entry.Mode != 0 {
		if err := os.Chmod(target, os.FileMode(entry.Mode)&0o777); err != nil {
			return err
		}
	}
	if !entry.ModTime.IsZero() {
		return os.Chtimes(target, entry.ModTime, entry.ModTime)
	}
	return nil
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("manifest exceeds %d bytes", limit)
	}
	return data, nil
}
