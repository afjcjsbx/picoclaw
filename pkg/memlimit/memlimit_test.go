package memlimit

import (
	"runtime/debug"
	"testing"
)

func TestMemoryLimitBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, false},
		{"128", 128 << 20, true},
		{"0", 0, false},
		{"-4", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		got, ok := memoryLimitBytes(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("memoryLimitBytes(%q) = (%d,%v), want (%d,%v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGCPercent(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"", 0, false},
		{"100", 100, true},
		{"0", 0, true},
		{"-1", -1, true},
		{"off", -1, true},
		{"x", 0, false},
	}
	for _, tt := range tests {
		got, ok := gcPercent(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("gcPercent(%q) = (%d,%v), want (%d,%v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestApply_DoesNotPanic(t *testing.T) {
	// Apply mutates process-wide runtime settings that t.Setenv does not
	// restore, so put them back once the test is done.
	prevLimit := debug.SetMemoryLimit(-1) // a negative input only reads the limit
	prevGC := debug.SetGCPercent(100)
	debug.SetGCPercent(prevGC)
	t.Cleanup(func() {
		debug.SetMemoryLimit(prevLimit)
		debug.SetGCPercent(prevGC)
	})

	for _, tt := range []struct {
		limit string
		gogc  string
	}{
		{"", ""},
		{"64", "200"},
		{"bogus", "bogus"},
	} {
		t.Setenv(EnvMemoryLimitMB, tt.limit)
		t.Setenv(EnvGOGC, tt.gogc)
		Apply() // must not panic on unset or invalid values
	}
}

func TestReleaseToOS_Threshold(t *testing.T) {
	// Below the threshold this is a no-op; above it must not panic.
	ReleaseToOS(0)
	ReleaseToOS(releaseThresholdBytes - 1)
	ReleaseToOS(releaseThresholdBytes + 1)
}
