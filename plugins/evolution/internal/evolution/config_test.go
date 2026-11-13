package evolution_test

import (
	"testing"

	"github.com/sipeed/picoclaw/plugins/evolution/internal/evolution"
)

func TestParseConfig_Defaults(t *testing.T) {
	cfg := evolution.ParseConfig(nil)
	if !cfg.Enabled {
		t.Fatal("expected enabled default")
	}
	if cfg.EffectiveMode() != "observe" {
		t.Fatalf("mode = %q, want observe", cfg.EffectiveMode())
	}
	if cfg.EffectiveMinTaskCount() != 2 || cfg.EffectiveMinSuccessRatio() != 0.7 {
		t.Fatalf("defaults = %d/%.2f", cfg.EffectiveMinTaskCount(), cfg.EffectiveMinSuccessRatio())
	}
	if cfg.RunsColdPathAutomatically() {
		t.Fatal("observe mode must not run the cold path automatically")
	}
}

func TestParseConfig_DraftModeAndSchedule(t *testing.T) {
	cfg := evolution.ParseConfig(map[string]string{
		"mode":              "draft",
		"cold_path_trigger": "scheduled",
		"cold_path_times":   " 09:30, 18:00 ,, ",
		"min_task_count":    "4",
		"min_success_ratio": "0.85",
	})
	if cfg.EffectiveMode() != "draft" {
		t.Fatalf("mode = %q, want draft", cfg.EffectiveMode())
	}
	if !cfg.RunsColdPathScheduled() {
		t.Fatal("expected scheduled cold path")
	}
	times := cfg.EffectiveColdPathTimes()
	if len(times) != 2 || times[0] != "09:30" || times[1] != "18:00" {
		t.Fatalf("times = %v", times)
	}
	if cfg.EffectiveMinTaskCount() != 4 || cfg.EffectiveMinSuccessRatio() != 0.85 {
		t.Fatalf("thresholds = %d/%.2f", cfg.EffectiveMinTaskCount(), cfg.EffectiveMinSuccessRatio())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestParseConfig_InvalidRatioFallsBack(t *testing.T) {
	cfg := evolution.ParseConfig(map[string]string{"min_success_ratio": "1.5"})
	if cfg.EffectiveMinSuccessRatio() != 0.7 {
		t.Fatalf("ratio = %.2f, want 0.7", cfg.EffectiveMinSuccessRatio())
	}
}

func TestParseConfig_Disabled(t *testing.T) {
	cfg := evolution.ParseConfig(map[string]string{"enabled": "false", "mode": "apply"})
	if cfg.Enabled {
		t.Fatal("expected disabled")
	}
	if cfg.EffectiveMode() != "" || cfg.AutoAppliesDrafts() {
		t.Fatalf("disabled config must not be active: mode=%q", cfg.EffectiveMode())
	}
}

func TestParseLLMConfig(t *testing.T) {
	cfg := evolution.ParseLLMConfig(map[string]string{
		"llm_base_url":        "https://api.example.com/v1/",
		"llm_api_key":         "secret",
		"llm_model":           "model-x",
		"llm_timeout_seconds": "12",
	})
	if !cfg.Enabled() {
		t.Fatal("expected enabled LLM config")
	}
	if cfg.BaseURL != "https://api.example.com/v1/" || cfg.Model != "model-x" || cfg.Timeout != 12 {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestParseLLMConfig_Incomplete(t *testing.T) {
	cfg := evolution.ParseLLMConfig(map[string]string{"llm_base_url": "https://api.example.com/v1"})
	if cfg.Enabled() {
		t.Fatal("missing model must disable the LLM")
	}
}
