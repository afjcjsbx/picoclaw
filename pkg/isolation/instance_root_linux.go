//go:build linux

package isolation

import (
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg"
	"github.com/sipeed/picoclaw/pkg/config"
)

// defaultLinuxInstanceExposePaths returns the narrow policy set of
// instance-root subdirectories for the Linux sandbox:
//
//   - workspace (rw) — the agent's working area
//   - runtime-user-env (rw) — redirected HOME/TMPDIR/XDG directories
//   - skills (ro) — skill scripts that may be executed inside the sandbox
//
// config.json, .security.yml, logs, cache, state and every other path under
// the instance root are intentionally excluded so a child process cannot read
// API keys or modify the instance configuration.
//
// This function returns the configured policy regardless of whether the
// directories exist on disk yet. BuildLinuxMountPlan filters the list by
// existence before passing it to bwrap; PrepareInstanceRoot (called during
// Preflight) creates the standard set before the first child is launched.
func defaultLinuxInstanceExposePaths(root string) []config.ExposePath {
	return []config.ExposePath{
		{
			Source: filepath.Join(root, pkg.WorkspaceName),
			Target: filepath.Join(root, pkg.WorkspaceName),
			Mode:   "rw",
		},
		{
			Source: filepath.Join(root, "runtime-user-env"),
			Target: filepath.Join(root, "runtime-user-env"),
			Mode:   "rw",
		},
		{
			Source: filepath.Join(root, "skills"),
			Target: filepath.Join(root, "skills"),
			Mode:   "ro",
		},
	}
}
