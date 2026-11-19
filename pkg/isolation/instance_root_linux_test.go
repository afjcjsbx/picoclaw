//go:build linux

package isolation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestDefaultLinuxInstanceExposePaths_NarrowSet(t *testing.T) {
	root := t.TempDir()
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot: %v", err)
	}

	paths := defaultLinuxInstanceExposePaths(root)

	wantTargets := map[string]string{
		filepath.Join(root, pkg.WorkspaceName):  "rw",
		filepath.Join(root, "runtime-user-env"): "rw",
		filepath.Join(root, "skills"):           "ro",
	}
	for _, p := range paths {
		wantMode, ok := wantTargets[p.Target]
		if !ok {
			t.Errorf("unexpected path in narrow set: %+v", p)
			continue
		}
		if p.Mode != wantMode {
			t.Errorf("path %s: mode=%q, want %q", p.Target, p.Mode, wantMode)
		}
		delete(wantTargets, p.Target)
	}
	for missing := range wantTargets {
		t.Errorf("defaultLinuxInstanceExposePaths missing %s", missing)
	}
}

func TestDefaultLinuxInstanceExposePaths_ReturnsFullPolicyWhenDirsMissing(t *testing.T) {
	root := t.TempDir()
	// defaultLinuxInstanceExposePaths returns the policy list even when the
	// subdirectories do not exist yet; BuildLinuxMountPlan applies the existence
	// filter before passing the plan to bwrap.
	paths := defaultLinuxInstanceExposePaths(root)
	if len(paths) != 3 {
		t.Fatalf("expected 3 policy paths, got: %+v", paths)
	}
}

func TestBuildLinuxMountPlan_ExistenceFilterSkipsMissingDirs(t *testing.T) {
	root := t.TempDir()
	// No PrepareInstanceRoot: workspace/runtime-user-env/skills do not exist.
	plan := BuildLinuxMountPlan(root, nil)
	for _, rule := range plan {
		if rule.Target == filepath.Join(root, pkg.WorkspaceName) ||
			rule.Target == filepath.Join(root, "runtime-user-env") ||
			rule.Target == filepath.Join(root, "skills") {
			t.Fatalf("BuildLinuxMountPlan must skip non-existent instance subdir; got rule: %+v", rule)
		}
	}
}

func TestDefaultLinuxInstanceExposePaths_RootNotExposed(t *testing.T) {
	root := t.TempDir()
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot: %v", err)
	}

	for _, p := range defaultLinuxInstanceExposePaths(root) {
		if p.Target == root {
			t.Fatalf("defaultLinuxInstanceExposePaths must not expose the root itself, got: %+v", p)
		}
	}
}

func TestDescribeBoundaryLinux_OperatorRestoresFullRoot(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	home, err := ResolveInstanceRoot()
	if err != nil {
		t.Fatalf("ResolveInstanceRoot: %v", err)
	}
	if err := PrepareInstanceRoot(home); err != nil {
		t.Fatalf("PrepareInstanceRoot: %v", err)
	}

	// Operator explicitly re-exposes the entire root rw.
	boundary, ok := DescribeBoundary(filepath.Join(home, pkg.WorkspaceName), config.IsolationConfig{
		Enabled: true,
		ExposePaths: []config.ExposePath{
			{Source: home, Target: home, Mode: "rw"},
		},
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if !boundary.RootVisible {
		t.Fatal("RootVisible = false, want true when operator adds root expose_paths")
	}
	if !boundary.RootWritable {
		t.Fatal("RootWritable = false, want true when operator adds root expose_paths rw")
	}
}

func TestDescribeBoundaryLinux_RootNotVisibleByDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EnvHome, root)
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot: %v", err)
	}

	boundary, ok := DescribeBoundary(filepath.Join(root, pkg.WorkspaceName), config.IsolationConfig{
		Enabled: true,
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if boundary.RootVisible {
		t.Fatal("RootVisible = true, want false with default Linux mount set")
	}
	if boundary.RootWritable {
		t.Fatal("RootWritable = true, want false with default Linux mount set")
	}
}

func TestBuildLinuxMountPlan_ConfigJsonNotMounted(t *testing.T) {
	root := t.TempDir()
	if err := PrepareInstanceRoot(root); err != nil {
		t.Fatalf("PrepareInstanceRoot: %v", err)
	}
	// Create a config.json so its parent is there; ensure it is not in the plan.
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	plan := BuildLinuxMountPlan(root, nil)
	for _, rule := range plan {
		if rule.Target == configPath || rule.Target == root {
			t.Fatalf("config.json or instance root must not be in the default Linux mount plan; got rule: %+v", rule)
		}
	}
}
