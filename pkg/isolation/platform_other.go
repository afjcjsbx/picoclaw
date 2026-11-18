//go:build !linux && !windows && !darwin

package isolation

import (
	"os/exec"

	"github.com/sipeed/picoclaw/pkg/config"
)

func applyNativePlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	// Unsupported platforms keep the command unchanged for the native backend.
	// Docker is handled by the shared dispatcher and works here when selected.
	return nil
}

func postStartNativePlatformIsolation(cmd *exec.Cmd, isolation config.IsolationConfig, root string) error {
	return nil
}

func cleanupPendingNativePlatformResources(cmd *exec.Cmd) {
}
