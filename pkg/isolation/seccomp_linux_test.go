//go:build linux

package isolation

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sipeed/picoclaw/pkg/config"
)

// runBPF is a minimal classic-BPF interpreter for seccomp filter verification.
// data is a serialized seccomp_data (at least 8 bytes: nr at 0, arch at 4).
// Returns the SECCOMP_RET value.
func runBPF(t *testing.T, insns []unix.SockFilter, data []byte) uint32 {
	t.Helper()
	var A uint32
	pc := 0
	for pc < len(insns) {
		insn := insns[pc]
		switch insn.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			if int(insn.K)+4 > len(data) {
				t.Fatalf("BPF load out of bounds: offset %d, data len %d", insn.K, len(data))
			}
			A = binary.LittleEndian.Uint32(data[insn.K:])
			pc++
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
			if A == insn.K {
				pc += int(insn.Jt) + 1
			} else {
				pc += int(insn.Jf) + 1
			}
		case unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K:
			if A&insn.K != 0 {
				pc += int(insn.Jt) + 1
			} else {
				pc += int(insn.Jf) + 1
			}
		case unix.BPF_RET | unix.BPF_K:
			return insn.K
		default:
			t.Fatalf("unexpected BPF opcode 0x%x at pc=%d", insn.Code, pc)
		}
	}
	t.Fatal("BPF program ran off end")
	return 0
}

// seccompData constructs a 64-byte seccomp_data slice with nr and arch set.
func seccompData(nr, arch uint32) []byte {
	d := make([]byte, 64)
	binary.LittleEndian.PutUint32(d[0:], nr)
	binary.LittleEndian.PutUint32(d[4:], arch)
	return d
}

func TestBuildBPFDenylist_AllowsNormalSyscall(t *testing.T) {
	// Use a fixed arch value and an unlisted syscall number to verify allow.
	arch := uint32(0xc000003e) // amd64, but logic is the same regardless
	insns := buildBPFDenylist(arch, false, []uint32{100, 200, 300})
	got := runBPF(t, insns, seccompData(1 /* getpid, not in list */, arch))
	if got != seccompRetAllow {
		t.Fatalf("expected ALLOW (0x%x), got 0x%x", seccompRetAllow, got)
	}
}

func TestBuildBPFDenylist_DeniesListedSyscall(t *testing.T) {
	arch := uint32(0xc000003e)
	insns := buildBPFDenylist(arch, false, []uint32{100, 200, 300})
	for _, nr := range []uint32{100, 200, 300} {
		got := runBPF(t, insns, seccompData(nr, arch))
		if got != seccompRetDenyEPERM {
			t.Fatalf("syscall %d: expected DENY (0x%x), got 0x%x", nr, seccompRetDenyEPERM, got)
		}
	}
}

func TestBuildBPFDenylist_RejectsWrongArch(t *testing.T) {
	arch := uint32(0xc000003e) // filter built for this arch
	insns := buildBPFDenylist(arch, false, []uint32{1})
	// A request claiming a different arch (e.g. arm64) is denied.
	wrongArch := uint32(0xc00000b7)
	got := runBPF(t, insns, seccompData(1, wrongArch))
	if got != seccompRetDenyEPERM {
		t.Fatalf("wrong arch should be denied, got 0x%x", got)
	}
}

func TestBuildBPFDenylist_RejectsX32SyscallBit(t *testing.T) {
	// Build a filter with the x32 guard. Syscall 1 is not in the denylist but
	// the x32 bit makes it denied.
	arch := uint32(0xc000003e)
	insns := buildBPFDenylist(arch, true /*hasX32*/, []uint32{999})
	got := runBPF(t, insns, seccompData(1|x32SyscallBit, arch))
	if got != seccompRetDenyEPERM {
		t.Fatalf("x32 ABI syscall should be denied, got 0x%x", got)
	}
	// Without the bit, the same number is allowed.
	got = runBPF(t, insns, seccompData(1, arch))
	if got != seccompRetAllow {
		t.Fatalf("non-x32 syscall 1 should be allowed, got 0x%x", got)
	}
}

func TestWriteSockFilters_RoundTrip(t *testing.T) {
	insns := []unix.SockFilter{
		{Code: 0x20, Jt: 0, Jf: 0, K: 4},
		{Code: 0x15, Jt: 0, Jf: 2, K: 0xc000003e},
		{Code: 0x06, Jt: 0, Jf: 0, K: seccompRetAllow},
		{Code: 0x06, Jt: 0, Jf: 0, K: seccompRetDenyEPERM},
	}
	var buf bytes.Buffer
	if err := writeSockFilters(&buf, insns); err != nil {
		t.Fatalf("writeSockFilters() error = %v", err)
	}
	if got, want := buf.Len(), 8*len(insns); got != want {
		t.Fatalf("writeSockFilters() wrote %d bytes, want %d", got, want)
	}
	// Re-parse the bytes and verify each instruction.
	b := buf.Bytes()
	for i, want := range insns {
		code := binary.LittleEndian.Uint16(b[8*i:])
		jt := b[8*i+2]
		jf := b[8*i+3]
		k := binary.LittleEndian.Uint32(b[8*i+4:])
		got := unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
		if got != want {
			t.Fatalf("insn[%d]: got %+v, want %+v", i, got, want)
		}
	}
}

