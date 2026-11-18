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
				"risk_threshold": "high"
			}
		}
	}`)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if got := cfg.Tools.Exec.RiskThreshold; got != "high" {
		t.Fatalf("RiskThreshold = %q, want high", got)
	}
}
