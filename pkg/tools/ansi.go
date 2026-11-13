package tools

import (
	"bytes"
	"io"
)

// ansiESC introduces every ANSI escape sequence.
const ansiESC = 0x1b

// The stripper only keeps a small state machine, never the sequence body, so
// memory stays O(1) even for malformed output. The caps stop a stray ESC from
// swallowing an unbounded run of ordinary text: once a sequence grows past its
// cap it is abandoned and the following bytes are treated as text again.
const (
	maxANSIStringBytes = 64 << 10 // OSC/DCS/SOS/PM/APC payloads
	maxANSIParamsBytes = 256      // CSI parameters and intermediates
)

type ansiState uint8

const (
	ansiGround ansiState = iota
	ansiEscape
	ansiCSI
	ansiString
	ansiStringEscape
	ansiEscapeIntermediate
)

// ansiStripper removes ANSI escape sequences (CSI, OSC, DCS, SOS, PM, APC and
// other ESC sequences) from a byte stream. State survives calls, so sequences
// split across writes or reads are removed too.
//
// Strip compacts the input slice in place. Callers must not reuse the removed
// prefix of p, but keep ownership of the returned slice.
type ansiStripper struct {
	state  ansiState
	seqLen int
}

// Strip removes ANSI escape sequences from p in place and returns the
// remaining bytes. The result aliases p. It is allocation-free.
func (s *ansiStripper) Strip(p []byte) []byte {
	if len(p) == 0 {
		return p
	}
	// Fast path: no ESC and no sequence in progress means the whole chunk is
	// plain text, which is the common case for shell output.
	if s.state == ansiGround && bytes.IndexByte(p, ansiESC) < 0 {
		return p
	}

	out := 0
	for i := 0; i < len(p); {
		if s.state == ansiGround {
			if p[i] != ansiESC {
				next := bytes.IndexByte(p[i:], ansiESC)
				if next < 0 {
					out += copy(p[out:], p[i:])
					break
				}
				next += i
				out += copy(p[out:], p[i:next])
				i = next
			}
			s.state = ansiEscape
			s.seqLen = 0
			i++
			continue
		}

		c := p[i]
		i++
		switch s.state {
		case ansiEscape:
			switch {
			case c == '[':
				s.state = ansiCSI
			case c == ']' || c == 'P' || c == 'X' || c == '^' || c == '_':
				s.state = ansiString
			case c >= 0x20 && c <= 0x2F:
				s.state = ansiEscapeIntermediate
				s.seqLen = 1
			case c >= 0x30 && c <= 0x7E:
				// Two-byte ESC sequence such as ESC 7 / ESC =.
				s.state = ansiGround
			case c == ansiESC:
				// A new ESC restarts the introducer.
			default:
				// Not a valid sequence: keep the following text (notably a
				// newline) instead of swallowing the rest of the stream.
				s.state = ansiGround
				if ansiKeepByte(c) {
					p[out] = c
					out++
				}
			}
		case ansiCSI:
			switch {
			case c >= 0x40 && c <= 0x7E:
				s.state = ansiGround
			case c == ansiESC:
				s.state = ansiEscape
			case c >= 0x20 && c <= 0x3F:
				s.seqLen++
				if s.seqLen > maxANSIParamsBytes {
					s.state = ansiGround
				}
			default:
				s.state = ansiGround
				if ansiKeepByte(c) {
					p[out] = c
					out++
				}
			}
		case ansiString:
			switch c {
			case 0x07: // BEL terminates an OSC string.
				s.state = ansiGround
			case ansiESC:
				s.state = ansiStringEscape
			default:
				s.seqLen++
				if s.seqLen > maxANSIStringBytes {
					s.state = ansiGround
				}
			}
		case ansiStringEscape:
			switch c {
			case '\\', 0x07: // ST or BEL terminates the string.
				s.state = ansiGround
			case ansiESC:
				// Keep waiting for the ST final byte.
			default:
				s.state = ansiString
				s.seqLen++
				if s.seqLen > maxANSIStringBytes {
					s.state = ansiGround
				}
			}
		case ansiEscapeIntermediate:
			switch {
			case c >= 0x30 && c <= 0x7E:
				s.state = ansiGround
			case c >= 0x20 && c <= 0x2F:
				s.seqLen++
				if s.seqLen > maxANSIParamsBytes {
					s.state = ansiGround
				}
			case c == ansiESC:
				s.state = ansiEscape
			default:
				s.state = ansiGround
				if ansiKeepByte(c) {
					p[out] = c
					out++
				}
			}
		}
	}
	return p[:out]
}

// ansiKeepByte reports whether a byte that ended a malformed escape sequence
// should be preserved. Printable bytes, tabs and line breaks survive; other C0
// controls and DEL are part of the noise being removed.
func ansiKeepByte(c byte) bool {
	return c == '\n' || c == '\r' || c == '\t' || (c >= 0x20 && c != 0x7f)
}

// ansiReader strips ANSI sequences from src while copying. Strip works in
// place in the caller's buffer, so wrapping a reader adds no per-chunk
// allocation and no extra buffering on top of io.Copy's buffer.
type ansiReader struct {
	src   io.Reader
	strip ansiStripper
}

func (r *ansiReader) Read(p []byte) (int, error) {
	for {
		n, err := r.src.Read(p)
		if n > 0 {
			n = len(r.strip.Strip(p[:n]))
			if n > 0 || err != nil {
				return n, err
			}
			// The whole chunk was an escape sequence: read again instead of
			// returning (0, nil), which callers treat as no progress.
			continue
		}
		if err != nil {
			return 0, err
		}
	}
}
