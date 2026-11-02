package tools

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
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

// TestBoundedOutputTruncatesOnRuneBoundary writes one byte at a time so the
// UTF-8 carry path is exercised across write boundaries.
func TestBoundedOutputTruncatesOnRuneBoundary(t *testing.T) {
	out := newBoundedOutput(10, t.TempDir())
	data := strings.Repeat("è", 100)

	for i := range data {
		_, err := out.Write([]byte{data[i]})
		require.NoError(t, err)
	}

	text, truncated := out.Render()
	require.True(t, truncated)
	require.True(t, utf8.ValidString(text))
	require.Contains(t, text, "chars omitted")
	require.NoError(t, out.Close())
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
