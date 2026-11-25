package fstools

import (
	"fmt"
	"os"
	"path/filepath"
)

// memoryFiles lists files that only the memory tool may change, so the agent
// cannot get around its character limits with write_file, edit_file or
// append_file. Reading them is unaffected.
type memoryFiles []string

// newMemoryFiles keeps each absolute path and its symlink-resolved form, so an
// alias reaching the same file is still recognized.
func newMemoryFiles(paths []string) memoryFiles {
	var files memoryFiles
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		files = append(files, abs)
		if resolved, resolveErr := resolvePathAgainstExistingAncestor(abs); resolveErr == nil && resolved != abs {
			files = append(files, resolved)
		}
	}
	return files
}

func (m memoryFiles) contains(path, workspace string, restrict bool) bool {
	if !filepath.IsAbs(path) && restrict {
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	candidates := []string{abs}
	if resolved, resolveErr := resolvePathAgainstExistingAncestor(abs); resolveErr == nil {
		candidates = append(candidates, resolved)
	}
	for _, file := range m {
		for _, candidate := range candidates {
			if file == candidate {
				return true
			}
		}
	}
	// Case-insensitive filesystems: compare the identity of an existing file.
	info, err := os.Stat(abs)
	if err != nil {
		return false
	}
	for _, file := range m {
		if ref, statErr := os.Stat(file); statErr == nil && os.SameFile(info, ref) {
			return true
		}
	}
	return false
}

// check refuses any write to path when it is one of the memory files.
func (m memoryFiles) check(path, workspace string, restrict bool) error {
	if len(m) == 0 || !m.contains(path, workspace, restrict) {
		return nil
	}
	return fmt.Errorf(
		"write denied: %s is managed by the memory tool, which enforces its size limits; "+
			"use the memory tool (add/replace/remove) to change it",
		path,
	)
}

// setMemoryFiles applies the protection to the filesystem a write tool was built with.
func setMemoryFiles(fsys fileSystem, paths []string) {
	switch p := fsys.(type) {
	case *protectedReadFs:
		p.memoryFiles = newMemoryFiles(paths)
	case *protectedWriteFs:
		p.memoryFiles = newMemoryFiles(paths)
	}
}

// SetMemoryFiles makes the tool refuse every write to the given files (absolute
// paths), which only the memory tool may change. Call it before the tool is used.
func (t *WriteFileTool) SetMemoryFiles(paths []string) { setMemoryFiles(t.fs, paths) }

// SetMemoryFiles makes the tool refuse every write to the given files (absolute
// paths), which only the memory tool may change. Call it before the tool is used.
func (t *EditFileTool) SetMemoryFiles(paths []string) { setMemoryFiles(t.fs, paths) }

// SetMemoryFiles makes the tool refuse every write to the given files (absolute
// paths), which only the memory tool may change. Call it before the tool is used.
func (t *AppendFileTool) SetMemoryFiles(paths []string) { setMemoryFiles(t.fs, paths) }
