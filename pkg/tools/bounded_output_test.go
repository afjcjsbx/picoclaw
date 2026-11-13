package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/redact"
)

func TestBoundedOutputSpillsFullStream(t *testing.T) {
	out := newBoundedOutput(20, t.TempDir())
	data := strings.Repeat("a", 10) + strings.Repeat("b", 100) + strings.Repeat("c", 10)

	n, err := out.Write([]byte(data))
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	text, truncated := out.Render()
	require.True(t, truncated)
	require.Contains(t, text, "[OUTPUT TRUNCATED")
	require.True(t, strings.HasPrefix(text, strings.Repeat("a", 8)))
	require.True(t, strings.HasSuffix(text, strings.Repeat("c", 10)))

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.NoError(t, out.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, string(got))
}

// TestBoundedOutputSpillsAcrossWritesInOrder overflows after several writes so
// the spill replay exercises the head/tail reconstruction on a live window.
func TestBoundedOutputSpillsAcrossWritesInOrder(t *testing.T) {
	out := newBoundedOutput(10, t.TempDir())
	chunks := []string{"abcd", "èèè", "efghij"}

	var data strings.Builder
	for _, chunk := range chunks {
		data.WriteString(chunk)
		_, err := out.Write([]byte(chunk))
		require.NoError(t, err)
	}

	_, truncated := out.Render()
	require.True(t, truncated)

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.NoError(t, out.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data.String(), string(got))
}

// TestBoundedOutputSpillReplaysOverlappingWindows covers a first overflow where
// the head and tail byte windows overlap and both cut through a multi-byte rune.
func TestBoundedOutputSpillReplaysOverlappingWindows(t *testing.T) {
	out := newBoundedOutput(10, t.TempDir())
	prior := strings.Repeat("€", 10) // 10 runes, 30 bytes

	_, err := out.Write([]byte(prior))
	require.NoError(t, err)
	_, err = out.Write([]byte("€"))
	require.NoError(t, err)

	_, truncated := out.Render()
	require.True(t, truncated)

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.NoError(t, out.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, prior+"€", string(got))
}

// TestBoundedOutputSpillPreservesTrailingPartialRune overflows after a write
// that ended mid-rune, so the spill replay must include the carried bytes.
func TestBoundedOutputSpillPreservesTrailingPartialRune(t *testing.T) {
	out := newBoundedOutput(1, t.TempDir())
	emoji := []byte("😀")

	_, err := out.Write(emoji)
	require.NoError(t, err)
	_, err = out.Write(emoji[:3])
	require.NoError(t, err)
	_, err = out.Write(emoji[3:])
	require.NoError(t, err)

	_, truncated := out.Render()
	require.True(t, truncated)

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.NoError(t, out.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(emoji)+string(emoji), string(got))
}

func TestBoundedOutputDoesNotSpillWithinLimit(t *testing.T) {
	out := newBoundedOutput(100, t.TempDir())

	n, err := out.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)

	text, truncated := out.Render()
	require.False(t, truncated)
	require.Equal(t, "hello", text)
	require.Empty(t, out.SpillPath())
	require.NoError(t, out.Close())
}

// TestBoundedOutputCountsRunesNotBytes keeps the max_output_chars contract:
// 1000 two-byte runes fit a 1500-character cap even though they are 2000 bytes.
func TestBoundedOutputCountsRunesNotBytes(t *testing.T) {
	out := newBoundedOutput(1500, t.TempDir())
	data := strings.Repeat("è", 1000)

	n, err := out.Write([]byte(data))
	require.NoError(t, err)
	require.Equal(t, len(data), n)

	text, truncated := out.Render()
	require.False(t, truncated)
	require.Equal(t, data, text)
	require.Empty(t, out.SpillPath())
	require.NoError(t, out.Close())
}

