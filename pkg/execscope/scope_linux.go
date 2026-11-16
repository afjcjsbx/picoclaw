//go:build linux

package execscope

import (
	"fmt"
	"os"

	"github.com/sipeed/picoclaw/pkg/isolation"
)

func newPlatformScope(cfg Config) (Scope, error) {
	switch forced := os.Getenv("PICOCLAW_EXEC_SCOPE_BACKEND"); forced {
	case "", "auto":
	case "cgroup":
		return newCgroupScope(cfg)
	case "pidns":
		if !isolation.CurrentConfig().Enabled || !isolation.PIDNamespaceSupported() {
			return nil, fmt.Errorf("pidns scope requested but bubblewrap PID namespace is unavailable")
		}
		return newPIDNSScope(cfg), nil
	case "supervisor":
		return newSupervisorScope(cfg)
	case "killpg":
		return newKillPGScope(cfg), nil
	default:
		return nil, fmt.Errorf("unknown exec scope backend %q", forced)
	}

	if os.Getpid() != 1 {
		if scope, err := newCgroupScope(cfg); err == nil {
			return scope, nil
		}
	}
	// If we are PID 1 (common in containers), orphaned descendants become our
	// children and would stay zombies: Go's exec layer only reaps the direct
	// child. Prefer a supervisor, which reaps its own subtree.
	if isolation.CurrentConfig().Enabled {
		if isolation.PIDNamespaceSupported() {
			return newPIDNSScope(cfg), nil
		}
		// Keep filesystem isolation even when the kernel cannot create a PID
		// namespace; the killpg backend is best-effort in this case.
		return newKillPGScope(cfg), nil
	}
	if scope, err := newSupervisorScope(cfg); err == nil {
		return scope, nil
	}
	return newKillPGScope(cfg), nil
}
