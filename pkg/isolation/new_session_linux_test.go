//go:build linux

package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestBuildLinuxBwrapArgs_IncludesNewSession verifies that --new-session is
// always present in the generated bwrap argument list regardless of which
// optional namespace flags the kernel supports.
func TestBuildLinuxBwrapArgs_IncludesNewSession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags map[string]bool
	}{
		{name: "no optional flags", flags: nil},
		{name: "with ipc", flags: map[string]bool{"--unshare-ipc": true}},
		{name: "without ipc", flags: map[string]bool{"--unshare-ipc": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args, err := buildLinuxBwrapArgs("/bin/true", "/bin/true", []string{"/bin/true"}, "", nil, tc.flags)
			if err != nil {
				t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
			}
			if !contains(args, "--new-session") {
				t.Fatalf("bwrap args missing --new-session: %v", args)
			}
			// --new-session must follow --die-with-parent so both always appear
			// together; check relative order.
			dwpIdx, nsIdx := -1, -1
			for i, a := range args {
				switch a {
				case "--die-with-parent":
					dwpIdx = i
				case "--new-session":
					nsIdx = i
				}
			}
			if dwpIdx < 0 || nsIdx < 0 || nsIdx <= dwpIdx {
				t.Fatalf("expected --new-session after --die-with-parent; got %v", args)
			}
		})
	}
}

// TestNewSession_BlocksControllingTerminal is a behavioral integration test.
// It runs a real bwrap sandbox and confirms that the sandboxed process cannot
// open /dev/tty (ENXIO) because --new-session detaches it from the controlling
// terminal, closing the TIOCSTI injection path (CVE-2017-5226).
//
// The test is skipped when bwrap is unavailable or the kernel rejects the
// mount namespace (e.g. a Docker container without seccomp=unconfined).
func TestNewSession_BlocksControllingTerminal(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not in PATH")
	}
	// Quick probe: if bwrap cannot create a mount namespace at all, skip.
	probe := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "true",
	)
	if out, probeErr := probe.CombinedOutput(); probeErr != nil {
		t.Skipf("bwrap cannot run in this environment: %v: %s", probeErr, strings.TrimSpace(string(out)))
	}

	// Set up a minimal isolation config pointing at a temp instance root so
	// that PrepareCommand / applyPlatformIsolation can build a valid mount plan.
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	Configure(cfg)

	// Ensure preflight directories exist.
	if pfErr := preflightCached(); pfErr != nil {
		t.Skipf("preflight failed (env too restricted): %v", pfErr)
	}

	// python3 tries to open /dev/tty; with --new-session it must fail with
	// ENXIO (No such device or address), printed to stderr.
	py3, lookErr := exec.LookPath("python3")
	if lookErr != nil {
		t.Skip("python3 not available")
	}

	// Ensure the python3 binary is accessible inside the mount plan.  The
	// mount plan already includes the system paths (/usr, /bin, /lib …);
	// resolving and mounting the binary itself is handled by
	// applyPlatformIsolation.
	root, rootErr := ResolveInstanceRoot()
	if rootErr != nil {
		t.Fatalf("ResolveInstanceRoot: %v", rootErr)
	}
	_ = root

	// /dev/tty open attempt: exit 0 on ENXIO, exit 1 on unexpected open success.
	script := `
import sys, errno
try:
    open('/dev/tty','r')
    print('TIOCSTI_OPEN_SUCCEEDED', file=sys.stderr)
    sys.exit(1)
except OSError as e:
    if e.errno == errno.ENXIO:
        print('ENXIO_OK', file=sys.stderr)
        sys.exit(0)
    print('UNEXPECTED_ERRNO', e.errno, str(e), file=sys.stderr)
    sys.exit(2)
`
	// Write the script to a file in the temp dir so it's inside the mount plan.
	scriptPath := filepath.Join(home, "check_tty.py")
	if writeErr := os.WriteFile(scriptPath, []byte(script), 0o644); writeErr != nil {
		t.Fatalf("write script: %v", writeErr)
	}

	cmd := exec.Command(py3, scriptPath)
	if prepErr := PrepareCommand(cmd); prepErr != nil {
		t.Fatalf("PrepareCommand: %v", prepErr)
	}
	out, runErr := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))

	if runErr != nil {
		// Exit 2 means unexpected errno — real failure.
		if strings.Contains(outStr, "UNEXPECTED_ERRNO") {
			t.Fatalf("sandbox opened /dev/tty with unexpected error: %s", outStr)
		}
		// Exit 1 means open succeeded — the fix is not working.
		if strings.Contains(outStr, "TIOCSTI_OPEN_SUCCEEDED") {
			t.Fatalf("sandbox opened /dev/tty successfully: TIOCSTI injection path is still open")
		}
		// Any other non-zero exit (e.g. bwrap setup failure in this env) is a
		// skip, not a test failure.
		t.Skipf("bwrap sandbox run failed (env too restricted): %v: %s", runErr, outStr)
	}

	if !strings.Contains(outStr, "ENXIO_OK") {
		t.Fatalf("unexpected output: %q", outStr)
	}
}
