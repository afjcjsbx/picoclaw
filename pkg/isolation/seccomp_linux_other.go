//go:build linux && !amd64 && !arm64

package isolation

import (
	"runtime"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// buildSeccompFilter returns nil on architectures that have no defined syscall
// table. The sandbox still runs but without a seccomp filter; this is a known
// gap and is logged at warn level so operators are aware.
func buildSeccompFilter() []unix.SockFilter {
	seccompUnsupportedWarn.Do(func() {
		logger.WarnCF("isolation", "seccomp denylist is not active on this architecture",
			map[string]any{
				"goarch": runtime.GOARCH,
				"reason": "no syscall table is defined for this architecture; the sandbox still runs without a seccomp filter",
			})
	})
	return nil
}

// seccompUnsupportedWarn keeps the warning to once per process: the filter is
// built for every isolated command.
var seccompUnsupportedWarn sync.Once
