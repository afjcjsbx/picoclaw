//go:build linux

package isolation

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// bwrapSemver holds a parsed bubblewrap release triple.
type bwrapSemver struct {
	Major, Minor, Patch int
}

func (v bwrapSemver) lessThan(other bwrapSemver) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

var (
	bwrapVersionMu    sync.Mutex
	bwrapVersionCache = map[string]bwrapSemver{}

	// bwrapCVEWarnOnce ensures the CVE-2026-87766 warning fires at most once
	// per process regardless of how many child processes are started.
	bwrapCVEWarnOnce sync.Once
)

// checkBwrapVersion verifies that bwrapPath is a usable, non-setuid bubblewrap
// binary and that its version is compatible with the flags PicoClaw passes.
// run is injectable so tests can supply controlled --version output without
// spawning a real binary.
//
// Hard errors (return non-nil):
//   - setuid bit is set on the binary
//   - version < 0.3.3 (lacks the '--' separator PicoClaw always passes)
//
// Soft outcome (return nil, side-effect warning):
//   - 0.3.3 <= version < 0.12.0: one Warn per process about CVE-2026-87766.
//
// Soft outcome (return nil, no warning):
//   - version >= 0.12.0
//   - run() returns an error (transient; not cached; let the namespace probe surface it)
//   - output is unparseable (safe default: allow; log at Debug)
func checkBwrapVersion(bwrapPath string, run func(string, ...string) ([]byte, error)) error {
	// Fail closed on a setuid bwrap before even running --version.
	// bubblewrap 0.12+ dropped the setuid execution model; older setuid builds
	// are the attack surface of CVE-2026-41163 and similar privilege-escalation
	// chains because the kernel grants capabilities to the calling user process.
	if info, err := os.Stat(bwrapPath); err == nil && info.Mode()&os.ModeSetuid != 0 {
		return fmt.Errorf(
			"bubblewrap at %s has the setuid bit set; PicoClaw refuses to run a setuid bwrap "+
				"because the setuid execution model is the attack surface for privilege escalation "+
				"(e.g. CVE-2026-41163); bubblewrap 0.12+ no longer uses setuid; "+
				"remove the setuid bit or install bubblewrap >= 0.12.0; "+
				"or disable isolation: set \"isolation.enabled\": false in config.json "+
				"(disabling isolation means child processes run without Linux filesystem isolation)",
			bwrapPath,
		)
	}

	// Try the in-process cache first; only successful parses are stored, so a
	// missing entry means either a first call or a previous transient run failure.
	bwrapVersionMu.Lock()
	ver, cached := bwrapVersionCache[bwrapPath]
	bwrapVersionMu.Unlock()

	if !cached {
		out, runErr := run(bwrapPath, "--version")
		if runErr != nil {
			// Transient run failure: do not cache; let the namespace probe surface
			// the real problem. Allow startup to continue.
			logger.DebugCF("isolation", "bwrap --version failed; version check skipped",
				map[string]any{
					"path":  bwrapPath,
					"error": runErr.Error(),
				})
			return nil //nolint:nilerr // intentional: transient run failure is non-fatal
		}
		v, ok := parseBwrapVersionString(string(out))
		if !ok {
			// Unrecognized output format: not a hard error; the binary may still
			// work. A new bubblewrap release might change the version format.
			logger.DebugCF("isolation", "bwrap --version output not recognized; version check skipped",
				map[string]any{
					"path":   bwrapPath,
					"output": strings.TrimSpace(string(out)),
				})
			return nil
		}
		ver = v
		bwrapVersionMu.Lock()
		bwrapVersionCache[bwrapPath] = ver
		bwrapVersionMu.Unlock()
	}

	return applyBwrapVersionPolicy(ver)
}

// applyBwrapVersionPolicy returns a hard error for versions too old to work
// with PicoClaw's flag set, and emits a one-time CVE warning for versions that
// work but have known unfixed vulnerabilities.
func applyBwrapVersionPolicy(ver bwrapSemver) error {
	// 0.1.x and 0.2.x predate the '--' argument separator that PicoClaw always
	// passes; they fail with an obscure "Unknown option --" error at runtime.
	minWorking := bwrapSemver{0, 3, 3}
	// CVE-2026-87766 (symlink traversal during sandbox setup) is fixed in 0.12.0.
	minSafe := bwrapSemver{0, 12, 0}

	if ver.lessThan(minWorking) {
		return fmt.Errorf(
			"bubblewrap %d.%d.%d is too old; PicoClaw requires bubblewrap >= 0.3.3 "+
				"(0.1.x and 0.2.x lack the '--' argument separator and fail with "+
				"\"Unknown option --\" at runtime); bubblewrap >= 0.12.0 is recommended "+
				"(fixes CVE-2026-87766 symlink traversal); install a newer version: %s; "+
				"or disable isolation: set \"isolation.enabled\": false in config.json "+
				"(disabling isolation means child processes run without Linux filesystem isolation)",
			ver.Major, ver.Minor, ver.Patch,
			bwrapInstallHint(),
		)
	}

	if ver.lessThan(minSafe) {
		// ponytail: global sync.Once; acceptable because one warning per process is the goal.
		bwrapCVEWarnOnce.Do(func() {
			logger.WarnCF("isolation", "bubblewrap < 0.12.0: CVE-2026-87766 not fixed in binary",
				map[string]any{
					"version": fmt.Sprintf("%d.%d.%d", ver.Major, ver.Minor, ver.Patch),
					"cve":     "CVE-2026-87766",
					"description": "symlink traversal while the sandbox is being built can " +
						"create files outside it",
					"fixed_in": "bubblewrap 0.12.0",
					"note": "your distro package may carry a backport that the version " +
						"string cannot show",
					"action": "upgrade bubblewrap or use a distro build that carries the fix",
				})
		})
	}

	return nil
}

// parseBwrapVersionString extracts the version triple from "bwrap --version"
// output. It accepts:
//   - "bubblewrap 0.9.0-1ubuntu1"  (distro suffix after the patch)
//   - "bubblewrap 0.13.1-rc1"      (pre-release suffix)
//   - "bubblewrap 0.13.1"          (clean release)
//   - extra lines before or after the version line
//   - the token "bubblewrap" anywhere on a line (case-insensitive)
//
// Returns false when no recognizable version triple is found; the caller should
// log at Debug and continue rather than treating this as a hard error.
func parseBwrapVersionString(output string) (bwrapSemver, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if strings.EqualFold(f, "bubblewrap") && i+1 < len(fields) {
				if ver, ok := parseBwrapSemverField(fields[i+1]); ok {
					return ver, true
				}
			}
		}
	}
	return bwrapSemver{}, false
}

// parseBwrapSemverField parses "X.Y.Z" with an optional suffix that begins
// with '-' or '+', such as "-rc1" or "-1ubuntu1".
func parseBwrapSemverField(s string) (bwrapSemver, bool) {
	// Strip suffix: everything from the first '-' or '+' onward.
	for i, r := range s {
		if r == '-' || r == '+' {
			s = s[:i]
			break
		}
	}
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return bwrapSemver{}, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return bwrapSemver{}, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return bwrapSemver{}, false
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return bwrapSemver{}, false
	}
	return bwrapSemver{major, minor, patch}, true
}
