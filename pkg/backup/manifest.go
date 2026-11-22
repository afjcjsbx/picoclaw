// Package backup creates and restores portable PicoClaw snapshots.
//
// A snapshot is a gzip tar with a manifest, the PICOCLAW_HOME tree, and a few
// files stored outside it (config, .security.yml, credential SSH key). It can be
// encrypted with a passphrase using AES-256-GCM and scrypt.
package backup

import "time"

const (
	// FormatName identifies the manifest format.
	FormatName = "picoclaw-backup"
	// FormatVersion is the archive layout version understood by this binary.
	FormatVersion = 2
	// ManifestName is the manifest entry inside the archive.
	ManifestName = "MANIFEST.json"

	// ArchiveHomePrefix stores the PICOCLAW_HOME tree.
	ArchiveHomePrefix = "home/"
	// ArchiveExtraPrefix stores single files outside PICOCLAW_HOME.
	ArchiveExtraPrefix = "extra/"

	// FileKindConfig is the main config file.
	FileKindConfig = "config"
	// FileKindSecurity is the .security.yml next to the config file.
	FileKindSecurity = "security"
	// FileKindSSHKey is the credential SSH key used by enc:// values.
	FileKindSSHKey = "ssh-key"

	// EntryFile is a regular file.
	EntryFile = "file"
	// EntryDir is a directory.
	EntryDir = "dir"
	// EntrySymlink is a relative symlink that stays inside the home.
	EntrySymlink = "symlink"

	maxSkippedEntries = 100
)

const (
	maxArchiveItems = 1_000_000
	maxArchiveBytes = int64(100) << 30
	maxManifestSize = 64 << 20
)

// Manifest describes one snapshot and is stored inside the archive.
type Manifest struct {
	Format          string    `json:"format"`
	FormatVersion   int       `json:"format_version"`
	CreatedAt       time.Time `json:"created_at"`
	PicoClawVersion string    `json:"picoclaw_version,omitempty"`
	ConfigVersion   int       `json:"config_version"`
	ConfigPath      string    `json:"config_path,omitempty"`
	Home            string    `json:"home"`
	Hostname        string    `json:"hostname,omitempty"`
	Platform        string    `json:"platform"`

	Encryption *EncryptionInfo `json:"encryption,omitempty"`
	Entries    []Entry         `json:"entries,omitempty"`
	Files      []ArchivedFile  `json:"files,omitempty"`

	Excluded     []ExcludedRule `json:"excluded,omitempty"`
	Warnings     []string       `json:"warnings,omitempty"`
	Skipped      []SkippedEntry `json:"skipped,omitempty"`
	SkippedCount int            `json:"skipped_count,omitempty"`

	FileCount    int   `json:"file_count"`
	DirCount     int   `json:"dir_count"`
	SymlinkCount int   `json:"symlink_count"`
	TotalBytes   int64 `json:"total_bytes"`
}

// Entry describes one directory entry relative to PICOCLAW_HOME.
type Entry struct {
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Mode       uint32    `json:"mode"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	SHA256     string    `json:"sha256,omitempty"`
	LinkTarget string    `json:"link_target,omitempty"`
}

// ArchivedFile is one file stored outside the home tree.
type ArchivedFile struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	ArchivePath string `json:"archive_path"`
	Entry       Entry  `json:"entry"`
}

// EncryptionInfo records the archive encryption parameters.
type EncryptionInfo struct {
	Algorithm string `json:"algorithm"`
	KDF       string `json:"kdf"`
	KDFLogN   uint32 `json:"kdf_log_n"`
	KDFR      uint32 `json:"kdf_r"`
	KDFP      uint32 `json:"kdf_p"`
}

// ExcludedRule documents a path class deliberately left out of the snapshot.
type ExcludedRule struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// SkippedEntry records a file that could not be archived safely.
type SkippedEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
