package tools

import (
	"fmt"
	"strconv"
	"sync"
	"unicode/utf8"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
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

	spill spillFile
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
		spill:          spillFile{dir: spillDir, pattern: "exec_*.log"},
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
	if !b.spill.active() && b.totalRunes > int64(b.maxRunes) {
		// The first overflow happens while the stream was still within
		// maxRunes, so the head and tail windows reconstruct every byte
		// written so far.
		b.spill.start(b.snapshotLocked())
	}
	b.totalBytes += int64(len(p))
	b.spill.write(p)
	b.appendLocked(p)
	return len(p), nil
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

// boundedOutputView is the inline view of a stream plus the byte accounting
// the model-facing exec header reports.
type boundedOutputView struct {
	Text         string
	Truncated    bool
	TotalBytes   int64
	OmittedBytes int64
}

// Render returns the bounded view. The view keeps the same 40/60 head-tail
// split as other tool truncation sites. TotalBytes counts every byte the child
// wrote, while OmittedBytes counts the stream bytes outside the rendered
// head/tail windows, so the caller can report both before the body without
// re-reading the collector.
func (b *boundedOutput) Render() boundedOutputView {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.totalRunes <= int64(b.maxRunes) {
		return boundedOutputView{
			Text:       string(b.snapshotLocked()),
			TotalBytes: b.totalBytes,
		}
	}

	b.finishRuneCountLocked()
	head := toolshared.RunePrefix(string(trimPartialRuneSuffix(b.head)), b.headRunes)
	tail := toolshared.RuneSuffix(
		string(trimPartialRuneSuffix(trimPartialRunePrefix(b.tail))),
		b.tailRunes,
	)
	omittedRunes := b.totalRunes - int64(utf8.RuneCountInString(head)) - int64(utf8.RuneCountInString(tail))
	if omittedRunes < 0 {
		omittedRunes = 0
	}
	omittedBytes := b.totalBytes - int64(len(head)) - int64(len(tail))
	if omittedBytes < 0 {
		omittedBytes = 0
	}
	notice := fmt.Sprintf(
		"\n\n... [OUTPUT TRUNCATED - %s chars omitted out of %s total] ...\n\n",
		formatCount64(omittedRunes), formatCount64(b.totalRunes),
	)
	return boundedOutputView{
		Text:         head + notice + tail,
		Truncated:    true,
		TotalBytes:   b.totalBytes,
		OmittedBytes: omittedBytes,
	}
}

// SpillPath returns the artifact path when the full stream overflowed the
// memory window and spilling succeeded.
func (b *boundedOutput) SpillPath() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spill.path
}

// SpillCapped reports whether the artifact reached maxOutputSpillBytes.
func (b *boundedOutput) SpillCapped() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spill.capped
}

func (b *boundedOutput) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.spill.active() {
		return nil
	}
	err := b.spill.close()
	if b.spill.dir != "" {
		maxAge, maxFiles := toolshared.ArtifactRetention()
		toolshared.PruneArtifacts(b.spill.dir, maxAge, maxFiles)
	}
	return err
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
