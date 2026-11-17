package isolation

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/sipeed/picoclaw/pkg/config"
)

// resolvedBackend returns the effective backend for the current platform.
// "auto" and an empty value keep the historical native behavior.
func resolvedBackend(isolation config.IsolationConfig) string {
	backend := isolation.EffectiveBackend()
	if backend != config.IsolationBackendAuto {
		return backend
	}
	switch runtime.GOOS {
	case "linux":
		return config.IsolationBackendBubblewrap
	case "darwin":
		return config.IsolationBackendSeatbelt
	case "windows":
		return config.IsolationBackendWindows
	default:
		return ""
	}
}

func isBackendSupportedOn(goos, backend string) bool {
	switch backend {
	case config.IsolationBackendDocker:
		return true
	case config.IsolationBackendSSH:
		// The SSH backend needs a POSIX remote shell. Local Windows is excluded
		// because the exec tool generates PowerShell commands there.
		return goos != "windows"
	case config.IsolationBackendBubblewrap:
		return goos == "linux"
	case config.IsolationBackendSeatbelt:
		return goos == "darwin"
	case config.IsolationBackendWindows:
		return goos == "windows"
	default:
		return false
	}
}

// applyPlatformIsolation dispatches to the configured backend.
func applyPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root, workspace string) error {
	switch resolvedBackend(isolation) {
	case config.IsolationBackendDocker:
		return applyDockerIsolation(cmd, isolation, root, workspace)
	case config.IsolationBackendSSH:
		return applySSHIsolation(cmd, isolation, root, workspace)
	default:
		return applyNativePlatformIsolation(cmd, isolation, root, workspace)
	}
}

func postStartPlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	switch resolvedBackend(isolation) {
	case config.IsolationBackendDocker:
		return postStartDockerIsolation(cmd, isolation, root)
	case config.IsolationBackendSSH:
		return postStartSSHIsolation(cmd, isolation, root)
	default:
		return postStartNativePlatformIsolation(cmd, isolation, root)
	}
}

func cleanupPendingPlatformResources(cmd *exec.Cmd) {
	cleanupPendingDockerResources(cmd)
	cleanupPendingSSHResources(cmd)
	cleanupPendingNativePlatformResources(cmd)
}

func validateBackend(isolation config.IsolationConfig) error {
	backend := resolvedBackend(isolation)
	if backend == "" {
		return fmt.Errorf("no subprocess isolation backend is available on %s", runtime.GOOS)
	}
	if !isBackendSupportedOn(runtime.GOOS, backend) {
		return fmt.Errorf(
			"subprocess isolation backend %q is not supported on %s",
			backend,
			runtime.GOOS,
		)
	}
	return nil
}
