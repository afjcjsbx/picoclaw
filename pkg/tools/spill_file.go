package tools

import (
	"fmt"
	"os"
	"path/filepath"
)

// maxOutputSpillBytes caps the artifact written for oversized command output.
// Memory stays bounded by the in-memory window; the artifact keeps the
// beginning of the stream up to this limit.
const maxOutputSpillBytes = 5_000_000

// spillFile is the artifact that receives the full output stream of a command
// once its in-memory window overflows, up to a size cap. Foreground output and
// background session logs both spill through it. It does no locking: the owner
// serializes calls.
//
// The zero value is usable and spills into the temp artifact directory.
type spillFile struct {
	// dir is the preferred directory. Once the artifact exists it is the
	// directory actually used, which is tempArtifactDir when dir was not
	// writable.
	dir string
	// pattern is the os.CreateTemp pattern of the artifact name.
	pattern string
	// limit caps the artifact size in bytes; zero means maxOutputSpillBytes.
	limit int64

	file   *os.File
	path   string
	size   int64
	capped bool
	// off is set once spilling failed or the artifact was closed, so it is
	// never restarted.
	off bool
}

// active reports whether the artifact is open and receiving output.
func (f *spillFile) active() bool { return f.file != nil }

// start creates the artifact and writes the stream seen so far. It does
// nothing when the artifact already exists or spilling was turned off; a
// failure turns it off.
func (f *spillFile) start(seen []byte) {
	if f.file != nil || f.off {
		return
	}
	file, dir, err := f.create()
	if err != nil {
		f.off = true
		return
	}
	f.file, f.dir, f.path, f.size = file, dir, file.Name(), 0
	f.write(seen)
}

func (f *spillFile) create() (*os.File, string, error) {
	pattern := f.pattern
	if pattern == "" {
		pattern = "exec_*.log"
	}
	if f.dir != "" && os.MkdirAll(f.dir, 0o700) == nil {
		if file, err := os.CreateTemp(f.dir, pattern); err == nil {
			return file, f.dir, nil
		}
	}

	dir := tempArtifactDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", err
	}
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, "", err
	}
	return file, dir, nil
}

// tempArtifactDir is the fallback for workspaces that cannot receive artifacts.
// Keeping spills in a dedicated directory lets the owner prune them with the
// same retention policy as workspace artifacts instead of leaking files in the
// system temp directory.
func tempArtifactDir() string {
	return filepath.Join(os.TempDir(), "picoclaw-artifacts", "tool")
}

// write appends p up to the size cap and marks the artifact capped when it
// reaches it. A disk error abandons the artifact: it must never stop the owner
// from draining the child's pipe.
func (f *spillFile) write(p []byte) {
	if f.file == nil || f.capped || len(p) == 0 {
		return
	}

	limit := f.limit
	if limit <= 0 {
		limit = maxOutputSpillBytes
	}
	budget := limit - f.size
	if budget <= 0 {
		f.capped = true
		return
	}
	if int64(len(p)) < budget {
		f.append(p)
		return
	}

	if !f.append(p[:budget]) {
		return
	}
	marker := fmt.Sprintf("\n... [spill capped at %s bytes] ...\n", formatCount64(limit))
	if _, err := f.file.WriteString(marker); err != nil {
		f.abort()
		return
	}
	f.capped = true
}

func (f *spillFile) append(p []byte) bool {
	if _, err := f.file.Write(p); err != nil {
		f.abort()
		return false
	}
	f.size += int64(len(p))
	return true
}

// abort closes and removes the artifact and turns spilling off.
func (f *spillFile) abort() {
	if f.file != nil {
		_ = f.file.Close()
		f.file = nil
	}
	if f.path != "" {
		_ = os.Remove(f.path)
		f.path = ""
	}
	f.size = 0
	f.off = true
}

// close releases the file handle and turns spilling off. The artifact stays on
// disk.
func (f *spillFile) close() error {
	f.off = true
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}
