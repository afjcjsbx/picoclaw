package loop

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/agent/agentctx"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func resolveTurnProfileOptions(cfg *config.Config, opts processOptions) (processOptions, error) {
	if cfg == nil {
		return opts, nil
	}
	profile, ok, err := cfg.Agents.Defaults.ResolveTurnProfile()
	if err != nil {
		return opts, err
	}
	if !ok {
		return opts, nil
	}
	opts.TurnProfile = profile
	if profile.HistoryMode == config.TurnProfileModeOff {
		opts.NoHistory = true
		opts.EnableSummary = false
	}
	return opts, nil
}

func turnProfileSystemPromptOff(profile config.EffectiveTurnProfile) bool {
	return profile.Enabled && profile.SystemPromptMode == config.TurnProfileModeOff
}

func turnProfileSkillsOff(profile config.EffectiveTurnProfile) bool {
	return profile.Enabled && profile.SkillsMode == config.TurnProfileModeOff
}

func turnProfileCustomSkills(profile config.EffectiveTurnProfile) bool {
	return profile.Enabled && profile.SkillsMode == config.TurnProfileModeCustom
}

func turnProfileHasCallableTools(
	profile config.EffectiveTurnProfile,
	defs []providers.ToolDefinition,
) bool {
	if !profile.Enabled {
		return true
	}
	return len(filterToolsByTurnProfile(defs, profile)) > 0
}

func turnProfileToolAllowed(profile config.EffectiveTurnProfile, name string) bool {
	if !profile.Enabled {
		return true
	}
	switch profile.ToolsMode {
	case config.TurnProfileModeOff:
		return false
	case config.TurnProfileModeCustom:
		allowed := agentctx.CleanAllowedSet(profile.AllowedTools)
		if len(allowed) == 0 {
			return false
		}
		_, ok := allowed[strings.ToLower(strings.TrimSpace(name))]
		return ok
	default:
		return true
	}
}

func filterToolsByTurnProfile(
	defs []providers.ToolDefinition,
	profile config.EffectiveTurnProfile,
) []providers.ToolDefinition {
	if !profile.Enabled {
		return defs
	}
	switch profile.ToolsMode {
	case config.TurnProfileModeOff:
		return nil
	case config.TurnProfileModeCustom:
		allowed := agentctx.CleanAllowedSet(profile.AllowedTools)
		if len(allowed) == 0 {
			return nil
		}
		filtered := make([]providers.ToolDefinition, 0, len(defs))
		for _, def := range defs {
			if _, ok := allowed[strings.ToLower(strings.TrimSpace(def.Function.Name))]; ok {
				filtered = append(filtered, def)
			}
		}
		return filtered
	default:
		return defs
	}
}
