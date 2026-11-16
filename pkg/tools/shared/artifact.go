package toolshared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/redact"
)

// Artifact retention bounds. Artifact directories (for example
// <workspace>/.artifacts/mcp and <workspace>/.artifacts/tool) are pruned on
// write so a long-lived process cannot grow them without bound.
const (
	// DefaultArtifactMaxAge is the default maximum age of an artifact file
	// before it is pruned. SetArtifactRetention overrides it.
	DefaultArtifactMaxAge = 24 * time.Hour
	// DefaultArtifactMaxFiles is the default maximum number of files kept per
	// artifact directory once age-based pruning has run. SetArtifactRetention
	// overrides it.
	DefaultArtifactMaxFiles = 128
)

const (
	// artifactPruneMinInterval bounds how often a directory is scanned. Spills
	// can happen several times per turn, and each scan reads every entry.
	artifactPruneMinInterval = time.Minute
	// artifactPruneMaxWrites forces a scan after this many unpruned writes, so
	// a burst cannot grow the directory far past the configured max-files
	// bound.
	artifactPruneMaxWrites = 8
)

type artifactPruneState struct {
	mu     sync.Mutex
	last   time.Time
	writes int
}

var artifactPruneStates sync.Map // dir -> *artifactPruneState

var (
	artifactRetentionMu sync.RWMutex
	artifactMaxAge      = DefaultArtifactMaxAge
	artifactMaxFiles    = DefaultArtifactMaxFiles
)

// SetArtifactRetention configures how long artifact files are kept and how
// many are kept per directory. A non-positive maxAge disables age-based
// pruning; a non-positive maxFiles disables the count bound. The defaults are
// DefaultArtifactMaxAge and DefaultArtifactMaxFiles.
func SetArtifactRetention(maxAge time.Duration, maxFiles int) {
	artifactRetentionMu.Lock()
	defer artifactRetentionMu.Unlock()
	artifactMaxAge = maxAge
	artifactMaxFiles = maxFiles
}

// ArtifactRetention returns the active artifact retention policy.
func ArtifactRetention() (maxAge time.Duration, maxFiles int) {
	artifactRetentionMu.RLock()
	defer artifactRetentionMu.RUnlock()
	return artifactMaxAge, artifactMaxFiles
}

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

	maxAge, maxFiles := ArtifactRetention()
	PruneArtifacts(dir, maxAge, maxFiles)
}

// PersistTextArtifact writes text to a new file in dir using the given
// os.CreateTemp pattern, then prunes the directory (throttled across rapid
// consecutive writes). It returns the created file path. The caller is
// responsible for choosing dir and pattern.
func PersistTextArtifact(dir, pattern, text string) (string, error) {
	return writeArtifactParts(dir, pattern, text)
}

// PersistTextArtifactWindow is PersistTextArtifact with an optional rune cap.
// When text is longer than maxRunes, it stores the first 40% and the last 60%
// of the budget around an "[LABEL TRUNCATED - N chars omitted out of T total]"
// notice and reports truncated=true. A non-positive maxRunes stores the whole
// text.
//
// The head and tail are written straight from the source string, so capping a
// multi-megabyte page does not allocate a second copy of the kept window.
func PersistTextArtifactWindow(dir, pattern, text string, maxRunes int, label string) (string, bool, error) {
	if maxRunes <= 0 {
		path, err := writeArtifactParts(dir, pattern, text)
		return path, false, err
	}

	total := utf8.RuneCountInString(text)
	if total <= maxRunes {
		path, err := writeArtifactParts(dir, pattern, text)
		return path, false, err
	}

	headBudget := int(float64(maxRunes) * HeadTailHeadRatio)
	tailBudget := maxRunes - headBudget
	headEnd := runePrefixBytes(text, headBudget)
	tailStart := runeSuffixStart(text, tailBudget)
	// Never leave an unmatchable fragment of a registered credential at a
	// truncation boundary: pull the head cut back to the start of a crossing
	// secret and push the tail cut past its end.
	headEnd = redact.SafeCut(text, headEnd)
	tailStart = redact.SafeStart(text, tailStart)
	if tailStart < headEnd {
		tailStart = headEnd
	}
	head, tail := text[:headEnd], text[tailStart:]
	omitted := total - utf8.RuneCountInString(head) - utf8.RuneCountInString(tail)
	if omitted < 0 {
		omitted = 0
	}
	notice := fmt.Sprintf(
		"\n\n... [%s TRUNCATED - %s chars omitted out of %s total] ...\n\n",
		label, formatCount(omitted), formatCount(total),
	)

	path, err := writeArtifactParts(dir, pattern, head, notice, tail)
	return path, true, err
}

// writeArtifactParts creates one artifact file and writes the parts in order.
// Writing a bounded window as separate parts keeps the kept content as views
// into the original string instead of a concatenated copy.
func writeArtifactParts(dir, pattern string, parts ...string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	tmpFile, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path := tmpFile.Name()
	// Stream through the redaction writer so configured credentials never reach
	// disk. Only the longest-registered-secret tail is buffered, never the
	// payload; SafeCut/SafeStart keep truncation cuts from splitting a secret.
	out := redact.Writer(tmpFile)
	for _, part := range parts {
		if _, err = io.WriteString(out, part); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(path)
			return "", err
		}
		// Parts are not contiguous source text (the notice separates head and
		// tail), so flush the carry buffer between them instead of letting a
		// partial credential from one part join the next.
		if flusher, ok := out.(interface{ Flush() error }); ok {
			if err = flusher.Flush(); err != nil {
				_ = tmpFile.Close()
				_ = os.Remove(path)
				return "", err
			}
		}
	}
	if err = out.Close(); err != nil {
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
