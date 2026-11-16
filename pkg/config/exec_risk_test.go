package config

import (
	"encoding/json"
	"testing"
)

func TestExecRiskConfigDefaultsAndJSON(t *testing.T) {
	t.Parallel()

	if got := DefaultConfig().Tools.Exec.RiskThreshold; got != "medium" {
		t.Fatalf("DefaultConfig().Tools.Exec.RiskThreshold = %q, want medium", got)
	}

	var cfg Config
	data := []byte(`{
		"tools": {
			"exec": {
				"risk_threshold": "high",
				"risk_overrides": {"rm": "low"},
				"arg_modifiers": {
					"echo": [{"args": ["--danger"], "level": "critical"}]
				}
			}
		}
	}`)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	exec := cfg.Tools.Exec
	if exec.RiskThreshold != "high" {
		t.Fatalf("RiskThreshold = %q, want high", exec.RiskThreshold)
	}
	if exec.RiskOverrides["rm"] != "low" {
		t.Fatalf("RiskOverrides = %#v", exec.RiskOverrides)
	}
	mods := exec.ArgModifiers["echo"]
	if len(mods) != 1 || len(mods[0].Args) != 1 || mods[0].Args[0] != "--danger" || mods[0].Level != "critical" {
		t.Fatalf("ArgModifiers = %#v", exec.ArgModifiers)
	}
}
