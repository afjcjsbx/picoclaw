//go:build linux

package isolation

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"golang.org/x/sys/unix"
)

// linuxPendingSeccompFds tracks the read end of a seccomp pipe per command.
// The parent closes it once the child has inherited the fd; until then bwrap
// cannot exec.
var linuxPendingSeccompFds sync.Map // *exec.Cmd -> *os.File

// prepareSeccompFd builds the BPF seccomp denylist for the current
// architecture, writes it to a pipe, appends the read end to cmd.ExtraFiles,
// and returns the fd number bwrap should receive via --seccomp.
// Returns -1 (no error) when this architecture has no defined syscall table.
func prepareSeccompFd(cmd *exec.Cmd) (int, error) {
	insns := buildSeccompFilter()
	if insns == nil {
		// Architecture not covered; filter intentionally absent on this arch.
		return -1, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		return 0, fmt.Errorf("create seccomp pipe: %w", err)
	}

	// Write the raw sock_filter instructions to the write end then close it.
	// bwrap reads the whole fd and divides the byte count by 8 to get the
	// instruction count.
	if err := writeSockFilters(w, insns); err != nil {
		_ = r.Close()
		_ = w.Close()
		return 0, fmt.Errorf("write seccomp filter: %w", err)
	}
	_ = w.Close()

	// fd 0/1/2 are stdio; ExtraFiles[i] becomes fd 3+i in the child.
	fdNum := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, r)
	linuxPendingSeccompFds.Store(cmd, r)
	return fdNum, nil
}

// closeSeccompReadEnd closes the read half of the seccomp pipe that was kept
// open in the parent. It must be called after cmd.Start() regardless of
// whether the start succeeded.
func closeSeccompReadEnd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if v, loaded := linuxPendingSeccompFds.LoadAndDelete(cmd); loaded {
		if f, ok := v.(*os.File); ok {
			_ = f.Close()
		}
	}
}

// writeSockFilters serializes insns as raw sock_filter bytes (8 bytes each,
// little-endian: code[2] | jt[1] | jf[1] | k[4]).
func writeSockFilters(w io.Writer, insns []unix.SockFilter) error {
	buf := make([]byte, 8*len(insns))
	for i, insn := range insns {
		binary.LittleEndian.PutUint16(buf[8*i:], insn.Code)
		buf[8*i+2] = insn.Jt
		buf[8*i+3] = insn.Jf
		binary.LittleEndian.PutUint32(buf[8*i+4:], insn.K)
	}
	_, err := w.Write(buf)
	return err
}

// bpfStmt returns a BPF statement instruction (no jump, jt=jf=0).
func bpfStmt(code uint16, k uint32) unix.SockFilter {
	return unix.SockFilter{Code: code, K: k}
}

// bpfJump returns a BPF conditional jump instruction.
func bpfJump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}

// SECCOMP return values.
const (
	seccompRetAllow     = uint32(0x7fff0000) // SECCOMP_RET_ALLOW
	seccompRetErrno     = uint32(0x00050000) // SECCOMP_RET_ERRNO base
	seccompEPERM        = uint32(1)
	seccompRetDenyEPERM = seccompRetErrno | seccompEPERM

	// x32SyscallBit is set on syscall numbers when user space uses the x32 ABI
	// on amd64. Requests with this bit set bypass the amd64 denylist unless we
	// reject them explicitly.
	x32SyscallBit = uint32(0x40000000)

	// offsets in struct seccomp_data
	seccompDataOffNr   = uint32(0) // __s32 nr
	seccompDataOffArch = uint32(4) // __u32 arch
)

// buildBPFDenylist assembles a classic-BPF seccomp program that:
//   - rejects requests from a different audit architecture;
//   - on amd64, rejects any syscall number with the x32 ABI bit set;
//   - returns SECCOMP_RET_ERRNO|EPERM for every syscall in denied;
//   - otherwise returns SECCOMP_RET_ALLOW.
//
// Program layout (amd64, N denied syscalls):
//
//	0       LD   [4]              load arch
//	1       JEQ  auditArch        jt=0 (continue), jf=to DENY
//	2       LD   [0]              load nr (also used by x32 JSET below)
//	3       JSET x32SyscallBit   jt=to DENY, jf=0            [amd64 only]
//	4..N+3  JEQ  nr_i             jt=to DENY, jf=0
//	N+4     RET  ALLOW
//	N+5     RET  ERRNO|EPERM      (DENY label)
func buildBPFDenylist(auditArch uint32, hasX32 bool, denied []uint32) []unix.SockFilter {
	insns := make([]unix.SockFilter, 0, len(denied)+6)

	// Step 1: load and check audit arch.
	insns = append(insns,
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, seccompDataOffArch),
	)
	archJumpIdx := len(insns)
	insns = append(insns,
		bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, auditArch, 0, 0), // jf backfilled
	)

	// Step 2: load syscall number; this also feeds the x32 JSET on amd64.
	insns = append(insns,
		bpfStmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, seccompDataOffNr),
	)

	// Step 3 (amd64 only): reject x32 ABI syscalls.
	x32JumpIdx := -1
	if hasX32 {
		x32JumpIdx = len(insns)
		insns = append(insns,
			bpfJump(unix.BPF_JMP|unix.BPF_JSET|unix.BPF_K, x32SyscallBit, 0, 0), // jt backfilled
		)
	}

	// Step 4: one JEQ per denied syscall.
	syscallStart := len(insns)
	for range denied {
		insns = append(insns,
			bpfJump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, 0, 0, 0), // all backfilled
		)
	}

	// Step 5: allow / deny terminators.
	insns = append(insns, bpfStmt(unix.BPF_RET|unix.BPF_K, seccompRetAllow))
	denyIdx := len(insns)
	insns = append(insns, bpfStmt(unix.BPF_RET|unix.BPF_K, seccompRetDenyEPERM))

	// Backfill jump offsets. jt/jf are forward distances: "skip N instructions"
	// (so jt=0 means "fall through to the very next instruction").
	insns[archJumpIdx].Jf = uint8(denyIdx - archJumpIdx - 1)
	if hasX32 {
		insns[x32JumpIdx].Jt = uint8(denyIdx - x32JumpIdx - 1)
	}
	for i, nr := range denied {
		idx := syscallStart + i
		insns[idx].K = nr
		insns[idx].Jt = uint8(denyIdx - idx - 1)
	}

	return insns
}
