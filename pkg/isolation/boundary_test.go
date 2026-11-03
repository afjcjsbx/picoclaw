package isolation

import (
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestDescribeBoundaryDisabled(t *testing.T) {
	if _, ok := DescribeBoundary("/tmp/workspace", config.IsolationConfig{}); ok {
		t.Fatal("DescribeBoundary() reported a boundary while isolation was disabled")
	}
}

func TestDescribeBoundaryActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	workspace := filepath.Join(home, "workspace")
	writable := filepath.Join(home, "shared")
	readOnly := filepath.Join(t.TempDir(), "toolchain")
	boundary, ok := DescribeBoundary(workspace, config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: writable, Target: writable, Mode: "rw"},
			{Source: readOnly, Target: readOnly, Mode: "ro"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}

	if boundary.Backend != backendLabel(runtime.GOOS) {
		t.Fatalf("Backend = %q, want %q", boundary.Backend, backendLabel(runtime.GOOS))
	}
	if boundary.Root != filepath.Clean(home) {
		t.Fatalf("Root = %q, want %q", boundary.Root, filepath.Clean(home))
	}
	if boundary.Workspace != filepath.Clean(workspace) {
		t.Fatalf("Workspace = %q, want %q", boundary.Workspace, filepath.Clean(workspace))
	}
	if !boundary.Network {
		t.Fatal("Network = false, want true")
	}

	if runtime.GOOS == "windows" {
		if boundary.PathBoundary {
			t.Fatal("PathBoundary = true on Windows, want false")
		}
		return
	}

	if !boundary.PathBoundary {
		t.Fatal("PathBoundary = false, want true")
	}
	if !boundary.RootWritable {
		t.Fatal("RootWritable = false, want true")
	}
	if boundary.Scratch != filepath.Join(home, "runtime-user-env", "tmp") {
		t.Fatalf("Scratch = %q", boundary.Scratch)
	}
	if !boundary.ScratchWritable {
		t.Fatal("ScratchWritable = false, want true")
	}
	if !slices.Contains(boundary.Writable, writable) {
		t.Fatalf("Writable = %v, want %q", boundary.Writable, writable)
	}
	if !slices.Contains(boundary.ReadOnly, readOnly) {
		t.Fatalf("ReadOnly = %v, want %q", boundary.ReadOnly, readOnly)
	}
}

func TestDescribeBoundaryUsesExposeTarget(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("remapped expose paths are Linux-only")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	source := filepath.Join(home, "host", "data")
	target := filepath.Join(home, "sandbox", "data")
	boundary, ok := DescribeBoundary(filepath.Join(home, "workspace"), config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: source, Target: target, Mode: "rw"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if !slices.Contains(boundary.Writable, target) {
		t.Fatalf("Writable = %v, want target %q", boundary.Writable, target)
	}
	if slices.Contains(boundary.Writable, source) {
		t.Fatalf("Writable = %v, must not contain source %q", boundary.Writable, source)
	}
}

func TestDescribeBoundaryRootOverrideReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not implement path-level filesystem remapping")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	boundary, ok := DescribeBoundary(filepath.Join(home, "workspace"), config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: home, Target: home, Mode: "ro"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if runtime.GOOS == "darwin" {
		// Seatbelt always allows writes under the instance root, so a
		// read-only override cannot be enforced.
		if !boundary.RootWritable {
			t.Fatal("RootWritable = false, want true on macOS")
		}
		if !boundary.ScratchWritable {
			t.Fatal("ScratchWritable = false, want true on macOS")
		}
		return
	}
	if boundary.RootWritable {
		t.Fatal("RootWritable = true, want false")
	}
	if boundary.ScratchWritable {
		t.Fatal("ScratchWritable = true, want false")
	}
}

func TestDescribeBoundaryDarwinReadOnlyUnderRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Seatbelt root write behavior")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	readOnly := filepath.Join(home, "data")
	boundary, ok := DescribeBoundary(filepath.Join(home, "workspace"), config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: readOnly, Target: readOnly, Mode: "ro"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if !boundary.RootWritable || !boundary.ScratchWritable {
		t.Fatalf("RootWritable=%v ScratchWritable=%v, want true/true",
			boundary.RootWritable, boundary.ScratchWritable)
	}
	if !slices.Contains(boundary.Writable, readOnly) {
		t.Fatalf("Writable = %v, want %q", boundary.Writable, readOnly)
	}
	if slices.Contains(boundary.ReadOnly, readOnly) {
		t.Fatalf("ReadOnly = %v, must not contain %q", boundary.ReadOnly, readOnly)
	}
}

