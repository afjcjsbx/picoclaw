package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpillFile_WritesSeenBytesThenAppends(t *testing.T) {
	dir := t.TempDir()
	f := spillFile{dir: dir, pattern: "unit_*.log"}
	require.False(t, f.active())

	f.write([]byte("ignored: nothing is open yet"))
	f.start([]byte("head|"))
	f.write([]byte("tail"))
	require.True(t, f.active())
	require.Equal(t, dir, filepath.Dir(f.path))
	require.True(t, strings.HasPrefix(filepath.Base(f.path), "unit_"))

	require.NoError(t, f.close())
	data, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.Equal(t, "head|tail", string(data))
	require.False(t, f.capped)
}

func TestSpillFile_CapsWithMarker(t *testing.T) {
	f := spillFile{dir: t.TempDir(), limit: 10}
	f.start([]byte("12345"))
	f.write([]byte("67890abc")) // crosses the cap
	f.write([]byte("ignored"))
	require.True(t, f.capped)
	require.NoError(t, f.close())

	data, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(data), "1234567890"))
	require.Contains(t, string(data), "[spill capped at 10 bytes]")
	require.NotContains(t, string(data), "abc")
}

func TestSpillFile_FallsBackToTempArtifactDir(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	f := spillFile{dir: filepath.Join(blocker, "sub")} // cannot be created under a file
	f.start([]byte("x"))
	require.True(t, f.active())
	t.Cleanup(func() { _ = os.Remove(f.path) })
	require.Equal(t, tempArtifactDir(), f.dir)
	require.NoError(t, f.close())
}

func TestSpillFile_StaysOffAfterCloseOrAbort(t *testing.T) {
	f := spillFile{dir: t.TempDir()}
	f.start([]byte("a"))
	path := f.path
	require.NoError(t, f.close())
	f.start([]byte("b"))
	require.False(t, f.active(), "a closed artifact must not restart")
	require.FileExists(t, path)

	g := spillFile{dir: t.TempDir()}
	g.start([]byte("a"))
	path = g.path
	g.abort()
	require.NoFileExists(t, path)
	require.Empty(t, g.path)
	g.start([]byte("b"))
	require.False(t, g.active(), "an aborted artifact must not restart")
}
