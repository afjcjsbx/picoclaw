//go:build !linux && !android && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package agent

func totalSystemMemory() uint64 { return 0 }
