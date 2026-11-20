package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/pid"
)

// CreateOptions controls a snapshot run.
type CreateOptions struct {
	Home         string
	ConfigPath   string
	SSHKeyPath   string
	OutputPath   string
	Passphrase   string
	Encrypt      bool
	AllowRunning bool
	Force        bool
	DryRun       bool
	Version      string
	Now          time.Time
}

// Result describes a completed or planned snapshot.
type Result struct {
	OutputPath string
	Encrypted  bool
	DryRun     bool
	Manifest   *Manifest
	Bytes      int64
	Duration   time.Duration
}

type backupPlan struct {
	manifest     Manifest
	entries      []plannedEntry
	files        []plannedFile
	skipped      []SkippedEntry
	skippedCount int
}

// Create writes a portable snapshot of a PicoClaw installation.
func Create(opts CreateOptions) (*Result, error) {
	start := opts.Now
	if start.IsZero() {
		start = time.Now()
	}

	home, err := absolutePath(opts.Home)
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	if homeErr := validateHomePath(home); homeErr != nil {
		return nil, homeErr
	}
	if info, statErr := os.Stat(home); statErr != nil {
		return nil, fmt.Errorf("home %s: %w", home, statErr)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("home %s is not a directory", home)
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

	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(".", fmt.Sprintf("picoclaw-backup-%s.tar.gz", start.Format("20060102-150405")))
	}
	if outputPath, err = absolutePath(outputPath); err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}
	if opts.Encrypt && !strings.HasSuffix(strings.ToLower(outputPath), ".enc") {
		outputPath += ".enc"
	}

	if !opts.AllowRunning && gatewayRunning(home) {
		return nil, fmt.Errorf(
			"the PicoClaw gateway is running for %s; stop it before backing up (or pass --allow-running)",
			home,
		)
	}
	if opts.Encrypt && !opts.DryRun {
		if strings.TrimSpace(opts.Passphrase) == "" {
			return nil, ErrPassphraseRequired
		}
		if len(opts.Passphrase) < 8 {
			return nil, fmt.Errorf("backup passphrase must be at least 8 characters")
		}
	}

	if info, statErr := os.Stat(outputPath); statErr == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("output %s is a directory", outputPath)
		}
		if !opts.Force {
			return nil, fmt.Errorf("output %s already exists (use --force to overwrite)", outputPath)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("stat output %s: %w", outputPath, statErr)
	}

	skip := map[string]bool{outputPath: true}
	if realOutput, evalErr := filepath.EvalSymlinks(outputPath); evalErr == nil {
		skip[realOutput] = true
	}

	plan, err := planBackup(home, configPath, sshKeyPath, skip, opts, start)
	if err != nil {
		return nil, err
	}

	result := &Result{
		OutputPath: outputPath,
		Encrypted:  opts.Encrypt,
		DryRun:     opts.DryRun,
		Manifest:   &plan.manifest,
	}
	if opts.DryRun {
		fillManifestFromPlan(&plan.manifest, plan)
		result.Bytes = plan.manifest.TotalBytes
		result.Duration = time.Since(start)
		return result, nil
	}
	if err := writeSnapshot(outputPath, plan, opts); err != nil {
		return nil, err
	}
	if info, err := os.Stat(outputPath); err == nil {
		result.Bytes = info.Size()
	}
	result.Duration = time.Since(start)
	return result, nil
}

