//go:build linux

package isolation

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseBwrapVersionString covers the range of real and synthetic --version
// outputs including distro suffixes, garbage lines, and empty input.
func TestParseBwrapVersionString(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantOK bool
		want   bwrapSemver
	}{
		{
			name:   "clean release",
			input:  "bubblewrap 0.13.1\n",
			wantOK: true,
			want:   bwrapSemver{0, 13, 1},
		},
		{
			name:   "distro suffix",
			input:  "bubblewrap 0.9.0-1ubuntu1\n",
			wantOK: true,
			want:   bwrapSemver{0, 9, 0},
		},
		{
			name:   "rc suffix",
			input:  "bubblewrap 0.12.0-rc1\n",
			wantOK: true,
			want:   bwrapSemver{0, 12, 0},
		},
		{
			name:   "plus suffix",
			input:  "bubblewrap 0.11.2+ds\n",
			wantOK: true,
			want:   bwrapSemver{0, 11, 2},
		},
		{
			name:   "extra lines before",
			input:  "some preamble\nbubblewrap 0.8.0\n",
			wantOK: true,
			want:   bwrapSemver{0, 8, 0},
		},
		{
			name:   "case insensitive keyword",
			input:  "BubbleWrap 0.6.3\n",
			wantOK: true,
			want:   bwrapSemver{0, 6, 3},
		},
		{
			name:   "version 0.3.3",
			input:  "bubblewrap 0.3.3\n",
			wantOK: true,
			want:   bwrapSemver{0, 3, 3},
		},
		{
			name:   "version 0.2.1",
			input:  "bubblewrap 0.2.1\n",
			wantOK: true,
			want:   bwrapSemver{0, 2, 1},
		},
		{
			name:   "version 0.1.0",
			input:  "bubblewrap 0.1.0\n",
			wantOK: true,
			want:   bwrapSemver{0, 1, 0},
		},
		{
			name:   "garbage line",
			input:  "this is not a version string\n",
			wantOK: false,
		},
		{
			name:   "empty",
			input:  "",
			wantOK: false,
		},
		{
			name:   "only newlines",
			input:  "\n\n\n",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseBwrapVersionString(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("parseBwrapVersionString() ok = %v, want %v (input %q)", ok, tc.wantOK, tc.input)
			}
			if ok && got != tc.want {
				t.Fatalf("parseBwrapVersionString() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestBwrapVersionPolicy verifies the decision table: which versions produce a
// hard error, which are accepted with a CVE warning, and which are silently ok.
// It drives applyBwrapVersionPolicy directly so the test does not depend on
// real bwrap binaries or the cache.
func TestBwrapVersionPolicy(t *testing.T) {
	cases := []struct {
		version     string
		ver         bwrapSemver
		wantErr     bool
		errContains string // substring that must appear in the error message
	}{
		{
			version: "0.1.0", ver: bwrapSemver{0, 1, 0},
			wantErr: true, errContains: "too old",
		},
		{
			version: "0.2.1", ver: bwrapSemver{0, 2, 1},
			wantErr: true, errContains: "too old",
		},
		{
			// 0.3.3 is the minimum working version; must succeed.
			version: "0.3.3", ver: bwrapSemver{0, 3, 3},
			wantErr: false,
		},
		{
			version: "0.8.0", ver: bwrapSemver{0, 8, 0},
			wantErr: false,
		},
		{
			version: "0.11.2", ver: bwrapSemver{0, 11, 2},
			wantErr: false,
		},
		{
			// 0.12.0 fixes CVE-2026-87766; no warning expected.
			version: "0.12.0", ver: bwrapSemver{0, 12, 0},
			wantErr: false,
		},
		{
			version: "0.13.1", ver: bwrapSemver{0, 13, 1},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run("v"+tc.version, func(t *testing.T) {
			err := applyBwrapVersionPolicy(tc.ver)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("applyBwrapVersionPolicy(%s) = nil, want error", tc.version)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("applyBwrapVersionPolicy(%s) error = %q, want to contain %q",
						tc.version, err.Error(), tc.errContains)
				}
			} else {
				if err != nil {
					t.Fatalf("applyBwrapVersionPolicy(%s) = %v, want nil", tc.version, err)
				}
			}
		})
	}
}

// TestCheckBwrapVersion_RunnerErrors covers the two soft-failure paths: a
// transient run error and unparseable output. Neither should return an error.
func TestCheckBwrapVersion_RunnerErrors(t *testing.T) {
	t.Run("transient run failure", func(t *testing.T) {
		path := "/tmp/fake-bwrap-transient"
		// Clear any cached entry.
		bwrapVersionMu.Lock()
		delete(bwrapVersionCache, path)
		bwrapVersionMu.Unlock()

		err := checkBwrapVersion(path, func(string, ...string) ([]byte, error) {
			return nil, errors.New("exec: not found")
		})
		if err != nil {
			t.Fatalf("transient run error should not be a hard error; got %v", err)
		}
		// Must not be cached after a run failure.
		bwrapVersionMu.Lock()
		_, cached := bwrapVersionCache[path]
		bwrapVersionMu.Unlock()
		if cached {
			t.Fatal("transient run failure must not be stored in the version cache")
		}
	})

	t.Run("unparseable output", func(t *testing.T) {
		path := "/tmp/fake-bwrap-garbage"
		bwrapVersionMu.Lock()
		delete(bwrapVersionCache, path)
		bwrapVersionMu.Unlock()

		err := checkBwrapVersion(path, func(string, ...string) ([]byte, error) {
			return []byte("some garbage output\n"), nil
		})
		if err != nil {
			t.Fatalf("unparseable version output should not be a hard error; got %v", err)
		}
	})
}

// TestCheckBwrapVersion_CachesSuccess verifies that a successfully parsed
// version is cached so the runner is not called again for the same path.
func TestCheckBwrapVersion_CachesSuccess(t *testing.T) {
	path := "/tmp/fake-bwrap-cached"
	bwrapVersionMu.Lock()
	delete(bwrapVersionCache, path)
	bwrapVersionMu.Unlock()

	calls := 0
	runner := func(string, ...string) ([]byte, error) {
		calls++
		return []byte("bubblewrap 0.13.1\n"), nil
	}

	for i := range 3 {
		if err := checkBwrapVersion(path, runner); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("runner called %d times, want exactly 1 (cached after first success)", calls)
	}
}

// TestCheckBwrapVersion_OldVersionHardError confirms that a version below 0.3.3
// is refused with a message that names the found version and the minimum.
func TestCheckBwrapVersion_OldVersionHardError(t *testing.T) {
	path := "/tmp/fake-bwrap-old"
	bwrapVersionMu.Lock()
	delete(bwrapVersionCache, path)
	bwrapVersionMu.Unlock()

	err := checkBwrapVersion(path, func(string, ...string) ([]byte, error) {
		return []byte("bubblewrap 0.2.1\n"), nil
	})
	if err == nil {
		t.Fatal("checkBwrapVersion(0.2.1) = nil, want error")
	}
	msg := err.Error()
	for _, want := range []string{"0.2.1", "0.3.3", "Unknown option --"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// TestCheckBwrapVersion_SetuidRefused verifies that a setuid binary is refused
// before --version is even called. It requires the ability to set the setuid
// bit (root or suitable capabilities) and is skipped otherwise.
func TestCheckBwrapVersion_SetuidRefused(t *testing.T) {
	dir := t.TempDir()
	bwrapPath := filepath.Join(dir, "fake-bwrap")
	if err := os.WriteFile(bwrapPath, []byte("#!/bin/sh\necho 'bubblewrap 0.13.1'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// os.ModeSetuid is Go's abstract setuid flag (1<<23), not Unix 0o4000.
	// Passing 0o4755 to os.Chmod does not set setuid; use os.ModeSetuid|0o755.
	if err := os.Chmod(bwrapPath, os.ModeSetuid|0o755); err != nil {
		t.Skipf("cannot set setuid bit (not running as root?): %v", err)
	}
	info, err := os.Stat(bwrapPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetuid == 0 {
		t.Skip("setuid bit not reflected by kernel/fs after chmod; skipping")
	}

	var called bool
	err = checkBwrapVersion(bwrapPath, func(string, ...string) ([]byte, error) {
		called = true
		return []byte("bubblewrap 0.13.1\n"), nil
	})
	if err == nil {
		t.Fatal("checkBwrapVersion with setuid bwrap: got nil, want error")
	}
	if called {
		t.Error("checkBwrapVersion ran --version on setuid binary; should have failed before that")
	}
	if !strings.Contains(err.Error(), "setuid") {
		t.Errorf("error message should mention setuid; got: %s", err.Error())
	}
}

// TestBwrapVersionGate_LabBinaries is a lab-only behavioral test that uses the
// real bwrap binaries installed under /opt/bwrap/vX.Y.Z/bwrap in the
// picoclaw-bwrap-lab Docker image. It is skipped automatically when those paths
// are absent (e.g. in normal CI).
func TestBwrapVersionGate_LabBinaries(t *testing.T) {
	labCases := []struct {
		version string
		wantErr bool
	}{
		{"0.2.1", true},  // too old: lacks '--' separator
		{"0.3.3", false}, // minimum working version; CVE-2026-87766 warn fired
		{"0.12.0", false},
		{"0.13.1", false},
	}

	realRunner := func(path string, args ...string) ([]byte, error) {
		return exec.Command(path, args...).Output()
	}

	for _, tc := range labCases {
		t.Run("v"+tc.version, func(t *testing.T) {
			bwrapPath := "/opt/bwrap/v" + tc.version + "/bwrap"
			if _, err := os.Stat(bwrapPath); err != nil {
				t.Skipf("lab binary %s not found (not running in picoclaw-bwrap-lab): %v", bwrapPath, err)
			}
			// Clear the cache so each subtest is independent.
			bwrapVersionMu.Lock()
			delete(bwrapVersionCache, bwrapPath)
			bwrapVersionMu.Unlock()

			err := checkBwrapVersion(bwrapPath, realRunner)
			if tc.wantErr && err == nil {
				t.Errorf("checkBwrapVersion(v%s) = nil, want error", tc.version)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("checkBwrapVersion(v%s) = %v, want nil", tc.version, err)
			}
			if tc.wantErr && err != nil {
				t.Logf("v%s correctly refused: %v", tc.version, err)
			}
		})
	}
}

// TestBwrapVersionGate_SetuidLabBinary copies the real bwrap 0.13.1 binary to a
// non-nosuid path, sets the setuid bit, and verifies that checkBwrapVersion
// refuses it. Skipped when the lab binary is absent or the setuid bit cannot be
// set (non-root environment).
func TestBwrapVersionGate_SetuidLabBinary(t *testing.T) {
	src := "/opt/bwrap/v0.13.1/bwrap"
	if _, err := os.Stat(src); err != nil {
		t.Skipf("lab binary %s not found (not running in picoclaw-bwrap-lab): %v", src, err)
	}

	// /opt lives on the container overlay root filesystem, not a nosuid tmpfs.
	dst := "/opt/setuid-test-bwrap"
	t.Cleanup(func() { _ = os.Remove(dst) })

	data, readErr := os.ReadFile(src)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if writeErr := os.WriteFile(dst, data, 0o755); writeErr != nil {
		t.Skipf("cannot write to %s (not running as root?): %v", dst, writeErr)
	}
	// os.ModeSetuid is Go's abstract setuid flag (1<<23), not Unix 0o4000.
	if chmodErr := os.Chmod(dst, os.ModeSetuid|0o755); chmodErr != nil {
		t.Skipf("cannot set setuid bit (not running as root?): %v", chmodErr)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSetuid == 0 {
		t.Skip("setuid bit not reflected after chmod (nosuid filesystem?)")
	}
	t.Logf("setuid binary prepared: %s mode=%v", dst, info.Mode())

	bwrapVersionMu.Lock()
	delete(bwrapVersionCache, dst)
	bwrapVersionMu.Unlock()

	var called bool
	vErr := checkBwrapVersion(dst, func(string, ...string) ([]byte, error) {
		called = true
		return []byte("bubblewrap 0.13.1\n"), nil
	})
	if vErr == nil {
		t.Fatal("checkBwrapVersion with setuid bwrap: got nil, want error")
	}
	if called {
		t.Error("checkBwrapVersion invoked --version runner on setuid binary; should have failed before")
	}
	t.Logf("setuid bwrap correctly refused: %v", vErr)
}
