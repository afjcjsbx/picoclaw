//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package agent

import (
	"runtime"

	"golang.org/x/sys/unix"
)

func totalSystemMemory() uint64 {
	name := "hw.physmem"
	if runtime.GOOS == "darwin" {
		name = "hw.memsize"
	} else if runtime.GOOS == "netbsd" {
		name = "hw.physmem64"
	}

	memory, err := unix.SysctlUint64(name)
	if err != nil {
		return 0
	}
	return memory
}
