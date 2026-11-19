//go:build linux && amd64

package isolation

import "golang.org/x/sys/unix"

// auditArchAMD64 is AUDIT_ARCH_X86_64: EM_X86_64 | __AUDIT_ARCH_64BIT | __AUDIT_ARCH_LE.
const auditArchAMD64 = uint32(0xc000003e)

// buildSeccompFilter returns the seccomp BPF denylist for amd64.
// The x32 ABI guard (0x40000000 bit) is enabled so the denylist cannot be
// bypassed by calling the same syscall through the x32 ABI.
func buildSeccompFilter() []unix.SockFilter {
	return buildBPFDenylist(auditArchAMD64, true, seccompDeniedSyscallsAMD64)
}

// seccompDeniedSyscallsAMD64 is the conservative denylist for x86-64.
// Normal agent workloads (compilation, scripting, network tools, git) do not
// need any of these. ptrace and clone/unshare are intentionally omitted so
// debuggers and browser-style nested user-namespace sandboxes keep working.
var seccompDeniedSyscallsAMD64 = []uint32{
	unix.SYS_KEXEC_LOAD,        // load a new kernel image
	unix.SYS_KEXEC_FILE_LOAD,   // load a new kernel image (fd variant)
	unix.SYS_INIT_MODULE,       // load a kernel module from memory
	unix.SYS_FINIT_MODULE,      // load a kernel module from fd
	unix.SYS_DELETE_MODULE,     // unload a kernel module
	unix.SYS_BPF,               // create/manipulate BPF programs and maps
	unix.SYS_PERF_EVENT_OPEN,   // performance-monitoring; also a container-escape vector
	unix.SYS_USERFAULTFD,       // user-space page-fault handling
	unix.SYS_KEYCTL,            // kernel key-ring management
	unix.SYS_ADD_KEY,           // add a key to the kernel key ring
	unix.SYS_REQUEST_KEY,       // search for and instantiate a kernel key
	unix.SYS_PROCESS_VM_READV,  // cross-process memory read
	unix.SYS_PROCESS_VM_WRITEV, // cross-process memory write
	// Legacy and new-mount-API calls; the sandbox already cannot hold
	// privileges, but deny these explicitly as an extra layer.
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
	unix.SYS_SETNS,             // switch namespace (escapes the UTS/IPC namespaces we set)
	unix.SYS_REBOOT,            // reboot or kexec
	unix.SYS_SWAPON,            // enable swap device/file
	unix.SYS_SWAPOFF,           // disable swap device/file
	unix.SYS_ACCT,              // process accounting
	unix.SYS_SETTIMEOFDAY,      // set system clock
	unix.SYS_CLOCK_SETTIME,     // set a POSIX clock
	unix.SYS_CLOCK_ADJTIME,     // adjust a POSIX clock
	unix.SYS_ADJTIMEX,          // tune kernel clock
	unix.SYS_IOPERM,            // set I/O port permissions (x86 only)
	unix.SYS_IOPL,              // change I/O privilege level (x86 only)
	unix.SYS_QUOTACTL,          // manipulate disk quotas
	unix.SYS_IO_URING_SETUP,    // set up io_uring
	unix.SYS_IO_URING_ENTER,    // initiate/complete io_uring operations
	unix.SYS_IO_URING_REGISTER, // register resources for io_uring
}
