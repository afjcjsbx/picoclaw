package isolation

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Boundary describes the filesystem and network boundary applied to isolated
// child processes. PathBoundary is false on backends that only apply
// process-level restrictions and do not provide a path-level filesystem view.
type Boundary struct {
	Backend         string
	Root            string
	RootWritable    bool
	Workspace       string
	Scratch         string
	ScratchWritable bool
	Writable        []string
	ReadOnly        []string
	Network         bool
	PathBoundary    bool
}

// DescribeBoundary returns the effective subprocess boundary for workspace and
// cfg. The boolean is false when isolation is disabled, unsupported, or the
// instance root cannot be resolved.
//
// The returned paths intentionally describe the stable configuration boundary,
// not every per-command mount. PicoClaw may additionally expose a command's
// executable, working directory, and absolute path arguments when it starts a
// child process.
func DescribeBoundary(workspace string, cfg config.IsolationConfig) (Boundary, bool) {
	if !cfg.Enabled || !IsSupported() {
		return Boundary{}, false
	}

	root, err := ResolveInstanceRoot()
	if err != nil {
		return Boundary{}, false
	}

	boundary := Boundary{
		Backend:   backendLabel(runtime.GOOS),
		Root:      filepath.Clean(root),
		Workspace: cleanBoundaryPath(workspace),
		Network:   true,
	}

	switch runtime.GOOS {
	case "linux", "darwin":
		boundary.PathBoundary = true
		boundary.Scratch = filepath.Join(boundary.Root, "runtime-user-env", "tmp")

		customTargets := customExposeTargets(cfg.ExposePaths)
		merged := MergeExposePaths(DefaultExposePaths(boundary.Root), cfg.ExposePaths)

		for _, rule := range merged {
			target := filepath.Clean(rule.Target)
			if target == boundary.Root || target == boundary.Scratch {
				// Root and scratch have dedicated prompt lines; their
				// effective mode is derived from the merged mount order below.
				continue
			}
			if _, custom := customTargets[target]; !custom {
				// Built-in system mounts are summarized in the prompt; they
				// are not part of the writable set users configure.
				continue
			}
			switch rule.Mode {
			case "rw":
				boundary.Writable = append(boundary.Writable, target)
			case "ro":
				boundary.ReadOnly = append(boundary.ReadOnly, target)
			}
		}

		boundary.RootWritable = effectiveBoundaryMode(merged, boundary.Root) == "rw"
		boundary.ScratchWritable = effectiveBoundaryMode(merged, boundary.Scratch) == "rw"
	case "windows":
		// The Windows backend applies restricted tokens, low integrity, and a
		// job object, but does not implement path-level filesystem remapping.
		boundary.PathBoundary = false
	default:
		return Boundary{}, false
	}

	return boundary, true
}

func backendLabel(goos string) string {
	switch goos {
	case "linux":
		return "Linux bubblewrap"
	case "darwin":
		return "macOS Seatbelt"
	case "windows":
		return "Windows restricted token and low integrity"
	default:
		return goos
	}
}

func cleanBoundaryPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func customExposeTargets(items []config.ExposePath) map[string]struct{} {
	targets := make(map[string]struct{}, len(items))
	for _, item := range items {
		targets[NormalizeExposePath(item).Target] = struct{}{}
	}
	return targets
}

// effectiveBoundaryMode returns the mode of the last merged rule whose target
// is path or one of its ancestors. Mount rules are applied in merge order, so a
// later parent mount overrides an earlier child mount and vice versa.
func effectiveBoundaryMode(rules []config.ExposePath, path string) string {
	mode := ""
	for _, rule := range rules {
		if boundaryPathWithin(rule.Target, path) {
			mode = rule.Mode
		}
	}
	return mode
}

// boundaryPathWithin reports whether child is parent or below it.
func boundaryPathWithin(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
