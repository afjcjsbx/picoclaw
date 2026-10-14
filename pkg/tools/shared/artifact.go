package toolshared

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Artifact retention bounds. Artifact directories (for example
// <workspace>/.artifacts/mcp and <workspace>/.artifacts/tool) are pruned on
// every write so a long-lived process cannot grow them without bound.
const (
	// DefaultArtifactMaxAge is the maximum age of an artifact file before it is
	// pruned.
	DefaultArtifactMaxAge = 24 * time.Hour
	// DefaultArtifactMaxFiles is the maximum number of files kept per artifact
	// directory once age-based pruning has run.
	DefaultArtifactMaxFiles = 128
)

// PersistTextArtifact writes text to a new file in dir using the given
// os.CreateTemp pattern, then prunes the directory. It returns the created file
// path. The caller is responsible for choosing dir and pattern.
func PersistTextArtifact(dir, pattern, text string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := tmpFile.Name()
	if _, err = tmpFile.WriteString(text); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err = tmpFile.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}

	PruneArtifacts(dir, DefaultArtifactMaxAge, DefaultArtifactMaxFiles)
	return path, nil
}

// PruneArtifacts bounds an artifact directory: it removes files older than
// maxAge and, if the directory still exceeds maxFiles, the oldest ones.
// Cleanup is best-effort and never fails the tool call.
func PruneArtifacts(dir string, maxAge time.Duration, maxFiles int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	type artifact struct {
		path    string
		modTime time.Time
	}
	files := make([]artifact, 0, len(entries))
	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if maxAge > 0 && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
			continue
		}
		files = append(files, artifact{path: path, modTime: info.ModTime()})
	}

	if maxFiles <= 0 || len(files) <= maxFiles {
		return
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})
	for _, file := range files[:len(files)-maxFiles] {
		_ = os.Remove(file.path)
	}
}