func planBackup(
	home, configPath, sshKeyPath string,
	skip map[string]bool,
	opts CreateOptions,
	start time.Time,
) (*backupPlan, error) {
	snap, warnings := readConfigSnapshot(configPath)
	manifest := Manifest{
		Format:        FormatName,
		FormatVersion: FormatVersion,
		CreatedAt:     start.UTC(),
		ConfigVersion: snap.Version,
		ConfigPath:    configPath,
		Home:          home,
		Platform:      runtime.GOOS + "/" + runtime.GOARCH,
		Excluded:      manifestExclusions(),
		Warnings:      warnings,
	}
	manifest.PicoClawVersion = opts.Version
	if manifest.PicoClawVersion == "" {
		manifest.PicoClawVersion = config.GetVersion()
	}
	if hostname, err := os.Hostname(); err == nil {
		manifest.Hostname = hostname
	}
	if opts.Encrypt {
		manifest.Encryption = currentEncryptionInfo()
	}

	entries, skipped, skippedCount, err := walkHome(home, skip)
	if err != nil {
		return nil, fmt.Errorf("plan home: %w", err)
	}
	plan := &backupPlan{
		manifest:     manifest,
		entries:      entries,
		skipped:      skipped,
		skippedCount: skippedCount,
	}

	if needsFileExtra(home, configPath) {
		file, err := planFile(FileKindConfig, configPath, ArchiveExtraPrefix+"config/"+filepath.Base(configPath))
		if err == nil {
			plan.files = append(plan.files, *file)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("plan config: %w", err)
		}
	}
	securityPath := filepath.Join(filepath.Dir(configPath), config.SecurityConfigFile)
	if needsFileExtra(home, configPath) || needsFileExtra(home, securityPath) {
		file, err := planFile(FileKindSecurity, securityPath, ArchiveExtraPrefix+"config/"+config.SecurityConfigFile)
		if err == nil {
			plan.files = append(plan.files, *file)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("plan security file: %w", err)
		}
	}
	if sshKeyPath != "" {
		if _, err := os.Stat(sshKeyPath); err != nil {
			if os.IsNotExist(err) && snap.HasEncryptedCreds {
				plan.manifest.Warnings = append(plan.manifest.Warnings,
					fmt.Sprintf("config uses enc:// credentials but the SSH key %s was not found", sshKeyPath))
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("plan SSH key: %w", err)
			}
		} else if needsFileExtra(home, sshKeyPath) {
			file, err := planFile(FileKindSSHKey, sshKeyPath, ArchiveExtraPrefix+"ssh/"+filepath.Base(sshKeyPath))
			if err != nil {
				return nil, fmt.Errorf("plan SSH key: %w", err)
			}
			plan.files = append(plan.files, *file)
		}
	}
	if snap.HasEncryptedCreds {
		plan.manifest.Warnings = append(plan.manifest.Warnings,
			"config contains enc:// credentials; keep PICOCLAW_KEY_PASSPHRASE and the restored SSH key")
	}
	if data, err := os.ReadFile(configPath); err == nil && strings.Contains(string(data), "file://") {
		plan.manifest.Warnings = append(plan.manifest.Warnings,
			"config uses file:// credentials; referenced files are not resolved automatically")
	}

	items, totalBytes := planTotals(plan)
	if items+1 > maxArchiveItems {
		return nil, fmt.Errorf("snapshot would exceed the %d-entry restore limit", maxArchiveItems)
	}
	if totalBytes+maxManifestSize > maxArchiveBytes {
		return nil, fmt.Errorf("snapshot would exceed the %d-byte restore limit", maxArchiveBytes)
	}
	return plan, nil
}

func planTotals(plan *backupPlan) (int, int64) {
	items := len(plan.entries) + len(plan.files)
	totalBytes := int64(0)
	for _, entry := range plan.entries {
		if entry.entry.Type == EntryFile {
			totalBytes += entry.entry.Size
		}
	}
	for _, file := range plan.files {
		totalBytes += file.file.Entry.Size
	}
	return items, totalBytes
}

