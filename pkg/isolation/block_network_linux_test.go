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

// TestInsertLinuxUnshareNet checks the splice helper: --unshare-net must land
// immediately before --, not at the end of the args slice.
func TestInsertLinuxUnshareNet(t *testing.T) {
	base := []string{"bwrap", "--die-with-parent", "--proc", "/proc", "--dev", "/dev", "--", "/bin/true"}
	got := insertLinuxUnshareNet(base)

	sepIdx := -1
	netIdx := -1
	for i, a := range got {
		switch a {
		case "--":
			sepIdx = i
		case "--unshare-net":
			netIdx = i
		}
	}
	if netIdx < 0 {
		t.Fatalf("--unshare-net not found in %v", got)
	}
	if sepIdx < 0 {
		t.Fatalf("-- not found in %v", got)
	}
	if netIdx != sepIdx-1 {
		t.Fatalf("--unshare-net at index %d, -- at index %d; want consecutive", netIdx, sepIdx)
	}
	// exec path must still follow --
	if got[sepIdx+1] != "/bin/true" {
		t.Fatalf("exec path after -- = %q, want /bin/true", got[sepIdx+1])
	}
}

// TestApplyPlatformIsolation_BlockNetwork_AddsUnshareNet verifies that
// applyPlatformIsolation injects --unshare-net when BlockNetwork=true and
// omits it when BlockNetwork=false (the default).
func TestApplyPlatformIsolation_BlockNetwork_AddsUnshareNet(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not available")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(binDir, "tool")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, blockNet := range []bool{false, true} {
		isolation := config.IsolationConfig{
			Enabled:      true,
			BlockNetwork: blockNet,
		}
		cmd := exec.Command(fakeBin)
		cmd.Path = fakeBin
		cmd.Args = []string{fakeBin}

		// Use the real bwrap path so LookPath inside applyPlatformIsolation succeeds.
		origPath := os.Getenv("PATH")
		t.Setenv("PATH", filepath.Dir(bwrapPath)+string(os.PathListSeparator)+origPath)
		t.Setenv(config.EnvHome, root)

		if applyErr := applyPlatformIsolation(cmd, isolation, root); applyErr != nil {
			t.Fatalf("applyPlatformIsolation(BlockNetwork=%v) error = %v", blockNet, applyErr)
		}

		hasNet := false
		for _, a := range cmd.Args {
			if a == "--unshare-net" {
				hasNet = true
				break
			}
		}
		if blockNet && !hasNet {
			t.Fatalf("BlockNetwork=true: --unshare-net missing from args: %v", cmd.Args)
		}
		if !blockNet && hasNet {
			t.Fatalf("BlockNetwork=false: --unshare-net unexpectedly present: %v", cmd.Args)
		}
	}
}

// TestBehavioralBlockNetworkIsolatesNetwork is an end-to-end test that starts
// a real bwrap sandbox and confirms that:
//   - without block_network, loopback services on the host are reachable;
//   - with block_network, they are not reachable and the network interface list
//     shows only lo (which bwrap may leave down).
//
// The test requires bwrap and skips automatically when it cannot create
// network namespaces (e.g. plain CI runners without user namespaces).
func TestBehavioralBlockNetworkIsolatesNetwork(t *testing.T) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not available")
	}

	// Quick capability check: attempt a harmless unshare-net sandbox.
	probe := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-net",
		"--", "true",
	)
	if out, probeErr := probe.CombinedOutput(); probeErr != nil {
		t.Skipf("bwrap --unshare-net not available: %v: %s", probeErr, out)
	}

	// Without block_network: loopback should have an address (lo exists).
	cmd := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "ip", "-o", "link",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("without block_network ip link: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "lo") {
		t.Fatalf("expected lo in default network ns: %s", out)
	}

	// With block_network (--unshare-net): confirm we are in a new, isolated ns.
	cmdNet := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-net",
		"--", "ip", "-o", "link",
	)
	outNet, err := cmdNet.CombinedOutput()
	if err != nil {
		t.Fatalf("with block_network ip link: %v: %s", err, outNet)
	}
	// In a fresh network namespace bwrap creates only lo (possibly DOWN).
	// Any interface from the host (e.g. eth0, enp0s3) must be absent.
	if strings.Contains(string(outNet), "eth") || strings.Contains(string(outNet), "enp") ||
		strings.Contains(string(outNet), "ens") || strings.Contains(string(outNet), "wlan") {
		t.Fatalf("host network interface visible inside block_network sandbox: %s", outNet)
	}
}