func TestPrepareSeccompFd_FdNumbering(t *testing.T) {
	insns := buildSeccompFilter()
	if insns == nil {
		t.Skip("no seccomp filter defined for this architecture")
	}

	// Without any existing ExtraFiles the fd must be 3.
	cmd1 := &exec.Cmd{}
	fdNum1, err := prepareSeccompFd(cmd1)
	if err != nil {
		t.Fatalf("prepareSeccompFd() error = %v", err)
	}
	if fdNum1 != 3 {
		t.Fatalf("fdNum = %d, want 3", fdNum1)
	}
	if len(cmd1.ExtraFiles) != 1 {
		t.Fatalf("ExtraFiles len = %d, want 1", len(cmd1.ExtraFiles))
	}
	closeSeccompReadEnd(cmd1)

	// With two pre-existing ExtraFiles the fd must be 5.
	f1, f2 := os.Stdin, os.Stdout // any open *os.File placeholders
	cmd2 := &exec.Cmd{ExtraFiles: []*os.File{f1, f2}}
	fdNum2, err := prepareSeccompFd(cmd2)
	if err != nil {
		t.Fatalf("prepareSeccompFd() error = %v", err)
	}
	if fdNum2 != 5 {
		t.Fatalf("fdNum = %d, want 5", fdNum2)
	}
	if len(cmd2.ExtraFiles) != 3 {
		t.Fatalf("ExtraFiles len = %d, want 3", len(cmd2.ExtraFiles))
	}
	closeSeccompReadEnd(cmd2)
}

func TestPrepareSeccompFd_CleanupIdempotent(t *testing.T) {
	insns := buildSeccompFilter()
	if insns == nil {
		t.Skip("no seccomp filter defined for this architecture")
	}
	cmd := &exec.Cmd{}
	if _, err := prepareSeccompFd(cmd); err != nil {
		t.Fatalf("prepareSeccompFd() error = %v", err)
	}
	// Calling close twice must not panic.
	closeSeccompReadEnd(cmd)
	closeSeccompReadEnd(cmd) // idempotent
}

// TestBuildLinuxBwrapArgs_UnshareUTS verifies that --unshare-uts always
// appears in the generated bwrap argument list.
func TestBuildLinuxBwrapArgs_UnshareUTS(t *testing.T) {
	root := t.TempDir()
	binaryPath := root + "/tool"
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	args, err := buildLinuxBwrapArgs(binaryPath, binaryPath, []string{binaryPath}, "", nil, nil)
	if err != nil {
		t.Fatalf("buildLinuxBwrapArgs() error = %v", err)
	}
	for _, arg := range args {
		if arg == "--unshare-uts" {
			return
		}
	}
	t.Fatalf("--unshare-uts not found in bwrap args: %v", args)
}

// TestSeccompFilterActiveInSandbox is a behavioral integration test.
// It runs a command inside the bubblewrap sandbox (via PrepareCommand) and
// checks that the kernel reports Seccomp mode 2 (BPF filter), proving the
// program was installed. It is skipped when bwrap is unavailable or when the
// kernel cannot create namespaces (plain Docker runner without seccomp=unconfined).
func TestSeccompFilterActiveInSandbox(t *testing.T) {
	insns := buildSeccompFilter()
	if insns == nil {
		t.Skip("no seccomp filter defined for this architecture")
	}

	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap not in PATH")
	}

	root := t.TempDir()
	t.Setenv("PICOCLAW_HOME", root)
	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	Configure(cfg)
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	prepErr := PrepareInstanceRoot(root)
	if prepErr != nil {
		t.Fatal(prepErr)
	}

	// Quick capability probe: verify bwrap can run at all in this environment.
	probeErr := exec.Command(bwrapPath,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--", "true",
	).Run()
	if probeErr != nil {
		t.Skipf("bwrap probe failed (%v); kernel may not allow user namespaces in this environment", probeErr)
	}

	// Run a real command through the full isolation path so prepareSeccompFd
	// installs the filter and bwrap applies it to the child.
	cmd := exec.Command("/bin/sh", "-c", "grep '^Seccomp:' /proc/self/status")
	prepErr = PrepareCommand(cmd)
	if prepErr != nil {
		t.Fatalf("PrepareCommand() error = %v", prepErr)
	}
	out, runErr := cmd.Output()
	closeSeccompReadEnd(cmd) // idempotent: ensure fd is released
	if runErr != nil {
		t.Skipf("sandbox command failed (%v); likely missing capability or /proc is masked", runErr)
	}
	line := strings.TrimSpace(string(out))
	// Seccomp: 2 means BPF-filter mode; 0 = disabled, 1 = strict.
	if !strings.HasSuffix(line, "2") {
		t.Fatalf("Seccomp status = %q, want 'Seccomp: 2'", line)
	}
}
