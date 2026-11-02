//go:build linux || android

package agent

import "golang.org/x/sys/unix"

func totalSystemMemory() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}

	// Unit (mem_unit) is the size in bytes of the memory unit. Legacy kernels
	// can report 0 when the value is already expressed in bytes; glibc treats
	// that as 1, so do the same instead of returning 0 and silently dropping
	// the RAM line from the runtime prompt.
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}

	// Totalram is uint32 on 32-bit targets (arm, 386, mipsle) and uint64 on
	// 64-bit ones, so the widening cast is required there and redundant here,
	// where the linter only ever sees the 64-bit build.
	return uint64(info.Totalram) * unit //nolint:unconvert // Required on 32-bit targets.
}
