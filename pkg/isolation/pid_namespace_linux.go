//go:build linux

package isolation

import (
	"fmt"
	"strings"
)

// probeLinuxPIDNamespace checks whether bwrap can run a process in its own
// PID namespace with a fresh /proc mount on this host/container.
//
// PID-namespace isolation is a required security property: without it the
// sandbox shares the host PID table, allowing enumeration of host processes,
// delivery of signals to same-uid processes, and orphaned children that
// outlive the sandbox (--die-with-parent alone is insufficient).
//
// The probe uses --unshare-pid --proc /proc together because they must work
// as a pair at runtime. Docker's default masked /proc blocks the fresh mount
// with EPERM unless the container is started with
// --security-opt systempaths=unconfined.
//
// If the probe fails a clear, actionable error is returned. The caller must
// not start bwrap without a working PID namespace. To opt out of isolation
// entirely, set "isolation.enabled": false in config.json (risk: no
// filesystem isolation; child processes can read/write more host files).
func probeLinuxPIDNamespace(bwrapPath string, run func(string, ...string) ([]byte, error)) error {
	output, err := run(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--unshare-pid",
		"--proc", "/proc",
		"--", "true",
	)
	if err == nil {
		return nil
	}
	// Docker masks /proc by default; bwrap cannot mount a fresh procfs inside
	// the PID namespace and exits with "Can't mount proc on /proc: Operation
	// not permitted". The kernel reports EPERM on the proc mount step.
	return fmt.Errorf(
		"bwrap PID namespace probe failed (%w: %s); "+
			"a fresh /proc inside the PID namespace requires an unmasked /proc.\n"+
			"Docker fix:        add --security-opt systempaths=unconfined to docker run\n"+
			"Kubernetes fix:    set securityContext.procMount: Unmasked (requires user-namespace support)\n"+
			"Disable isolation: set \"isolation.enabled\": false in config.json "+
			"(disables all filesystem isolation; child processes can read/write more host files)",
		err,
		strings.TrimSpace(string(output)),
	)
}