func TestValidateDarwinExposePaths(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS Seatbelt expose_paths validation")
	}

	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "data")
	inside := filepath.Join(home, "data")

	if err := validateDarwinExposePaths(nil, home); err != nil {
		t.Fatalf("validateDarwinExposePaths(nil) error = %v", err)
	}
	if err := validateDarwinExposePaths([]config.ExposePath{
		{Source: outside, Target: outside, Mode: "ro"},
	}, home); err != nil {
		t.Fatalf("read-only path outside root rejected: %v", err)
	}
	if err := validateDarwinExposePaths([]config.ExposePath{
		{Source: inside, Target: inside, Mode: "rw"},
	}, home); err != nil {
		t.Fatalf("read-write path inside root rejected: %v", err)
	}
	if err := validateDarwinExposePaths([]config.ExposePath{
		{Source: inside, Target: inside, Mode: "ro"},
	}, home); err == nil {
		t.Fatal("read-only path inside root must be rejected")
	}
	if err := validateDarwinExposePaths([]config.ExposePath{
		{Source: home, Target: home, Mode: "ro"},
	}, home); err == nil {
		t.Fatal("read-only instance root must be rejected")
	}
	if err := validateDarwinExposePaths([]config.ExposePath{
		{Source: outside, Target: filepath.Join(outside, "dst"), Mode: "ro"},
	}, home); err == nil {
		t.Fatal("remapped expose path must be rejected")
	}
}

func TestDescribeBoundaryScratchOverride(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("scratch mount overrides are Linux-specific; macOS keeps the instance root writable")
	}

	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	scratch := filepath.Join(home, "runtime-user-env", "tmp")
	workspace := filepath.Join(home, "workspace")

	boundary, ok := DescribeBoundary(workspace, config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: home, Target: home, Mode: "ro"},
			{Source: scratch, Target: scratch, Mode: "rw"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if boundary.RootWritable {
		t.Fatal("RootWritable = true, want false")
	}
	if !boundary.ScratchWritable {
		t.Fatal("ScratchWritable = false, want true")
	}
	if slices.Contains(boundary.Writable, scratch) || slices.Contains(boundary.ReadOnly, scratch) {
		t.Fatalf("scratch must not be duplicated in expose lists: writable=%v read-only=%v",
			boundary.Writable, boundary.ReadOnly)
	}

	boundary, ok = DescribeBoundary(workspace, config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: scratch, Target: scratch, Mode: "ro"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if !boundary.RootWritable {
		t.Fatal("RootWritable = false, want true")
	}
	if boundary.ScratchWritable {
		t.Fatal("ScratchWritable = true, want false")
	}
	if slices.Contains(boundary.Writable, scratch) || slices.Contains(boundary.ReadOnly, scratch) {
		t.Fatalf("scratch must not be duplicated in expose lists: writable=%v read-only=%v",
			boundary.Writable, boundary.ReadOnly)
	}
}

func TestEffectiveBoundaryModeUsesLastAncestor(t *testing.T) {
	rules := []config.ExposePath{
		{Target: "/root", Mode: "rw"},
		{Target: "/root/runtime-user-env", Mode: "ro"},
		{Target: "/root/runtime-user-env/tmp", Mode: "rw"},
	}
	if got := effectiveBoundaryMode(rules, "/root/runtime-user-env/tmp"); got != "rw" {
		t.Fatalf("effectiveBoundaryMode() = %q, want rw", got)
	}
	if got := effectiveBoundaryMode(rules, "/root/other"); got != "rw" {
		t.Fatalf("effectiveBoundaryMode() = %q, want rw", got)
	}
	if got := effectiveBoundaryMode(rules, "/outside"); got != "" {
		t.Fatalf("effectiveBoundaryMode() = %q, want empty", got)
	}
}

func TestBackendLabel(t *testing.T) {
	tests := map[string]string{
		"linux":   "Linux bubblewrap",
		"darwin":  "macOS Seatbelt",
		"windows": "Windows restricted token and low integrity",
	}
	for goos, want := range tests {
		if got := backendLabel(goos); got != want {
			t.Errorf("backendLabel(%q) = %q, want %q", goos, got, want)
		}
	}
}
