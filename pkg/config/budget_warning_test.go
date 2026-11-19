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

func TestRunBudgetConfig(t *testing.T) {
	d := DefaultConfig().Agents.Defaults
	if d.RunBudgetSeconds != 0 || d.RunBudgetWarningThreshold != 0.8 {
		t.Fatalf("defaults = %d / %v, want off / 0.8", d.RunBudgetSeconds, d.RunBudgetWarningThreshold)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"version":3,"agents":{"defaults":{"run_budget_seconds":1800,"run_budget_warning_threshold":0.5}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Agents.Defaults; got.RunBudgetSeconds != 1800 || got.RunBudgetWarningThreshold != 0.5 {
		t.Fatalf("loaded = %d / %v, want 1800 / 0.5", got.RunBudgetSeconds, got.RunBudgetWarningThreshold)
	}

	// null disables, like 0.
	nullBody := `{"version":3,"agents":{"defaults":{"run_budget_seconds":null}}}`
	if err = os.WriteFile(path, []byte(nullBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if cfg, err = LoadConfig(path); err != nil || cfg.Agents.Defaults.RunBudgetSeconds != 0 {
		t.Fatalf("null budget = %v, err %v, want 0", cfg.Agents.Defaults.RunBudgetSeconds, err)
	}

	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_RUN_BUDGET_WARNING_THRESHOLD", "1")
	if _, err = LoadConfig(path); err == nil {
		t.Fatal("LoadConfig accepted threshold 1, want an error")
	}
}
