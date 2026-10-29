//go:build linux || android

package agent

import "golang.org/x/sys/unix"

func totalSystemMemory() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}

	// Totalram is uint64 on 64-bit architectures but uint32 on 32-bit ones
	// (arm, 386, mipsle), so widen both operands before multiplying.
	//
	// Unit (mem_unit) is documented as the size in bytes of the memory unit.
	// Legacy kernels can report 0 when the value is expressed in bytes; glibc
	// treats that as 1, so do the same instead of returning 0.
	unit := uint64(info.Unit)
	if unit == 0 {
		unit = 1
	}

	return uint64(info.Totalram) * unit
}
