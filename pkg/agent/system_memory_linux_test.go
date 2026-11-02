//go:build linux || android

package agent

import "testing"

// totalSystemMemory must never return 0 on a running Linux host. A zero result
// means the sysinfo multiplication went wrong: either the field widening is
// incorrect on this architecture, or mem_unit was 0 and treated as 0 instead
// of 1. Both cases silently drop the RAM line from the runtime prompt.
func TestTotalSystemMemoryDoesNotReturnZero(t *testing.T) {
	ram := totalSystemMemory()
	if ram == 0 {
		t.Fatal("totalSystemMemory() returned 0, expected the host memory size")
	}

	// Guard against an obviously wrong multiplication (e.g. losing the unit
	// factor would report a few KiB of RAM instead of GiB).
	const oneMiB = 1 << 20
	if ram < oneMiB {
		t.Fatalf("totalSystemMemory() = %d bytes, implausibly small", ram)
	}
}
