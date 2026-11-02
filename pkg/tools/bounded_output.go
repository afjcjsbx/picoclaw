package tools

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"unicode/utf8"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	// maxOutputSpillBytes caps the artifact written for oversized foreground
	// output. Memory stays bounded by maxOutputChars; the artifact keeps the
	// beginning of the stream up to this limit.
	maxOutputSpillBytes = 5_000_000
)

// boundedOutput is an io.Writer that retains a head/tail window in memory and
// spills the full stream to a file once the window is exceeded. It is safe for
// concurrent writers because stdout and stderr share one pipe.
type boundedOutput struct {
	mu sync.Mutex

	max        int
	headLimit  int
	tailLimit  int
	head       []byte
	tail       []byte
	total      int64
	totalRunes int64
	runeCarry  []byte

	spillDir  string
	spill     *os.File
	spillPath string
	spillSize int64
	spillCap  int64
	capped    bool
	failed    bool
	artifact  bool
}

func newBoundedOutput(max int, spillDir string) *boundedOutput {
	if max <= 0 {
		max = 1
	}
	headLimit := int(float64(max) * toolshared.HeadTailHeadRatio)
	tailLimit := max - headLimit
	return &boundedOutput{
		max:       max,
		headLimit: headLimit,
		tailLimit: tailLimit,
		spillDir:  spillDir,
		spillCap:  maxOutputSpillBytes,
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

	b.total += int64(len(p))
	b.countRunesLocked(p)
	if b.spill == nil && !b.failed && b.total > int64(b.max) {
		b.openSpillLocked()
	}
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

	file, artifact, err := b.createSpillFile()
	if err != nil {
		b.failed = true
		return
	}
	b.spill = file
	b.spillPath = file.Name()
	b.artifact = artifact
	b.spillSize = 0

	// The first overflow happens while total was still <= max, so head+tail
	// contains every byte written so far.
	if err := b.writeSpillLocked(b.head); err != nil {
		b.abortSpillLocked()
		return
	}
	if err := b.writeSpillLocked(b.tail); err != nil {
		b.abortSpillLocked()
	}
}

func (b *boundedOutput) createSpillFile() (*os.File, bool, error) {
	if b.spillDir != "" {
		if err := os.MkdirAll(b.spillDir, 0o700); err == nil {
			file, err := os.CreateTemp(b.spillDir, "exec_*.log")
			if err == nil {
				return file, true, nil
			}
		}
	}
	return b.createTempSpillFile()
}

func (b *boundedOutput) createTempSpillFile() (*os.File, bool, error) {
	file, err := os.CreateTemp("", "picoclaw-exec-*.log")
	if err != nil {
		return nil, false, err
	}
	return file, false, nil
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

func (b *boundedOutput) appendLocked(p []byte) {
	if len(b.head) < b.headLimit {
		take := b.headLimit - len(b.head)
		if take > len(p) {
			take = len(p)
		}
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 || b.tailLimit == 0 {
		return
	}

	if len(p) >= b.tailLimit {
		b.tail = append(b.tail[:0], p[len(p)-b.tailLimit:]...)
		return
	}
	b.tail = append(b.tail, p...)
	if len(b.tail) > b.tailLimit {
		copy(b.tail, b.tail[len(b.tail)-b.tailLimit:])
		b.tail = b.tail[:b.tailLimit]
	}
}

// Render returns the bounded view and whether the stream was truncated. The
// view keeps the same 40/60 head-tail split as other tool truncation sites.
func (b *boundedOutput) Render() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.total <= int64(b.max) {
		return string(b.head) + string(b.tail), false
	}

	b.finishRuneCountLocked()
	head := trimPartialRuneSuffix(b.head)
	tail := trimPartialRunePrefix(b.tail)
	omitted := b.totalRunes - int64(utf8.RuneCount(head)) - int64(utf8.RuneCount(tail))
	if omitted < 0 {
		omitted = 0
	}
	notice := fmt.Sprintf(
		"\n\n... [OUTPUT TRUNCATED - %s chars omitted out of %s total] ...\n\n",
		formatCount64(omitted), formatCount64(b.totalRunes),
	)
	return string(head) + notice + string(tail), true
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
	if b.artifact {
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
