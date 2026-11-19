//go:build linux

package isolation

import "path/filepath"

// resolveLinuxExecDir returns the canonical path of dir with all symlink
// components resolved. bwrap >= 0.12 rejects symlink paths as bind-mount
// targets ("Can't mount on symlink destination"), so the working directory
// must be fully resolved before it is added to the mount plan or passed to
// --chdir. Returns dir unchanged when dir is empty or realpath fails (e.g.
// the directory does not yet exist).
func resolveLinuxExecDir(dir string) string {
	if dir == "" {
		return dir
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}
