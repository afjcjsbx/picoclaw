package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestSetupConfigWatcherPollingRetriesAfterInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(configPath, config.DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig(initial) error = %v", err)
	}

	configChan, stopWatch := setupConfigWatcherPolling(configPath, false, 50*time.Millisecond)
	t.Cleanup(stopWatch)

	// A partially written / invalid file must not be delivered and must not
	// permanently advance the change watermark.
	if err := os.WriteFile(configPath, []byte("{invalid-json"), 0o644); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	select {
	case cfg := <-configChan:
		t.Fatalf("invalid config was delivered: %+v", cfg)
	case <-time.After(1200 * time.Millisecond):
	}

	// Once the file becomes valid again, the watcher must deliver it.
	fixed := config.DefaultConfig()
	fixed.Gateway.Port = 38383
	if err := config.SaveConfig(configPath, fixed); err != nil {
		t.Fatalf("SaveConfig(fixed) error = %v", err)
	}

	select {
	case cfg := <-configChan:
		if cfg.Gateway.Port != fixed.Gateway.Port {
			t.Fatalf("reloaded port = %d, want %d", cfg.Gateway.Port, fixed.Gateway.Port)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for the fixed config to be delivered")
	}
}
