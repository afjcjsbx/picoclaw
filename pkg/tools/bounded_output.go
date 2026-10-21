package tools

import (
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Foreground exec captures at most this much per stream before the head/tail
// window starts dropping the middle. The minimum is comfortably above the
// default output cap, so ordinary commands still spill their full text; the
// maximum keeps a misconfigured large cap from reintroducing unbounded memory.
const (
	minExecCaptureBytes = 1 << 20
	maxExecCaptureBytes = 4 << 20
)

// execOutputCaptureBytes returns the per-stream capture budget in bytes.
func execOutputCaptureBytes(maxOutputChars int) int {
	if maxOutputChars <= 0 {
		maxOutputChars = config.DefaultExecMaxOutputChars
	}
	capture := maxOutputChars * 2
	if capture < minExecCaptureBytes {
		capture = minExecCaptureBytes
	}
	if capture > maxExecCaptureBytes {
		capture = maxExecCaptureBytes
	}
	return capture
}

// boundedOutputBuffer keeps the first headMax and the last tailMax bytes
// written to it, counting everything in between as dropped. Foreground exec
// streams are unbounded, so a plain bytes.Buffer can grow to the size of the
// command output (tens of MB for a noisy command) even though the result is
// later truncated. The head/tail window keeps memory constant while preserving
// the parts a truncation notice is expected to show.
type boundedOutputBuffer struct {
	head    []byte
	tail    []byte
	headMax int
	tailMax int
	total   int64
}

func newBoundedOutputBuffer(headMax, tailMax int) *boundedOutputBuffer {
	if headMax < 0 {
		headMax = 0
	}
	if tailMax < 0 {
		tailMax = 0
	}
	return &boundedOutputBuffer{headMax: headMax, tailMax: tailMax}
}

func (b *boundedOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	b.total += int64(n)

	if len(b.head) < b.headMax {
		room := b.headMax - len(b.head)
		if room > n {
			room = n
		}
		b.head = append(b.head, p[:room]...)
		p = p[room:]
	}
	if len(p) == 0 {
		return n, nil
	}

	b.tail = append(b.tail, p...)
	if len(b.tail) > b.tailMax {
		drop := len(b.tail) - b.tailMax
		// Slide the window in place: the backing array stays bounded by
		// tailMax plus the writer's chunk size.
		copy(b.tail, b.tail[drop:])
		b.tail = b.tail[:b.tailMax]
	}
	return n, nil
}

// Dropped reports how many bytes were written outside the retained window.
func (b *boundedOutputBuffer) Dropped() int64 {
	return b.total - int64(len(b.head)) - int64(len(b.tail))
}

// Empty reports whether nothing was written.
func (b *boundedOutputBuffer) Empty() bool {
	return b.total == 0
}

func (b *boundedOutputBuffer) String() string {
	var sb strings.Builder
	sb.Grow(len(b.head) + len(b.tail) + 64)
	sb.Write(b.head)
	if dropped := b.Dropped(); dropped > 0 {
		sb.WriteString("\n...[")
		sb.WriteString(strconv.FormatInt(dropped, 10))
		sb.WriteString(" bytes omitted]...\n")
	}
	sb.Write(b.tail)
	return sb.String()
}
