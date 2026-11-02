package fstools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// charLimits caps files, in characters, against growth by the write tools. It
// keeps the memory files within their limits when the agent edits them with
// write_file, edit_file or append_file instead of the memory tool: a write that
// leaves the file longer than its limit is refused, while one that does not grow
// an already oversized file is still allowed so it can be cut down.
type charLimits map[string]int

// newCharLimits keys each absolute path by itself and by its symlink-resolved
// form, so an alias reaching the same file is still recognized.
func newCharLimits(files map[string]int) charLimits {
	if len(files) == 0 {
		return nil
	}
	limits := make(charLimits, 2*len(files))
	for path, limit := range files {
		abs, err := filepath.Abs(path)
		if err != nil {
			continue
		}
		limits[abs] = limit
		if resolved, err := resolvePathAgainstExistingAncestor(abs); err == nil {
			limits[resolved] = limit
		}
	}
	return limits
}

func (l charLimits) limitFor(path, workspace string, restrict bool) (int, bool) {
	if !filepath.IsAbs(path) && restrict {
		path = filepath.Join(workspace, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, false
	}
	candidates := []string{abs}
	if resolved, resolveErr := resolvePathAgainstExistingAncestor(abs); resolveErr == nil {
		candidates = append(candidates, resolved)
	}
	for _, candidate := range candidates {
		if limit, ok := l[candidate]; ok {
			return limit, true
		}
	}
	// Case-insensitive filesystems: compare the identity of an existing file.
	info, err := os.Stat(abs)
	if err != nil {
		return 0, false
	}
	for guarded, limit := range l {
		if ref, err := os.Stat(guarded); err == nil && os.SameFile(info, ref) {
			return limit, true
		}
	}
	return 0, false
}

func trimmedChars(b []byte) int { return utf8.RuneCountInString(strings.TrimSpace(string(b))) }

// check refuses a write of data to path (appended to the current content when
// appending) that leaves a limited file over its limit and longer than before.
// current reads the file as it is now; a missing file counts as empty.
func (l charLimits) check(
	path, workspace string,
	restrict, appending bool,
	data []byte,
	current func() []byte,
) error {
	if len(l) == 0 {
		return nil
	}
	limit, ok := l.limitFor(path, workspace, restrict)
	if !ok {
		return nil
	}
	cur := current()
	next := data
	if appending {
		next = append(append([]byte(nil), cur...), data...)
	}
	if used := trimmedChars(next); used > limit && used > trimmedChars(cur) {
		return fmt.Errorf(
			"write denied: %s is limited to %d characters and this write would make it %d; "+
				"use the memory tool (add/replace/remove) to consolidate its entries, or shorten the content",
			path, limit, used,
		)
	}
	return nil
}

// setCharLimits applies limits to the filesystem a write tool was built with.
func setCharLimits(fsys fileSystem, files map[string]int) {
	switch p := fsys.(type) {
	case *protectedReadFs:
		p.limits = newCharLimits(files)
	case *protectedWriteFs:
		p.limits = newCharLimits(files)
	}
}

// SetCharLimits caps files (absolute path -> characters): growth past the cap is
// refused. Call it before the tool is used.
func (t *WriteFileTool) SetCharLimits(files map[string]int) { setCharLimits(t.fs, files) }

// SetCharLimits caps files (absolute path -> characters): growth past the cap is
// refused. Call it before the tool is used.
func (t *EditFileTool) SetCharLimits(files map[string]int) { setCharLimits(t.fs, files) }

// SetCharLimits caps files (absolute path -> characters): growth past the cap is
// refused. Call it before the tool is used.
func (t *AppendFileTool) SetCharLimits(files map[string]int) { setCharLimits(t.fs, files) }
