//go:build linux

package isolation

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestBuildLinuxBwrapArgs_CapDropAllways verifies that --cap-drop ALL appears in
// the generated bwrap argument list regardless of the effective uid.
func TestBuildLinuxBwrapArgs_CapDropAllways(t *testing.T) {
	for _, euid := range []int{0, 1000} {
		t.Run(fmt.Sprintf("euid%d", euid), func(t *testing.T) {
			prev := linuxEUID
			linuxEUID = func() int { return euid }
			t.Cleanup(func() { linuxEUID = prev })

			args, err := buildLinuxBwrapArgs("/bin/true", "/bin/true", []string{"/bin/true"}, "", nil, nil)
			if err != nil {
				t.Fatalf("buildLinuxBwrapArgs: %v", err)
			}
			found := false
			for i, a := range args {
				if a == "--cap-drop" && i+1 < len(args) && args[i+1] == "ALL" {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("missing --cap-drop ALL in args for euid=%d: %v", euid, args)
			}
		})
	}
}

// TestBuildLinuxBwrapArgs_UnshareUserOnlyForRoot verifies that --unshare-user is
// present when euid==0 and absent otherwise.
func TestBuildLinuxBwrapArgs_UnshareUserOnlyForRoot(t *testing.T) {
	for _, tc := range []struct {
		euid       int
		wantUserNS bool
	}{
		{euid: 0, wantUserNS: true},
		{euid: 1000, wantUserNS: false},
	} {
		t.Run(fmt.Sprintf("euid%d", tc.euid), func(t *testing.T) {
			prev := linuxEUID
			linuxEUID = func() int { return tc.euid }
			t.Cleanup(func() { linuxEUID = prev })

			args, err := buildLinuxBwrapArgs("/bin/true", "/bin/true", []string{"/bin/true"}, "", nil, nil)
			if err != nil {
				t.Fatalf("buildLinuxBwrapArgs: %v", err)
			}
			got := contains(args, "--unshare-user")
			if got != tc.wantUserNS {
				t.Fatalf("--unshare-user present=%t, want %t for euid=%d: %v", got, tc.wantUserNS, tc.euid, args)
			}
		})
	}
}

// TestCapEffZeroInSandbox is a behavioral integration test that runs a real
// bwrap sandbox via PrepareCommand and asserts that the sandboxed process sees
// CapEff==0 in /proc/self/status.  It skips when bwrap is unavailable or cannot
// create namespaces in the current environment (e.g. plain Docker runners without
// seccomp=unconfined).
func TestCapEffZeroInSandbox(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not in PATH")
	}
	// Sanity probe: can bwrap actually run here?
	probeErr := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "true",
	).Run()
	if probeErr != nil {
		t.Skipf("bwrap cannot create namespaces in this environment: %v", probeErr)
	}

	root := t.TempDir()
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot: %v", err)
	}
	t.Setenv(config.EnvHome, root)
	Configure(&config.Config{Isolation: config.IsolationConfig{Enabled: true}})
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	var buf bytes.Buffer
	cmd := exec.Command("grep", "CapEff", "/proc/self/status")
	cmd.Stdout = &buf

	if prepErr := PrepareCommand(cmd); prepErr != nil {
		t.Skipf("PrepareCommand failed (bwrap may lack required permissions): %v", prepErr)
	}

	if runErr := cmd.Run(); runErr != nil {
		t.Fatalf("sandboxed grep failed: %v; output: %s", runErr, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "CapEff:\t0000000000000000") {
		t.Fatalf("CapEff not zero inside sandbox; got:\n%s", out)
	}
}
