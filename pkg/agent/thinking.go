package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// ThinkingLevel controls how the provider sends thinking parameters.
//
//   - "adaptive": sends {thinking: {type: "adaptive"}} + output_config.effort (Claude 4.6+)
//   - "low"/"medium"/"high"/"xhigh": sends {thinking: {type: "enabled", budget_tokens: N}} (all models)
//   - "off": disables thinking
type ThinkingLevel string

const (
	ThinkingOff      ThinkingLevel = "off"
	ThinkingLow      ThinkingLevel = "low"
	ThinkingMedium   ThinkingLevel = "medium"
	ThinkingHigh     ThinkingLevel = "high"
	ThinkingXHigh    ThinkingLevel = "xhigh"
	ThinkingAdaptive ThinkingLevel = "adaptive"
)

// ParseThinkingLevel normalizes a config string to a ThinkingLevel.
// Case-insensitive and whitespace-tolerant for user-facing config values.
// Returns ThinkingOff for unknown or empty values.
func ParseThinkingLevel(level string) ThinkingLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "adaptive":
		return ThinkingAdaptive
	case "low":
		return ThinkingLow
	case "medium":
		return ThinkingMedium
	case "high":
		return ThinkingHigh
	case "xhigh":
		return ThinkingXHigh
	default:
		return ThinkingOff
	}
}

func IsConfiguredThinkingLevel(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "low", "medium", "high", "xhigh", "adaptive":
		return true
	default:
		return false
	}
}

type ThinkingSettings struct {
	Level      ThinkingLevel
	Configured bool
}

func ThinkingSettingsFromModelConfig(mc *config.ModelConfig) ThinkingSettings {
	if mc == nil || !IsConfiguredThinkingLevel(mc.ThinkingLevel) {
		return ThinkingSettings{}
	}
	return ThinkingSettings{
		Level:      ParseThinkingLevel(mc.ThinkingLevel),
		Configured: true,
	}
}

func ActiveThinkingSettings(agent *AgentInstance, modelCfg *config.ModelConfig) ThinkingSettings {
	if settings := ThinkingSettingsFromModelConfig(modelCfg); settings.Configured {
		return settings
	}
	if modelCfg == nil && agent != nil {
		return ThinkingSettings{
			Level:      agent.ThinkingLevel,
			Configured: agent.ThinkingLevelConfigured,
		}
	}
	return ThinkingSettings{}
}

func ApplyThinkingOption(
	opts map[string]any,
	provider providers.LLMProvider,
	settings ThinkingSettings,
	warnUnsupported bool,
	agentID string,
) {
	if opts == nil || !settings.Configured {
		return
	}
	if settings.Level == ThinkingOff {
		opts["thinking_level"] = string(settings.Level)
		return
	}
	if tc, ok := provider.(providers.ThinkingCapable); ok && tc.SupportsThinking() {
		opts["thinking_level"] = string(settings.Level)
		return
	}
	if warnUnsupported {
		logger.WarnCF("agent", "thinking_level is set but current provider does not support it, ignoring",
			map[string]any{"agent_id": agentID, "thinking_level": string(settings.Level)})
	}
}
