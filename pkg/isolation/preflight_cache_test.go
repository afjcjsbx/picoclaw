package isolation

import (
	"path/filepath"
	"testing"

	"github.com/afjcjsbx/picoclaw/pkg/config"
)

func TestPreflightCacheInvalidatedByConfigure(t *testing.T) {
	t.Setenv(config.EnvHome, filepath.Join(t.TempDir(), "home"))
	t.Cleanup(func() { Configure(config.DefaultConfig()) })

	base := config.DefaultConfig()

	bad := *base
	bad.Isolation.Enabled = true
	bad.Isolation.ExposePaths = []config.ExposePath{
		{Source: "/tmp/a", Target: "/tmp/dup", Mode: "ro"},
		{Source: "/tmp/b", Target: "/tmp/dup", Mode: "ro"},
	}
	Configure(&bad)
	if err := preflightCached(); err == nil {
		t.Fatal("expected duplicate expose_path target to fail preflight")
	}
	// Same generation: the cached error is returned without recompute.
	if err := preflightCached(); err == nil {
		t.Fatal("expected cached preflight error")
	}

	good := *base
	good.Isolation.Enabled = true
	good.Isolation.ExposePaths = nil
	Configure(&good)
	if err := preflightCached(); err != nil {
		t.Fatalf("expected success after Configure invalidated the cache: %v", err)
	}
}
