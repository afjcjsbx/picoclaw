//go:build linux

package isolation

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// argBroadMountWarn tracks paths for which we have already emitted a "refused
// broad mount" warning to avoid log spam on repeated calls with the same arg.
var argBroadMountWarn sync.Map

// isLinuxArgPathCandidate reports whether arg is a plain absolute path token
// that is safe to treat as a filesystem path. Command strings passed to
// sh -c (e.g. "cat /home/bob/secret; rm -rf /") are rejected because they
// contain shell metacharacters and would otherwise cause unintended mounts.
//
// A valid token must be absolute and may not contain whitespace, control
// characters, or any of the following shell metacharacters:
//
//	; & | < > ( ) $ ` ' " \ * ? [ ] { }
func isLinuxArgPathCandidate(arg string) (string, bool) {
	if !filepath.IsAbs(arg) {
		return "", false
	}
	for _, r := range arg {
		if isLinuxArgUnsafeRune(r) {
			return "", false
		}
	}
	return arg, true
}

// isLinuxArgUnsafeRune returns true for characters that indicate arg is a
// shell command string rather than a bare filesystem path.
func isLinuxArgUnsafeRune(r rune) bool {
	// Control characters (includes \n, \r, \t) and space.
	if r <= 0x20 || r == 0x7f {
		return true
	}
	switch r {
	case ';', '&', '|', '<', '>', '(', ')', '$', '`', '\'', '"', '\\',
		'*', '?', '[', ']', '{', '}':
		return true
	}
	return false
}

// isLinuxBroadMount reports whether path is too broad to auto-expose from a
// command-line argument. The following are always refused:
//
//   - "/" itself
//   - any path directly under "/" (/home, /etc, /tmp, /proc, /sys, /dev, …)
//   - the invoking user's home directory
//   - any ancestor of the home directory
//
// Callers that need these paths must list them in isolation.expose_paths.
func isLinuxBroadMount(path string) bool {
	clean := filepath.Clean(path)
	if clean == "/" {
		return true
	}
	// Directly under "/": depth-1 paths like /home, /tmp, /etc, /proc.
	if filepath.Dir(clean) == "/" {
		return true
	}
	// Home directory and every ancestor of home (e.g. /home when home=/home/bob).
	home, err := os.UserHomeDir()
	if err == nil {
		home = filepath.Clean(home)
		// clean equals the home dir, or home is rooted under clean (clean is
		// an ancestor of home).
		if clean == home || boundaryPathWithin(clean, home) {
			return true
		}
	}
	return false
}

// linuxPseudoFilesystemPrefixes lists kernel-backed filesystems that the
// sandbox provides itself. A fresh /proc is mounted with --proc and /dev with
// --dev, so entries from these trees never need to be copied in from the host.
var linuxPseudoFilesystemPrefixes = []string{"/proc", "/sys", "/dev"}

// isLinuxPseudoFilesystemPath reports whether path lives in a pseudo-filesystem
// that is owned by the sandbox rather than the host. Auto-binding such a path is
// useless and can break sandbox setup: /proc/self/... resolves to a host PID
// that does not exist in the sandbox PID namespace, so bwrap fails while
// creating the mount point (e.g. "Can't mkdir parents for /proc/1234/status").
func isLinuxPseudoFilesystemPath(path string) bool {
	clean := filepath.Clean(path)
	for _, prefix := range linuxPseudoFilesystemPrefixes {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return true
		}
	}
	return false
}

// linuxPlanCoversPath reports whether any rule in plan has a target that is
// path or an ancestor of path. When true, adding a new rule for path would
// stack a potentially conflicting mode on top of an existing one.
func linuxPlanCoversPath(plan []MountRule, path string) bool {
	clean := filepath.Clean(path)
	for _, rule := range plan {
		if boundaryPathWithin(filepath.Clean(rule.Target), clean) {
			return true
		}
	}
	return false
}

