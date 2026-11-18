package tools

import (
	"os"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
)

// TestMain keeps the unit suite on the host. Production defaults to a required
// sandbox; the isolation-specific tests configure their own mode.
func TestMain(m *testing.M) {
	isolation.Configure(&config.Config{Isolation: config.IsolationConfig{Mode: config.IsolationModeOff}})
	os.Exit(m.Run())
}
