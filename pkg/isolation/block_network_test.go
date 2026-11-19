package isolation

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestBlockNetworkConfigRoundTrip verifies that block_network survives a JSON
// marshal/unmarshal cycle unchanged.
func TestBlockNetworkConfigRoundTrip(t *testing.T) {
	cfg := config.IsolationConfig{
		Enabled:      true,
		BlockNetwork: true,
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"block_network":true`) {
		t.Fatalf("block_network missing from JSON: %s", raw)
	}
	var got config.IsolationConfig
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !got.BlockNetwork {
		t.Fatal("BlockNetwork = false after round-trip, want true")
	}
}

// TestBlockNetworkDefaultFalse confirms that BlockNetwork is off by default
// and therefore does not change existing behavior.
func TestBlockNetworkDefaultFalse(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Isolation.BlockNetwork {
		t.Fatal("DefaultConfig().Isolation.BlockNetwork should be false")
	}
}

// TestDescribeBoundaryBlockNetwork checks that Network=false on Linux when
// block_network=true, and Network=true (unchanged) on other platforms.
func TestDescribeBoundaryBlockNetwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	boundary, ok := DescribeBoundary(filepath.Join(home, "workspace"), config.IsolationConfig{
		Enabled:      true,
		BlockNetwork: true,
	})
	if !ok {
		t.Fatal("DescribeBoundary() = false, want true")
	}
	if runtime.GOOS == "linux" {
		if boundary.Network {
			t.Fatal("Network = true with block_network=true on Linux, want false")
		}
	} else {
		// macOS and Windows: boundary is returned, but preflight would reject the
		// config if the agent tried to start. The boundary itself keeps Network=true
		// because those backends cannot enforce it; the preflight error is the guard.
		if !boundary.Network {
			t.Fatalf("Network = false on %s without enforcement, want true", runtime.GOOS)
		}
	}
}

// TestPreflightBlockNetworkOnUnsupportedOS ensures that block_network=true is
// rejected with a clear error on platforms that cannot enforce it, instead of
// silently being ignored (a security setting must fail closed).
func TestPreflightBlockNetworkOnUnsupportedOS(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	if runtime.GOOS == "linux" {
		t.Skip("preflight block_network OS rejection is darwin/windows-only")
	}

	err := preflight(config.IsolationConfig{
		Enabled:      true,
		BlockNetwork: true,
	})
	if err == nil {
		t.Fatalf("preflight() = nil, want error for block_network on %s", runtime.GOOS)
	}
	if !strings.Contains(err.Error(), "block_network") {
		t.Fatalf("error should mention block_network: %v", err)
	}
}
