package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigLoopDetection(t *testing.T) {
	got := DefaultConfig().Agents.Defaults.LoopDetection
	if got.Enabled {
		t.Fatal("loop detection must remain opt-in")
	}
	if got.RepeatThreshold != DefaultLoopDetectionRepeatThreshold {
		t.Fatalf("repeat threshold = %d, want %d", got.RepeatThreshold, DefaultLoopDetectionRepeatThreshold)
	}
	if got.CriticalThreshold != DefaultLoopDetectionCriticalThreshold {
		t.Fatalf("critical threshold = %d, want %d", got.CriticalThreshold, DefaultLoopDetectionCriticalThreshold)
	}
	if got.WindowSize != DefaultLoopDetectionWindowSize {
		t.Fatalf("window size = %d, want %d", got.WindowSize, DefaultLoopDetectionWindowSize)
	}
}

func TestLoopDetectionConfigNormalized(t *testing.T) {
	got := (LoopDetectionConfig{
		Enabled:           true,
		RepeatThreshold:   10,
		CriticalThreshold: 4,
		WindowSize:        2,
	}).Normalized()

	if !got.Enabled {
		t.Fatal("normalization changed enabled flag")
	}
	if got.CriticalThreshold <= got.RepeatThreshold {
		t.Fatalf("critical threshold %d must exceed repeat threshold %d", got.CriticalThreshold, got.RepeatThreshold)
	}
	if got.WindowSize < got.CriticalThreshold {
		t.Fatalf("window size %d must cover critical threshold %d", got.WindowSize, got.CriticalThreshold)
	}
}

func TestLoadConfigLoopDetectionEnvOverrides(t *testing.T) {
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_LOOP_DETECTION_ENABLED", "true")
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_LOOP_DETECTION_REPEAT_THRESHOLD", "4")
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_LOOP_DETECTION_CRITICAL_THRESHOLD", "8")
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_LOOP_DETECTION_WINDOW_SIZE", "24")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	got := cfg.Agents.Defaults.LoopDetection
	if !got.Enabled || got.RepeatThreshold != 4 || got.CriticalThreshold != 8 || got.WindowSize != 24 {
		t.Fatalf("loop detection env overrides not applied: %+v", got)
	}
}
