//go:build linux

package isolation

// Per-path namespace probe cache and helpers for EINVAL/EPERM classification.
// probeLinuxNamespaceFlags and cachedLinuxNamespaceFlags stay in platform_linux.go;
// this file provides the state, classifier, and runner variable they share.

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// linuxNamespaceProbe is the per-bwrap-path namespace probe cache.
//
// Only successful probes are stored; a failed probe is retried on the next
// call so an environment corrected with --security-opt or CAP_SYS_ADMIN
// recovers without a process restart.  The warned set prevents log spam when
// the same failure fires on every command invocation before the operator fixes
// the container configuration.
var linuxNamespaceProbe struct {
	mu      sync.Mutex
	results map[string]map[string]bool // bwrap binary path → flag → supported
	warned  map[string]struct{}        // error messages already logged once
}

// linuxNamespaceProbeRun is the runner that cachedLinuxNamespaceFlags passes to
// probeLinuxNamespaceFlags.  Swapped by tests to exercise cache behavior
// without requiring a real bwrap binary on the host.
var linuxNamespaceProbeRun = func(path string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.Bytes(), err
}

// isNamespaceUnsupported reports whether the bwrap stderr indicates EINVAL —
// the kernel genuinely does not support this namespace type, so the flag should
// be dropped with a warning.
//
// EPERM, "Operation not permitted", "No permissions to create new namespace",
// or any other failure means the calling environment (seccomp policy, AppArmor,
// missing capabilities) blocked the syscall.  Those must surface as errors so
// the operator can act; silently dropping the namespace in that case gives false
// assurance about the isolation state.
func isNamespaceUnsupported(stderr string) bool {
	// strerror(EINVAL) == "Invalid argument"; only this strerror suffix means
	// "kernel lacks the namespace type".
	return strings.Contains(stderr, "Creating new namespace failed: Invalid argument")
}

// namespaceProbeError builds an actionable error for a probe failure that is
// not EINVAL.  It includes the real bwrap output and suggests concrete fixes for
// the most common container environments.
func namespaceProbeError(flag, stderr string, cause error) error {
	return fmt.Errorf(
		"bubblewrap optional namespace flag %s failed: %w; bwrap output: %q\n"+
			"\nCommon causes and fixes:\n"+
			"  Docker default seccomp: docker run --security-opt seccomp=unconfined\n"+
			"    or a custom profile that allows unshare(2)/clone(2).\n"+
			"    Also check AppArmor: docker run --security-opt apparmor=unconfined.\n"+
			"  Root without CAP_SYS_ADMIN: docker run --cap-add SYS_ADMIN,\n"+
			"    or run picoclaw as a non-root user.\n"+
			"  Kubernetes RuntimeDefault seccomp: use a custom profile that allows\n"+
			"    clone(2)/unshare(2).\n"+
			"\nTo opt out of subprocess isolation:\n"+
			"  set \"isolation.enabled\": false in config.json\n"+
			"  Risk: child processes will access host files and network without restriction.",
		flag, cause, strings.TrimSpace(stderr),
	)
}
