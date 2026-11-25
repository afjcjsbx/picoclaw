//go:build linux

package isolation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkSymlinkMountTargets guards against CVE-2026-87766 on bwrap < 0.12.0.
//
// bwrap before 0.12.0 follows symlinks when setting up bind mounts, so a
// sandboxed agent that plants a symlink inside a writable mount can redirect a
// later mount target to an arbitrary host path. We detect and refuse the whole
// command before handing arguments to bwrap, regardless of bwrap version.
//
// Only path components that lie strictly below a writable mount's sandbox
// target are inspected. System-level symlinks (Debian merged-usr
// /bin -> usr/bin, Silverblue /home -> var/home, etc.) sit under read-only
// mounts and are therefore never reached by this check.
//
// The check runs on every launch because symlinks can appear inside a writable
// mount after the preflight cache was last populated.
func checkSymlinkMountTargets(plan []MountRule) error {
	for i, r := range plan {
		for _, w := range plan[:i] {
			if w.Mode != "rw" {
				// Only writable mounts can contain agent-planted symlinks.
				continue
			}
			if !boundaryPathWithin(w.Target, r.Target) {
				continue
			}
			rel, err := filepath.Rel(w.Target, r.Target)
			if err != nil || rel == "." || rel == "" {
				// r.Target is the same as or directly within w.Target;
				// no path components to traverse.
				continue
			}
			if walkErr := symlinkGuardWalk(w.Source, rel, w, r); walkErr != nil {
				return walkErr
			}
		}
	}
	return nil
}

// symlinkGuardWalk Lstats each path component of rel under hostBase. It
// returns an error if any component is a symlink, which would let bwrap < 0.12
// redirect the mount to an unintended host location.
//
// hostBase is the host-side root that corresponds to the writable mount's
// sandbox target (W.Source when Source != Target for remapped mounts).
func symlinkGuardWalk(hostBase, rel string, w, r MountRule) error {
	current := hostBase
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, lstatErr := os.Lstat(current)
		if lstatErr != nil {
			return nil //nolint:nilerr // absent component means safe traversal; lstatErr is intentionally not propagated
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf(
				"isolation: mount target %q (source %q) traverses symlink %q "+
					"inside writable mount %q -> %q; "+
					"remove the symlink or choose a target path that does not pass through it",
				r.Target, r.Source, current, w.Source, w.Target,
			)
		}
	}
	return nil
}
