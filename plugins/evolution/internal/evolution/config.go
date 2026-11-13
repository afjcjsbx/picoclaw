package evolution

import (
	"fmt"
	"strconv"
	"strings"
)

// Config is the plugin-local evolution configuration. It mirrors the semantics
// of the former core config block but is decoded from the plugin entry's
// string map, so the core never needs an evolution config type.
type Config struct {
	Enabled         bool
	Mode            string
	StateDir        string
	MinTaskCount    int
	MinSuccessRatio float64
	ColdPathTrigger string
	ColdPathTimes   []string
}

// DefaultConfig returns the plugin defaults: enabled for recording once the
// plugin itself is explicitly enabled, observe-only, and no automatic cold path.
func DefaultConfig() Config {
	return Config{
		Enabled:         true,
		Mode:            "observe",
		MinTaskCount:    2,
		MinSuccessRatio: 0.7,
		ColdPathTrigger: "after_turn",
	}
}

// ParseConfig decodes the string map supplied by the host in hook.hello.
// Unknown keys are ignored so the plugin stays forward-compatible. Invalid
// values fall back to the defaults instead of failing the hook handshake.
func ParseConfig(values map[string]string) Config {
	cfg := DefaultConfig()
	if values == nil {
		return cfg
	}
	if raw, ok := values["enabled"]; ok {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(raw)); err == nil {
			cfg.Enabled = parsed
		}
	}
	if raw, ok := values["mode"]; ok {
		cfg.Mode = strings.TrimSpace(raw)
	}
	if raw, ok := values["state_dir"]; ok {
		cfg.StateDir = strings.TrimSpace(raw)
	}
	if raw, ok := values["min_task_count"]; ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
			cfg.MinTaskCount = parsed
		}
	}
	if raw, ok := values["min_success_ratio"]; ok {
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			cfg.MinSuccessRatio = parsed
		}
	}
	if raw, ok := values["cold_path_trigger"]; ok {
		cfg.ColdPathTrigger = strings.TrimSpace(raw)
	}
	if raw, ok := values["cold_path_times"]; ok {
		cfg.ColdPathTimes = parseCSV(raw)
	}
	return cfg
}

func parseCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Validate reports whether the configuration can run. It is intentionally
// permissive: evolution is best-effort and must never break the agent turn.
func (c Config) Validate() error {
	if c.MinTaskCount < 0 {
		return fmt.Errorf("min_task_count must not be negative")
	}
	if c.MinSuccessRatio < 0 || c.MinSuccessRatio > 1 {
		return fmt.Errorf("min_success_ratio must be between 0 and 1")
	}
	if c.ColdPathTriggerMode() == "scheduled" && len(c.EffectiveColdPathTimes()) == 0 {
		return fmt.Errorf("scheduled cold path requires at least one cold_path_times entry")
	}
	return nil
}

func (c Config) EffectiveMode() string {
	if !c.Enabled {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "draft":
		return "draft"
	case "apply":
		return "apply"
	case "", "observe":
		return "observe"
	default:
		return "observe"
	}
}

func (c Config) RunsColdPathAutomatically() bool {
	return c.RunsColdPathAfterTurn() || c.RunsColdPathScheduled()
}

func (c Config) ColdPathTriggerMode() string {
	if c.EffectiveMode() != "draft" && c.EffectiveMode() != "apply" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.ColdPathTrigger)) {
	case "", "after_turn":
		return "after_turn"
	case "scheduled":
		return "scheduled"
	case "manual", "none", "off":
		return "manual"
	default:
		return "after_turn"
	}
}

func (c Config) RunsColdPathAfterTurn() bool {
	return c.ColdPathTriggerMode() == "after_turn"
}

func (c Config) RunsColdPathScheduled() bool {
	return c.ColdPathTriggerMode() == "scheduled"
}

func (c Config) EffectiveMinTaskCount() int {
	if c.MinTaskCount > 0 {
		return c.MinTaskCount
	}
	return 2
}

func (c Config) EffectiveMinSuccessRatio() float64 {
	if c.MinSuccessRatio > 0 && c.MinSuccessRatio <= 1 {
		return c.MinSuccessRatio
	}
	return 0.7
}

func (c Config) EffectiveColdPathTimes() []string {
	out := make([]string, 0, len(c.ColdPathTimes))
	for _, value := range c.ColdPathTimes {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (c Config) AutoAppliesDrafts() bool {
	return c.EffectiveMode() == "apply"
}

// LLMConfig describes the optional OpenAI-compatible endpoint used for better
// clustering, success judging, and draft generation. When it is incomplete the
// runtime falls back to the heuristic implementations.
type LLMConfig struct {
	BaseURL    string
	APIKey     string
	APIKeyFile string
	Model      string
	Timeout    int // seconds; zero uses the client default
}

func ParseLLMConfig(values map[string]string) LLMConfig {
	cfg := LLMConfig{}
	if values == nil {
		return cfg
	}
	cfg.BaseURL = strings.TrimSpace(values["llm_base_url"])
	cfg.APIKey = strings.TrimSpace(values["llm_api_key"])
	cfg.APIKeyFile = strings.TrimSpace(values["llm_api_key_file"])
	cfg.Model = strings.TrimSpace(values["llm_model"])
	if raw, ok := values["llm_timeout_seconds"]; ok {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
			cfg.Timeout = parsed
		}
	}
	return cfg
}

func (c LLMConfig) Enabled() bool {
	return c.BaseURL != "" && c.Model != ""
}
