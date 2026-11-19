//go:build linux

package isolation

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// resetNamespaceProbeCache clears the per-path cache at the start of a test and
// restores it via t.Cleanup so tests that manipulate the cache do not affect
// each other.
func resetNamespaceProbeCache(t *testing.T) {
	t.Helper()
	do := func() {
		linuxNamespaceProbe.mu.Lock()
		linuxNamespaceProbe.results = nil
		linuxNamespaceProbe.warned = nil
		linuxNamespaceProbe.mu.Unlock()
	}
	do()
	t.Cleanup(do)
}

// TestIsNamespaceUnsupported covers the EINVAL/EPERM classifier with the real
// bwrap stderr text formats observed in the lab and derived from the bwrap
// source format "Creating new namespace failed: <strerror(errno)>".
func TestIsNamespaceUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		want   bool
	}{
		// EINVAL: kernel does not support the namespace type.
		// Fixture: bwrap source → die_with_error("Creating new namespace failed")
		// → strerror(EINVAL) == "Invalid argument".
		{
			name:   "einval_full_bwrap_prefix",
			stderr: "bwrap: Creating new namespace failed: Invalid argument",
			want:   true,
		},
		{
			name:   "einval_no_bwrap_prefix",
			stderr: "Creating new namespace failed: Invalid argument",
			want:   true,
		},
		{
			name:   "einval_trailing_newline",
			stderr: "bwrap: Creating new namespace failed: Invalid argument\n",
			want:   true,
		},

		// EPERM: seccomp, AppArmor, or missing capabilities blocked the syscall.
		// strerror(EPERM) == "Operation not permitted".
		// Observed in Docker default-seccomp + root containers.
		{
			name:   "eperm_operation_not_permitted",
			stderr: "bwrap: Creating new namespace failed: Operation not permitted",
			want:   false,
		},
		{
			name:   "eperm_operation_not_permitted_trailing_newline",
			stderr: "bwrap: Creating new namespace failed: Operation not permitted\n",
			want:   false,
		},
		// Alternative message seen in some bwrap versions / seccomp profiles
		// (Docker default seccomp, non-root).
		{
			name:   "eperm_no_permissions_variant",
			stderr: "bwrap: No permissions to create a new namespace",
			want:   false,
		},

		// Other bwrap failures that must not be silently dropped.
		{
			name:   "setup_failure",
			stderr: "bwrap: setting up environment failed",
			want:   false,
		},
		{
			name:   "empty_stderr",
			stderr: "",
			want:   false,
		},
		{
			name:   "unrelated_text",
			stderr: "something completely different",
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNamespaceUnsupported(tc.stderr); got != tc.want {
				t.Fatalf("isNamespaceUnsupported(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// TestCachedLinuxNamespaceFlags_CachesSuccessfulProbe verifies that a
// successful probe result is cached: the second call for the same bwrap path
// returns without invoking the runner again.
func TestCachedLinuxNamespaceFlags_CachesSuccessfulProbe(t *testing.T) {
	resetNamespaceProbeCache(t)
	old := linuxNamespaceProbeRun
	t.Cleanup(func() { linuxNamespaceProbeRun = old })

	var calls atomic.Int64
	// Simulate EINVAL → --unshare-ipc unsupported but probe succeeds (no error).
	linuxNamespaceProbeRun = func(_ string, _ ...string) ([]byte, error) {
		calls.Add(1)
		return []byte("bwrap: Creating new namespace failed: Invalid argument"),
			errors.New("exit status 1")
	}

	flags1, err1 := cachedLinuxNamespaceFlags("/fake/bwrap")
	if err1 != nil {
		t.Fatalf("first call: unexpected error: %v", err1)
	}
	flags2, err2 := cachedLinuxNamespaceFlags("/fake/bwrap")
	if err2 != nil {
		t.Fatalf("second call: unexpected error: %v", err2)
	}
	// Runner should only have been called for the first invocation.
	want := int64(len(linuxOptionalNamespaceFlags))
	if got := calls.Load(); got != want {
		t.Fatalf("probe runner called %d times, want %d (cached after first success)", got, want)
	}
	// EINVAL → flag should be marked as not supported.
	if flags1["--unshare-ipc"] || flags2["--unshare-ipc"] {
		t.Fatalf("flags1[--unshare-ipc]=%v flags2[--unshare-ipc]=%v, want false (EINVAL → unsupported)",
			flags1["--unshare-ipc"], flags2["--unshare-ipc"])
	}
}

// TestCachedLinuxNamespaceFlags_ErrorNotCached verifies that a probe error
// (EPERM or other non-EINVAL failure) is not stored in the cache so the probe
// is retried on the next call, allowing recovery when the environment is fixed.
func TestCachedLinuxNamespaceFlags_ErrorNotCached(t *testing.T) {
	resetNamespaceProbeCache(t)
	old := linuxNamespaceProbeRun
	t.Cleanup(func() { linuxNamespaceProbeRun = old })

	var calls atomic.Int64
	// Simulate EPERM → namespaceProbeError returned, not classified as unsupported.
	linuxNamespaceProbeRun = func(_ string, _ ...string) ([]byte, error) {
		calls.Add(1)
		return []byte("bwrap: Creating new namespace failed: Operation not permitted"),
			errors.New("exit status 1")
	}

	_, err1 := cachedLinuxNamespaceFlags("/fake/bwrap")
	if err1 == nil {
		t.Fatal("first call: expected error for EPERM, got nil")
	}
	_, err2 := cachedLinuxNamespaceFlags("/fake/bwrap")
	if err2 == nil {
		t.Fatal("second call: expected error not cached, got nil")
	}
	if calls.Load() < 2 {
		t.Fatalf("error was cached: runner called %d times, want ≥2", calls.Load())
	}
}

// TestCachedLinuxNamespaceFlags_DifferentPathReprobes verifies that a different
// bwrap binary path gets its own probe run, not a cached result from a prior path.
func TestCachedLinuxNamespaceFlags_DifferentPathReprobes(t *testing.T) {
	resetNamespaceProbeCache(t)
	old := linuxNamespaceProbeRun
	t.Cleanup(func() { linuxNamespaceProbeRun = old })

	var calls atomic.Int64
	linuxNamespaceProbeRun = func(_ string, _ ...string) ([]byte, error) {
		calls.Add(1)
		return nil, nil // success, all flags supported
	}

	if _, err := cachedLinuxNamespaceFlags("/path/a/bwrap"); err != nil {
		t.Fatalf("path a: unexpected error: %v", err)
	}
	callsAfterA := calls.Load()

	if _, err := cachedLinuxNamespaceFlags("/path/b/bwrap"); err != nil {
		t.Fatalf("path b: unexpected error: %v", err)
	}
	callsAfterB := calls.Load()

	if callsAfterB <= callsAfterA {
		t.Fatalf("different bwrap path not re-probed: calls after path/a=%d, after path/b=%d",
			callsAfterA, callsAfterB)
	}
}

// TestNamespaceProbeError_WrapsOriginalError verifies that the error returned
// for a non-EINVAL probe failure wraps the original cause so errors.Is works.
func TestNamespaceProbeError_WrapsOriginalError(t *testing.T) {
	cause := errors.New("exit status 1")
	err := namespaceProbeError("--unshare-ipc",
		"bwrap: Creating new namespace failed: Operation not permitted", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("namespaceProbeError did not wrap cause: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"--unshare-ipc",
		"Operation not permitted",
		"isolation.enabled",
		"seccomp",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("namespaceProbeError message missing %q: %s", want, msg)
		}
	}
}
