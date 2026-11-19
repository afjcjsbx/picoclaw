//go:build linux && arm64

package isolation

import "golang.org/x/sys/unix"

// auditArchARM64 is AUDIT_ARCH_AARCH64: EM_AARCH64 | __AUDIT_ARCH_64BIT | __AUDIT_ARCH_LE.
const auditArchARM64 = uint32(0xc00000b7)

// buildSeccompFilter returns the seccomp BPF denylist for arm64.
// No x32 guard: arm64 has only one syscall ABI.
func buildSeccompFilter() []unix.SockFilter {
	return buildBPFDenylist(auditArchARM64, false, seccompDeniedSyscallsARM64)
}

// seccompDeniedSyscallsARM64 mirrors the amd64 list minus the x86-specific
// ioperm/iopl calls, which do not exist on arm64.
var seccompDeniedSyscallsARM64 = []uint32{
	unix.SYS_KEXEC_LOAD,
	unix.SYS_KEXEC_FILE_LOAD,
	unix.SYS_INIT_MODULE,
	unix.SYS_FINIT_MODULE,
	unix.SYS_DELETE_MODULE,
	unix.SYS_BPF,
	unix.SYS_PERF_EVENT_OPEN,
	unix.SYS_USERFAULTFD,
	unix.SYS_KEYCTL,
	unix.SYS_ADD_KEY,
	unix.SYS_REQUEST_KEY,
	unix.SYS_PROCESS_VM_READV,
	unix.SYS_PROCESS_VM_WRITEV,
	unix.SYS_MOUNT,
	unix.SYS_UMOUNT2,
	unix.SYS_PIVOT_ROOT,
	unix.SYS_MOVE_MOUNT,
	unix.SYS_OPEN_TREE,
	unix.SYS_FSOPEN,
	unix.SYS_FSCONFIG,
	unix.SYS_FSMOUNT,
	unix.SYS_FSPICK,
	unix.SYS_MOUNT_SETATTR,
	unix.SYS_SETNS,
	unix.SYS_REBOOT,
	unix.SYS_SWAPON,
	unix.SYS_SWAPOFF,
	unix.SYS_ACCT,
	unix.SYS_SETTIMEOFDAY,
	unix.SYS_CLOCK_SETTIME,
	unix.SYS_CLOCK_ADJTIME,
	unix.SYS_ADJTIMEX,
	// SYS_IOPERM and SYS_IOPL are x86-specific and do not exist on arm64.
	unix.SYS_QUOTACTL,
	unix.SYS_IO_URING_SETUP,
	unix.SYS_IO_URING_ENTER,
	unix.SYS_IO_URING_REGISTER,
}
