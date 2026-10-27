//go:build linux || android

package agent

import "golang.org/x/sys/unix"

func totalSystemMemory() uint64 {
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		return 0
	}
	return uint64(info.Totalram) * uint64(info.Unit)
}