// TestBoundedOutputKeepsFourByteRunesWithinCap covers the worst case for the
// byte-window sizing: 100 four-byte runes exactly fit a 100-character cap.
func TestBoundedOutputKeepsFourByteRunesWithinCap(t *testing.T) {
	out := newBoundedOutput(100, t.TempDir())
	data := strings.Repeat("😀", 100)

	_, err := out.Write([]byte(data))
	require.NoError(t, err)

	text, truncated := out.Render()
	require.False(t, truncated)
	require.Equal(t, data, text)
	require.NoError(t, out.Close())
}

// TestBoundedOutputTruncatesOnRuneBoundary writes one byte at a time so the
// UTF-8 carry path is exercised across write boundaries.
func TestBoundedOutputTruncatesOnRuneBoundary(t *testing.T) {
	out := newBoundedOutput(10, t.TempDir())
	data := strings.Repeat("è", 100)

	for i := 0; i < len(data); i++ {
		_, err := out.Write([]byte{data[i]})
		require.NoError(t, err)
	}

	text, truncated := out.Render()
	require.True(t, truncated)
	require.True(t, utf8.ValidString(text))
	require.True(t, strings.HasPrefix(text, strings.Repeat("è", 4)))
	require.True(t, strings.HasSuffix(text, strings.Repeat("è", 6)))
	require.Contains(t, text, "chars omitted")
	require.NoError(t, out.Close())
}

// TestBoundedOutputSpillRedactsRegisteredSecrets covers the disk side of the
// pipeline filter: the stream that is mirrored to .artifacts/tool must not keep
// a configured credential, even when the credential is split across writes.
func TestBoundedOutputSpillRedactsRegisteredSecrets(t *testing.T) {
	const secret = "sk-live-0123456789abcdef"
	redact.SetSecrets([]string{secret})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	out := newBoundedOutput(5, t.TempDir())
	for _, chunk := range []string{"head ", secret[:6], secret[6:14], secret[14:], " tail"} {
		_, err := out.Write([]byte(chunk))
		require.NoError(t, err)
	}

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.NoError(t, out.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(got), secret)
	require.Contains(t, string(got), redact.Marker)
	require.Contains(t, string(got), "head ")
	require.Contains(t, string(got), " tail")
}

func TestBoundedOutputCapsSpillFile(t *testing.T) {
	out := newBoundedOutput(20, t.TempDir())
	out.spillCap = 50

	_, err := out.Write([]byte(strings.Repeat("x", 200)))
	require.NoError(t, err)
	require.True(t, out.SpillCapped())

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.NoError(t, out.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.LessOrEqual(t, info.Size(), out.spillCap+100)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "[spill capped at")
}

// TestBoundedOutputFallsBackToTempArtifactDir keeps spills with no workspace in
// the dedicated temp artifact directory so Close retains and prunes them.
func TestBoundedOutputFallsBackToTempArtifactDir(t *testing.T) {
	out := newBoundedOutput(10, "")

	_, err := out.Write([]byte(strings.Repeat("x", 100)))
	require.NoError(t, err)

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.Contains(t, path, tempArtifactDir())
	t.Cleanup(func() { _ = os.Remove(path) })

	require.NoError(t, out.Close())
}

// TestBoundedOutputFallbackWhenWorkspaceUnwritable covers the same fallback when
// the workspace artifact directory cannot be created.
func TestBoundedOutputFallbackWhenWorkspaceUnwritable(t *testing.T) {
	root := t.TempDir()
	notADir := filepath.Join(root, "workspace-file")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o600))

	out := newBoundedOutput(10, filepath.Join(notADir, ".artifacts", "tool"))

	_, err := out.Write([]byte(strings.Repeat("x", 100)))
	require.NoError(t, err)

	path := out.SpillPath()
	require.NotEmpty(t, path)
	require.Contains(t, path, tempArtifactDir())
	t.Cleanup(func() { _ = os.Remove(path) })

	require.NoError(t, out.Close())
}
