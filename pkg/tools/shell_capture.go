// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"bytes"

	"github.com/sipeed/picoclaw/pkg/config"
)

// headTailBuffer is an io.Writer that retains at most limit bytes of a
// stream: the first limit/2 bytes (head) and the last limit/2 bytes (tail).
// Bytes in between are dropped, so a runaway foreground command cannot
// exhaust process memory while its output is being captured.
//
// The retained window is intentionally large enough for the configured
// output truncation (see execCaptureLimit), so the head+tail truncation
// performed after the process exits sees everything it could have kept.
type headTailBuffer struct {
	head  bytes.Buffer
	tail  []byte
	limit int
	seen  int64
}

func newHeadTailBuffer(limit int) *headTailBuffer {
	return &headTailBuffer{limit: limit}
}

// Write implements io.Writer. It always reports the whole slice as written
// (and never returns an error); bytes that no longer fit the window are
// discarded inside the buffer.
func (b *headTailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.seen += int64(n)
	half := b.limit / 2

	if b.head.Len() < half {
		room := half - b.head.Len()
		if len(p) <= room {
			b.head.Write(p)
			return n, nil
		}
		b.head.Write(p[:room])
		p = p[room:]
	}

	if len(p) >= half {
		// The tail window lies entirely in this write.
		b.tail = append(b.tail[:0], p[len(p)-half:]...)
		return n, nil
	}
	b.tail = append(b.tail, p...)
	if len(b.tail) > half {
		b.tail = append(b.tail[:0], b.tail[len(b.tail)-half:]...)
	}
	return n, nil
}

// Bytes returns the retained stream: the whole stream while nothing was
// dropped, otherwise head + tail with the middle omitted.
func (b *headTailBuffer) Bytes() []byte {
	if len(b.tail) == 0 {
		return b.head.Bytes()
	}
	out := make([]byte, 0, b.head.Len()+len(b.tail))
	out = append(out, b.head.Bytes()...)
	out = append(out, b.tail...)
	return out
}

// Truncated reports whether bytes were dropped from the middle of the
// stream because the limit was exceeded.
func (b *headTailBuffer) Truncated() bool {
	return b.seen > int64(b.head.Len()+len(b.tail))
}

// execCaptureLimit returns the per-stream capture budget used while a
// foreground command runs. It is at least 1 MiB and at least twice
// maxOutputChars, so it always covers the head+tail window that the
// post-exit truncation may keep. The upper bound keeps absurdly large
// configured values from defeating the memory cap entirely.
func execCaptureLimit(maxOutputChars int) int {
	const (
		minLimit = 1 << 20 // 1 MiB
		maxLimit = 1 << 30 // 1 GiB
	)
	if maxOutputChars <= 0 {
		maxOutputChars = config.DefaultExecMaxOutputChars
	}
	if maxOutputChars > maxLimit/2 {
		return maxLimit
	}
	limit := maxOutputChars * 2
	if limit < minLimit {
		limit = minLimit
	}
	return limit
}
