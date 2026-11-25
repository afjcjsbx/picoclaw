package config

import "testing"

func TestAgentDefaultsBootstrapLimits(t *testing.T) {
	var zero AgentDefaults
	if got := zero.GetMaxBootstrapChars(); got != DefaultMaxBootstrapChars {
		t.Fatalf("default per-file cap = %d, want %d", got, DefaultMaxBootstrapChars)
	}
	if got := zero.GetMaxBootstrapTotalChars(); got != DefaultMaxBootstrapTotalChars {
		t.Fatalf("default total cap = %d, want %d", got, DefaultMaxBootstrapTotalChars)
	}

	zero.MaxBootstrapChars = 1234
	zero.MaxBootstrapTotalChars = 4321
	if got := zero.GetMaxBootstrapChars(); got != 1234 {
		t.Fatalf("configured per-file cap = %d, want 1234", got)
	}
	if got := zero.GetMaxBootstrapTotalChars(); got != 4321 {
		t.Fatalf("configured total cap = %d, want 4321", got)
	}

	zero.MaxBootstrapChars = -1
	zero.MaxBootstrapTotalChars = -1
	if got := zero.GetMaxBootstrapChars(); got != DefaultMaxBootstrapChars {
		t.Fatalf("negative per-file cap = %d, want default", got)
	}
	if got := zero.GetMaxBootstrapTotalChars(); got != DefaultMaxBootstrapTotalChars {
		t.Fatalf("negative total cap = %d, want default", got)
	}
}
