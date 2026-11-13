package tools

import (
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestANSIStripper(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"sgr", "\x1b[1;31mred\x1b[0m plain", "red plain"},
		{"cursor and erase", "\x1b[2K\rloading", "\rloading"},
		{"osc bel", "\x1b]0;window title\x07body", "body"},
		{"osc hyperlink", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"dcs", "\x1bP1;2|payload\x1b\\ok", "ok"},
		{"apc", "\x1b_payload\x1b\\ok", "ok"},
		{"charset designation", "\x1b(0abc", "abc"},
		{"two byte", "\x1b7save\x1b8restore", "saverestore"},
		{"utf8", "héllo → ok", "héllo → ok"},
		{"unterminated", "text\x1b[31", "text"},
		{"newline after stray esc", "a\x1b\nb", "a\nb"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s ansiStripper
			require.Equal(t, tt.want, string(s.Strip([]byte(tt.in))))
		})
	}
}

// TestANSIReaderHandlesSequencesSplitAcrossReads forces one-byte reads so every
// multi-byte sequence crosses a Read boundary.
func TestANSIReaderHandlesSequencesSplitAcrossReads(t *testing.T) {
	const in = "\x1b[1;31mred\x1b[0m " +
		"\x1b]0;title\x07plain " +
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\ done\n"

	r := &ansiReader{src: iotest.OneByteReader(strings.NewReader(in))}
	out, err := io.ReadAll(r)

	require.NoError(t, err)
	require.Equal(t, "red plain link done\n", string(out))
}

// TestANSIReaderSkipsChunksConsumedByEscapes guards against returning
// (0, nil), which io.Copy treats as no progress.
func TestANSIReaderSkipsChunksConsumedByEscapes(t *testing.T) {
	r := &ansiReader{src: strings.NewReader("\x1b[0m")}
	buf := make([]byte, 16)

	n, err := r.Read(buf)

	require.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)
}

func TestANSIStripperPlainTextIsZeroAlloc(t *testing.T) {
	var s ansiStripper
	p := []byte("plain output without escapes")

	allocs := testing.AllocsPerRun(1000, func() {
		_ = s.Strip(p)
	})

	require.Zero(t, allocs)
}

func TestANSIStripperBoundsMalformedSequences(t *testing.T) {
	var s ansiStripper
	body := strings.Repeat("x", maxANSIStringBytes+32)

	out := string(s.Strip([]byte("\x1b]" + body + "TAIL")))

	require.True(t, strings.HasSuffix(out, "TAIL"))
	require.Less(t, len(out), len(body)+len("TAIL"),
		"the unterminated OSC payload must not be emitted in full")
}

func TestANSIReaderFeedsCleanStreamToBoundedOutput(t *testing.T) {
	out := newBoundedOutput(10, t.TempDir())
	r := &ansiReader{src: strings.NewReader(strings.Repeat("\x1b[31mred\x1b[0m ", 20))}

	_, err := io.Copy(out, r)
	require.NoError(t, err)

	text, truncated := out.Render()
	require.True(t, truncated)
	require.NotContains(t, text, "\x1b")
	require.Contains(t, text, "red")

	spillPath := out.SpillPath()
	require.NotEmpty(t, spillPath)
	spilled, err := os.ReadFile(spillPath)
	require.NoError(t, err)
	require.NotContains(t, string(spilled), "\x1b")
	require.Contains(t, string(spilled), "red")
	require.NoError(t, out.Close())
}
