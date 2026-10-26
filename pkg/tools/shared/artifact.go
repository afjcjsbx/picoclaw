package toolshared

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Artifact retention bounds. Artifact directories (for example
// <workspace>/.artifacts/mcp and <workspace>/.artifacts/tool) are pruned on
// write so a long-lived process cannot grow them without bound.
const (
	// DefaultArtifactMaxAge is the maximum age of an artifact file before it is
	// pruned.
	DefaultArtifactMaxAge = 24 * time.Hour
	// DefaultArtifactMaxFiles is the maximum number of files kept per artifact
	// directory once age-based pruning has run.
	DefaultArtifactMaxFiles = 128
)

const (
	// artifactPruneMinInterval bounds how often a directory is scanned. Spills
	// can happen several times per turn, and each scan reads every entry.
	artifactPruneMinInterval = time.Minute
	// artifactPruneMaxWrites forces a scan after this many unpruned writes, so
	// a burst cannot grow the directory far past DefaultArtifactMaxFiles.
	artifactPruneMaxWrites = 8
)

type artifactPruneState struct {
	mu     sync.Mutex
	last   time.Time
	writes int
}

var artifactPruneStates sync.Map // dir -> *artifactPruneState

// pruneArtifactsAfterWrite prunes dir at most once per interval, or after a
// burst of writes, instead of rescanning it on every single spill.
func pruneArtifactsAfterWrite(dir string) {
	value, _ := artifactPruneStates.LoadOrStore(dir, &artifactPruneState{})
	state := value.(*artifactPruneState)

	state.mu.Lock()
	if !state.last.IsZero() &&
		state.writes < artifactPruneMaxWrites &&
		time.Since(state.last) < artifactPruneMinInterval {
		state.writes++
		state.mu.Unlock()
		return
	}
	state.last = time.Now()
	state.writes = 0
	state.mu.Unlock()

	PruneArtifacts(dir, DefaultArtifactMaxAge, DefaultArtifactMaxFiles)
}

// PersistTextArtifact writes text to a new file in dir using the given
// os.CreateTemp pattern, then prunes the directory (throttled across rapid
// consecutive writes). It returns the created file path. The caller is
// responsible for choosing dir and pattern.
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

	pruneArtifactsAfterWrite(dir)
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
