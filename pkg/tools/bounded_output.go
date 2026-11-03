package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"unicode/utf8"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	// maxOutputSpillBytes caps the artifact written for oversized foreground
	// output. Memory stays bounded by max_output_chars runes; the artifact
	// keeps the beginning of the stream up to this limit.
	maxOutputSpillBytes = 5_000_000
)

// boundedOutput is an io.Writer that retains a head/tail window in memory and
// spills the full stream to a file once the window is exceeded. It is safe for
// concurrent writers because stdout and stderr share one pipe.
//
// The window is measured in runes, matching the max_output_chars contract: the
// first 40% and the last 60% of the configured cap are kept. Each side is
// stored in a byte slice with room for utf8.UTFMax bytes per rune, plus one
// rune of slack for a trailing partial rune, so memory stays bounded by
// 4x max_output_chars bytes even for four-byte runes.
type boundedOutput struct {
	mu sync.Mutex

	maxRunes       int
	headRunes      int
	tailRunes      int
	headLimitBytes int
	tailLimitBytes int
	head           []byte
	tail           []byte
	totalBytes     int64
	totalRunes     int64
	runeCarry      []byte

	spillDir  string
	spill     *os.File
	spillPath string
	spillSize int64
	spillCap  int64
	capped    bool
	failed    bool
}

func newBoundedOutput(maxChars int, spillDir string) *boundedOutput {
	if maxChars <= 0 {
		maxChars = 1
	}
	headRunes := int(float64(maxChars) * toolshared.HeadTailHeadRatio)
	tailRunes := maxChars - headRunes
	return &boundedOutput{
		maxRunes:       maxChars,
		headRunes:      headRunes,
		tailRunes:      tailRunes,
		headLimitBytes: headRunes * utf8.UTFMax,
		tailLimitBytes: tailRunes*utf8.UTFMax + utf8.UTFMax,
		spillDir:       spillDir,
		spillCap:       maxOutputSpillBytes,
	}
}

// Write records p in the bounded window and mirrors it to the spill file when
// the window overflows. It never returns an error: a disk failure must not stop
// draining the child's pipe.
func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.countRunesLocked(p)
	if b.spill == nil && !b.failed && b.totalRunes > int64(b.maxRunes) {
		b.openSpillLocked()
	}
	b.totalBytes += int64(len(p))
	if err := b.writeSpillLocked(p); err != nil {
		b.abortSpillLocked()
	}
	b.appendLocked(p)
	return len(p), nil
}

func (b *boundedOutput) openSpillLocked() {
	if b.spill != nil || b.failed {
		return
	}

	file, dir, err := b.createSpillFile()
	if err != nil {
		b.failed = true
		return
	}
	b.spill = file
	b.spillDir = dir
	b.spillPath = file.Name()
	b.spillSize = 0

	// The first overflow happens while the stream was still within maxRunes,
	// so the head and tail windows reconstruct every byte written so far.
	if err := b.writeSpillLocked(b.snapshotLocked()); err != nil {
		b.abortSpillLocked()
	}
}

func (b *boundedOutput) createSpillFile() (*os.File, string, error) {
	if b.spillDir == "" {
		return b.createTempSpillFile()
	}
	if err := os.MkdirAll(b.spillDir, 0o700); err != nil {
		return b.createTempSpillFile()
	}
	file, err := os.CreateTemp(b.spillDir, "exec_*.log")
	if err != nil {
		return b.createTempSpillFile()
	}
	return file, b.spillDir, nil
}

func (b *boundedOutput) createTempSpillFile() (*os.File, string, error) {
	dir := tempArtifactDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	file, err := os.CreateTemp(dir, "exec_*.log")
	if err != nil {
		return nil, "", err
	}
	return file, dir, nil
}

// tempArtifactDir is the fallback for workspaces that cannot receive artifacts.
// Keeping spills in a dedicated directory lets Close prune them with the same
// retention policy as workspace artifacts instead of leaking files in the
// system temp directory.
func tempArtifactDir() string {
	return filepath.Join(os.TempDir(), "picoclaw-artifacts", "tool")
}

func (b *boundedOutput) writeSpillLocked(p []byte) error {
	if b.spill == nil || b.capped || len(p) == 0 {
		return nil
	}

	budget := b.spillCap - b.spillSize
	if budget <= 0 {
		b.capped = true
		return nil
	}
	if int64(len(p)) >= budget {
		if _, err := b.spill.Write(p[:budget]); err != nil {
			return err
		}
		b.spillSize += budget
		marker := fmt.Sprintf("\n... [spill capped at %s bytes] ...\n", formatCount64(b.spillCap))
		if _, err := b.spill.WriteString(marker); err != nil {
			return err
		}
		b.capped = true
		return nil
	}
	n, err := b.spill.Write(p)
	if err != nil {
		return err
	}
	b.spillSize += int64(n)
	return nil
}

