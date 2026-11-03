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
	readOnly := filepath.Join(home, "toolchain")
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
	if boundary.RootWritable {
		t.Fatal("RootWritable = true, want false")
	}
	if boundary.ScratchWritable {
		t.Fatal("ScratchWritable = true, want false")
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
