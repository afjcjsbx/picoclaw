//go:build windows

package agent

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

type memoryStatusEx struct {
	size                    uint32
	memoryLoad              uint32
	totalPhysicalMemory     uint64
	availablePhysicalMemory uint64
	totalPageFile           uint64
	availablePageFile       uint64
	totalVirtual            uint64
	availableVirtual        uint64
	availableExtended       uint64
}

func totalSystemMemory() uint64 {
	status := memoryStatusEx{size: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	result, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0
	}
	return status.totalPhysicalMemory
}