// appendLocked keeps the first headLimitBytes bytes and, independently, the
// last tailLimitBytes bytes. The windows may overlap while the stream is short;
// snapshotLocked joins them without duplicating bytes.
func (b *boundedOutput) appendLocked(p []byte) {
	if remaining := b.headLimitBytes - len(b.head); remaining > 0 {
		take := len(p)
		if take > remaining {
			take = remaining
		}
		b.head = append(b.head, p[:take]...)
	}
	if b.tailLimitBytes == 0 {
		return
	}

	b.tail = append(b.tail, p...)
	if len(b.tail) > b.tailLimitBytes {
		b.tail = b.tail[len(b.tail)-b.tailLimitBytes:]
	}
}

// snapshotLocked reconstructs every byte written so far. It is valid while the
// stream fits in the combined head and tail windows, which is guaranteed before
// the first overflow: both windows together hold utf8.UTFMax*maxRunes plus one
// rune of slack, and a stream with at most maxRunes runes holds at most
// utf8.UTFMax bytes per rune plus a trailing partial rune.
func (b *boundedOutput) snapshotLocked() []byte {
	total := b.totalBytes
	if total <= int64(len(b.head)) {
		return b.head
	}
	if total <= int64(len(b.tail)) {
		return b.tail
	}

	overlap := int64(len(b.head)+len(b.tail)) - total
	if overlap < 0 {
		overlap = 0
	}
	if overlap > int64(len(b.tail)) {
		overlap = int64(len(b.tail))
	}
	out := make([]byte, 0, int(total))
	out = append(out, b.head...)
	out = append(out, b.tail[overlap:]...)
	return out
}

// Render returns the bounded view and whether the stream was truncated. The
// view keeps the same 40/60 head-tail split as other tool truncation sites.
func (b *boundedOutput) Render() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.totalRunes <= int64(b.maxRunes) {
		return string(b.snapshotLocked()), false
	}

	b.finishRuneCountLocked()
	head := toolshared.RunePrefix(string(trimPartialRuneSuffix(b.head)), b.headRunes)
	tail := toolshared.RuneSuffix(
		string(trimPartialRuneSuffix(trimPartialRunePrefix(b.tail))),
		b.tailRunes,
	)
	omitted := b.totalRunes - int64(utf8.RuneCountInString(head)) - int64(utf8.RuneCountInString(tail))
	if omitted < 0 {
		omitted = 0
	}
	notice := fmt.Sprintf(
		"\n\n... [OUTPUT TRUNCATED - %s chars omitted out of %s total] ...\n\n",
		formatCount64(omitted), formatCount64(b.totalRunes),
	)
	return head + notice + tail, true
}

// SpillPath returns the artifact path when the full stream overflowed the
// memory window and spilling succeeded.
func (b *boundedOutput) SpillPath() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spillPath
}

// SpillCapped reports whether the artifact reached maxOutputSpillBytes.
func (b *boundedOutput) SpillCapped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capped
}

func (b *boundedOutput) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.spill == nil {
		return nil
	}
	err := b.spill.Close()
	b.spill = nil
	if b.spillDir != "" {
		toolshared.PruneArtifacts(b.spillDir, toolshared.DefaultArtifactMaxAge, toolshared.DefaultArtifactMaxFiles)
	}
	return err
}

func (b *boundedOutput) abortSpillLocked() {
	if b.spill != nil {
		_ = b.spill.Close()
		b.spill = nil
	}
	if b.spillPath != "" {
		_ = os.Remove(b.spillPath)
		b.spillPath = ""
	}
	b.spillSize = 0
	b.failed = true
}

func trimPartialRuneSuffix(b []byte) []byte {
	for len(b) > 0 {
		r, size := utf8.DecodeLastRune(b)
		if r != utf8.RuneError || size > 1 {
			return b
		}
		b = b[:len(b)-1]
	}
	return b
}

func trimPartialRunePrefix(b []byte) []byte {
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r != utf8.RuneError || size > 1 {
			return b
		}
		b = b[1:]
	}
	return b
}

func (b *boundedOutput) countRunesLocked(p []byte) {
	if len(b.runeCarry) > 0 {
		p = append(b.runeCarry, p...)
		b.runeCarry = nil
	}
	for len(p) > 0 {
		r, size := utf8.DecodeRune(p)
		if r == utf8.RuneError && size == 1 && !utf8.FullRune(p) {
			b.runeCarry = append(b.runeCarry[:0], p...)
			return
		}
		b.totalRunes++
		p = p[size:]
	}
}

func (b *boundedOutput) finishRuneCountLocked() {
	if len(b.runeCarry) > 0 {
		b.totalRunes++
		b.runeCarry = nil
	}
}

func formatCount64(n int64) string {
	digits := strconv.FormatInt(n, 10)
	if len(digits) <= 3 {
		return digits
	}

	first := len(digits) % 3
	if first == 0 {
		first = 3
	}
	var out []byte
	out = append(out, digits[:first]...)
	for i := first; i < len(digits); i += 3 {
		out = append(out, ',')
		out = append(out, digits[i:i+3]...)
	}
	return string(out)
}
