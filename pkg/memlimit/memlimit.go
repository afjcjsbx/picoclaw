// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package memlimit exposes helpers for keeping PicoClaw's resident memory
// footprint in check on constrained devices.
package memlimit

import (
	"os"
	"runtime/debug"
	"strconv"

	"github.com/afjcjsbx/picoclaw/pkg/logger"
)

// Environment variables understood by Apply.
const (
	// EnvMemoryLimitMB sets a soft memory limit for the Go runtime (equivalent
	// to GOMEMLIMIT but in megabytes, easier to reason about on small devices).
	// When the process approaches the limit the GC runs more aggressively
	// instead of growing the heap further.
	EnvMemoryLimitMB = "PICOCLAW_MEMORY_LIMIT_MB"

	// EnvGOGC sets the GC target percentage (equivalent to GOGC).
	EnvGOGC = "PICOCLAW_GOGC"
)

// Apply configures the Go runtime from PICOCLAW_MEMORY_LIMIT_MB and
// PICOCLAW_GOGC when set. It is a no-op for unset or invalid variables, so the
// runtime defaults (or Go's own GOMEMLIMIT/GOGC handling) remain in effect.
func Apply() {
	if bytes, ok := memoryLimitBytes(os.Getenv(EnvMemoryLimitMB)); ok {
		debug.SetMemoryLimit(bytes)
	} else if v := os.Getenv(EnvMemoryLimitMB); v != "" {
		logger.Warnf("memlimit: ignoring invalid %s=%q", EnvMemoryLimitMB, v)
	}
	if pct, ok := gcPercent(os.Getenv(EnvGOGC)); ok {
		debug.SetGCPercent(pct)
	} else if v := os.Getenv(EnvGOGC); v != "" {
		logger.Warnf("memlimit: ignoring invalid %s=%q", EnvGOGC, v)
	}
}

// memoryLimitBytes parses a megabyte value into bytes. Empty or invalid values
// report ok=false.
func memoryLimitBytes(v string) (int64, bool) {
	if v == "" {
		return 0, false
	}
	mb, err := strconv.Atoi(v)
	if err != nil || mb <= 0 {
		return 0, false
	}
	return int64(mb) << 20, true
}

// gcPercent parses a GOGC-style percentage. Empty or invalid values report
// ok=false. A negative value or "off" disables the GC (typically paired with a
// memory limit); 0 is valid but does not disable it: it makes the collector run
// almost continuously.
func gcPercent(v string) (int, bool) {
	if v == "" {
		return 0, false
	}
	if v == "off" {
		return -1, true
	}
	pct, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return pct, true
}

// releaseThresholdBytes is the amount of just-freed heap above which
// ReleaseToOS actually asks the runtime to return memory to the OS. Below it
// the syscall/GC overhead is not worth it.
const releaseThresholdBytes = 8 << 20 // 8 MiB

// ReleaseToOS returns freed heap memory to the operating system, but only when
// recentlyFreed indicates a large transient allocation (at least
// releaseThresholdBytes). This is meant to be called after short-lived
// operations that allocate tens of megabytes — such as decoding and encoding a
// large image — so that the resident set does not stay inflated until the next
// scheduled GC.
//
// It is a no-op for small values, so callers need not gate the call themselves.
func ReleaseToOS(recentlyFreed int) {
	if recentlyFreed < releaseThresholdBytes {
		return
	}
	debug.FreeOSMemory()
}