func fillManifestFromPlan(manifest *Manifest, plan *backupPlan) {
	manifest.Entries = make([]Entry, 0, len(plan.entries))
	for _, item := range plan.entries {
		entry := item.entry
		switch entry.Type {
		case EntryFile:
			manifest.FileCount++
			manifest.TotalBytes += entry.Size
		case EntryDir:
			manifest.DirCount++
		case EntrySymlink:
			manifest.SymlinkCount++
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	manifest.Files = make([]ArchivedFile, 0, len(plan.files))
	for _, file := range plan.files {
		manifest.Files = append(manifest.Files, file.file)
		manifest.FileCount++
		manifest.TotalBytes += file.file.Entry.Size
	}
	manifest.Skipped = plan.skipped
	manifest.SkippedCount = plan.skippedCount
}

func manifestExclusions() []ExcludedRule {
	rules := append([]ExcludedRule(nil), excludedTopLevelDirs...)
	rules = append(
		rules,
		ExcludedRule{Path: "**/*.log", Reason: "log file"},
		ExcludedRule{Path: "**/.DS_Store, **/._*, **/Thumbs.db, **/desktop.ini", Reason: "OS metadata"},
		ExcludedRule{
			Path:   "**/*.tmp, **/.tmp-*, **/*.pid, **/*.sock, **/*.swp, **/*~",
			Reason: "temporary or runtime file",
		},
		ExcludedRule{
			Path:   "**/node_modules/, **/__pycache__/, **/.venv/, **/venv/",
			Reason: "reinstallable dependencies and tool caches",
		},
	)
	return rules
}

func writeSnapshot(outputPath string, plan *backupPlan, opts CreateOptions) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	tmpPath := fmt.Sprintf("%s.tmp-%d-%d", outputPath, os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	defer func() {
		if retErr != nil {
			file.Close()
			os.Remove(tmpPath)
		}
	}()

	var writer io.Writer = file
	var encryptor *encryptWriter
	if opts.Encrypt {
		var encInfo *EncryptionInfo
		encryptor, encInfo, err = newEncryptWriter(file, opts.Passphrase)
		if err != nil {
			return err
		}
		plan.manifest.Encryption = encInfo
		writer = encryptor
	}

	gz := gzip.NewWriter(writer)
	tw := tar.NewWriter(gz)
	closeAll := func() {
		_ = tw.Close()
		_ = gz.Close()
		if encryptor != nil {
			_ = encryptor.Close()
		}
	}
	if writeErr := writePlan(tw, plan); writeErr != nil {
		closeAll()
		return writeErr
	}

	manifestData, err := json.MarshalIndent(&plan.manifest, "", "  ")
	if err != nil {
		closeAll()
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := writeTarEntry(
		tw,
		ManifestName,
		tar.TypeReg,
		0o600,
		plan.manifest.CreatedAt,
		"",
		manifestData,
	); err != nil {
		closeAll()
		return err
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		if encryptor != nil {
			encryptor.Close()
		}
		return fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		if encryptor != nil {
			encryptor.Close()
		}
		return fmt.Errorf("close gzip: %w", err)
	}
	if encryptor != nil {
		if err := encryptor.Close(); err != nil {
			return fmt.Errorf("close encryption: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("secure output: %w", err)
	}
	if opts.Force && runtime.GOOS == "windows" {
		if err := os.Remove(outputPath); err != nil && !os.IsNotExist(err) {
			os.Remove(tmpPath)
			return fmt.Errorf("replace existing output: %w", err)
		}
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("commit output: %w", err)
	}
	return nil
}

func writePlan(tw *tar.Writer, plan *backupPlan) error {
	plan.manifest.Entries = make([]Entry, 0, len(plan.entries))
	for _, item := range plan.entries {
		entry := item.entry
		name := ArchiveHomePrefix + entry.Path
		if entry.Type == EntryFile {
			hash, size, err := writeTarFile(tw, name, item.abs, &entry)
			if err != nil {
				return err
			}
			entry.Size = size
			entry.SHA256 = hash
			plan.manifest.FileCount++
			plan.manifest.TotalBytes += size
		} else if err := writeTarEntry(tw, name, tarType(entry.Type), int64(entry.Mode)&0o777, entry.ModTime, entry.LinkTarget, nil); err != nil {
			return err
		} else if entry.Type == EntryDir {
			plan.manifest.DirCount++
		} else {
			plan.manifest.SymlinkCount++
		}
		plan.manifest.Entries = append(plan.manifest.Entries, entry)
	}

	plan.manifest.Files = make([]ArchivedFile, 0, len(plan.files))
	for _, planned := range plan.files {
		file := planned.file
		hash, size, err := writeTarFile(tw, file.ArchivePath, planned.abs, &file.Entry)
		if err != nil {
			return err
		}
		file.Entry.Size = size
		file.Entry.SHA256 = hash
		plan.manifest.Files = append(plan.manifest.Files, file)
		plan.manifest.FileCount++
		plan.manifest.TotalBytes += size
	}
	plan.manifest.Skipped = plan.skipped
	plan.manifest.SkippedCount = plan.skippedCount
	return nil
}

func tarType(kind string) byte {
	switch kind {
	case EntryDir:
		return tar.TypeDir
	case EntrySymlink:
		return tar.TypeSymlink
	default:
		return tar.TypeReg
	}
}

func writeTarEntry(
	tw *tar.Writer,
	name string,
	kind byte,
	mode int64,
	modTime time.Time,
	link string,
	data []byte,
) error {
	header := &tar.Header{
		Name: name, Mode: mode, ModTime: modTime, Typeflag: kind, Format: tar.FormatPAX,
	}
	if kind == tar.TypeDir {
		header.Name = strings.TrimSuffix(name, "/") + "/"
	}
	if kind == tar.TypeSymlink {
		header.Linkname = link
	}
	if data != nil {
		header.Size = int64(len(data))
	}
	if headerErr := tw.WriteHeader(header); headerErr != nil {
		return fmt.Errorf("write %s header: %w", name, headerErr)
	}
	if data != nil {
		if _, err := tw.Write(data); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func writeTarFile(tw *tar.Writer, name, source string, entry *Entry) (string, int64, error) {
	file, err := os.Open(source)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", source, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("stat %s: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s changed type during backup", source)
	}
	header := &tar.Header{
		Name: name, Mode: int64(entry.Mode) & 0o777, Size: info.Size(),
		ModTime: info.ModTime(), Typeflag: tar.TypeReg, Format: tar.FormatPAX,
	}
	if headerErr := tw.WriteHeader(header); headerErr != nil {
		return "", 0, fmt.Errorf("write %s header: %w", name, headerErr)
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(tw, hasher), file)
	if err != nil {
		return "", 0, fmt.Errorf("write %s: %w", name, err)
	}
	if written != info.Size() {
		return "", 0, fmt.Errorf("%s changed size during backup", source)
	}
	entry.ModTime = info.ModTime()
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func gatewayRunning(home string) bool {
	return pid.ReadPidFileWithCheck(home) != nil
}

func absolutePath(value string) (string, error) {
	value = fileutil.ExpandHome(value)
	if value == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func validateHomePath(home string) error {
	if home == "" || filepath.Dir(home) == home {
		return fmt.Errorf("refusing to use %q as a PicoClaw home", home)
	}
	if userHome, err := os.UserHomeDir(); err == nil && filepath.Clean(userHome) == home {
		return fmt.Errorf("refusing to use the user home directory %q as a PicoClaw home", home)
	}
	return nil
}
