package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestBudgetWarningRatioDefaultAndLoad(t *testing.T) {
	if got := DefaultConfig().Agents.Defaults.MaxToolBudgetWarningRatio; got != 0.75 {
		t.Fatalf("default ratio = %v, want 0.75", got)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Agents.Defaults.MaxToolBudgetWarningRatio; got != 0.75 {
		t.Fatalf("ratio of a config without the field = %v, want default 0.75", got)
	}

	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_MAX_TOOL_BUDGET_WARNING_RATIO", "1")
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("LoadConfig accepted ratio 1, want an error")
	}
}

func TestValidateBudgetWarningRatio(t *testing.T) {
	for _, tt := range []struct {
		ratio float64
		ok    bool
	}{
		{0, true}, // unset: the default applies
		{0.5, true},
		{0.99, true},
		{1, false},
		{-0.1, false},
		{1.5, false},
		{math.NaN(), false},
	} {
		cfg := &Config{}
		cfg.Agents.Defaults.MaxToolBudgetWarningRatio = tt.ratio
		if err := cfg.ValidateBudgetWarningRatio(); (err == nil) != tt.ok {
			t.Errorf("ratio %v: err = %v, want ok=%v", tt.ratio, err, tt.ok)
		}
	}
}