// warnArgBroadMount logs a single warning per distinct path that was refused
// because it was too broad to auto-mount from a command-line argument.
func warnArgBroadMount(path string) {
	if _, already := argBroadMountWarn.LoadOrStore(path, struct{}{}); already {
		return
	}
	logger.WarnCF("isolation", "argument path not auto-exposed: too broad",
		map[string]any{
			"path":        path,
			"reason":      "auto-mounting this path would expose a broad host directory; use isolation.expose_paths to grant explicit access",
			"effect":      "the command will see ENOENT or EROFS for this path inside the sandbox",
			"expose_hint": "add the path to isolation.expose_paths in config.json",
		})
}

// appendLinuxArgumentMountsFor processes one resolved path token and updates
// the mount plan accordingly. It is a pure helper kept separate from the loop
// so it can be tested independently.
func appendLinuxArgumentMountsFor(plan []MountRule, path string) []MountRule {
	clean := filepath.Clean(path)

	// /proc, /sys and /dev are mounted by bwrap itself. Binding host entries
	// from these trees is unnecessary and, for resolved /proc/self/... paths,
	// makes bwrap fail because the host PID is absent from the sandbox.
	if isLinuxPseudoFilesystemPath(clean) {
		return plan
	}

	if isLinuxBroadMount(clean) {
		warnArgBroadMount(clean)
		return plan
	}

	_, statErr := os.Stat(clean)
	if statErr == nil {
		// Existing path. If it is a symlink, bwrap >= 0.12 refuses to bind-
		// mount onto the symlink destination ("Can't mount on symlink
		// destination"). Mount only the resolved real target. The symlink's
		// parent directory remains visible only through whatever rules are
		// already in the plan.
		if isSymlinkPath(clean) {
			resolved, err := filepath.EvalSymlinks(clean)
			if err != nil {
				return plan
			}
			resolvedClean := filepath.Clean(resolved)
			if isLinuxBroadMount(resolvedClean) || linuxPlanCoversPath(plan, resolvedClean) {
				return plan
			}
			if !isSymlinkPath(resolvedClean) {
				plan = ensureLinuxMountRule(plan, resolvedClean, resolvedClean, "ro")
			}
			resolvedParent := filepath.Dir(resolvedClean)
			if !isLinuxBroadMount(resolvedParent) && !linuxPlanCoversPath(plan, resolvedParent) {
				plan = ensureLinuxMountRule(plan, resolvedParent, resolvedParent, "ro")
			}
			return plan
		}
		// Plain existing path: expose read-only. A directory passed on the
		// command line must not become a writable hole in the sandbox;
		// writable locations come from expose_paths or the working directory.
		// Skip if the plan already has a covering rule (prevents stacking a
		// conflicting mode on top).
		if linuxPlanCoversPath(plan, clean) {
			return plan
		}
		plan = ensureLinuxMountRule(plan, clean, clean, "ro")
		// Also mount the resolved real file when clean is not a symlink but
		// still has a symlink somewhere in its path components.
		if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
			resolvedClean := filepath.Clean(resolved)
			notBroad := !isLinuxBroadMount(resolvedClean)
			notCovered := !linuxPlanCoversPath(plan, resolvedClean)
			if notBroad && notCovered && !isSymlinkPath(resolvedClean) {
				plan = ensureLinuxMountRule(plan, resolvedClean, resolvedClean, "ro")
			}
		}
		return plan
	}

	if !errors.Is(statErr, os.ErrNotExist) {
		return plan
	}

	// Non-existent path: expose the parent rw so the command can create the
	// output file at that location (e.g. a transcoder writing to a not-yet-
	// existing path under a temporary directory).
	parent := filepath.Dir(clean)
	if parent == clean {
		return plan
	}
	if isLinuxBroadMount(parent) {
		warnArgBroadMount(parent)
		return plan
	}
	if _, err := os.Stat(parent); err != nil {
		return plan
	}
	if !linuxPlanCoversPath(plan, parent) {
		plan = ensureLinuxMountRule(plan, parent, parent, "rw")
	}
	return plan
}
