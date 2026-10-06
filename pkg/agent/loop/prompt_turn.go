package loop

import (
	"strings"

	agentcore "github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/agent/agentctx"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func promptBuildRequestForTurn(
	ts *turnState,
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
	cfg *config.Config,
) agentctx.PromptBuildRequest {
	req := agentctx.PromptBuildRequest{
		History:           history,
		Summary:           summary,
		CurrentMessage:    currentMessage,
		Media:             append([]string(nil), media...),
		Channel:           ts.channel,
		ChatID:            ts.chatID,
		SenderID:          ts.opts.Dispatch.SenderID(),
		SenderDisplayName: ts.opts.SenderDisplayName,
		ActiveSkills:      activeSkillNames(ts.agent, ts.opts),
		Overlays:          promptOverlaysForOptions(ts.opts),
	}
	hasCallableTools := true
	if ts.profile.Enabled {
		hasCallableTools = turnProfileHasCallableTools(ts.profile, ts.agent.Tools.ToProviderDefs()) ||
			turnProfileNativeSearchCallable(cfg, ts.profile, ts.agent)
	}
	if turnProfileSystemPromptOff(ts.profile) {
		req.SuppressDefaultSystemPrompt = true
		req.SuppressSkillContext = true
		req.ToolUseFallback = hasCallableTools
	}
	if ts.profile.Enabled && !hasCallableTools {
		req.SuppressToolUseRule = true
	}
	if turnProfileSkillsOff(ts.profile) {
		req.SuppressSkillContext = true
	}
	if turnProfileCustomSkills(ts.profile) {
		req.AllowedSkills = append([]string(nil), ts.profile.AllowedSkills...)
	}
	if ts.profile.Enabled && ts.profile.ToolsMode == config.TurnProfileModeCustom {
		req.AllowedTools = append([]string(nil), ts.profile.AllowedTools...)
	}
	return req
}

func turnProfileNativeSearchCallable(
	cfg *config.Config,
	profile config.EffectiveTurnProfile,
	agent *agentcore.AgentInstance,
) bool {
	if cfg == nil || agent == nil {
		return false
	}
	if !cfg.Tools.IsToolEnabled("web") || !cfg.Tools.Web.PreferNative {
		return false
	}
	if !turnProfileToolAllowed(profile, "web_search") {
		return false
	}
	nativeProvider, ok := agent.Provider.(providers.NativeSearchCapable)
	return ok && nativeProvider.SupportsNativeSearch()
}

func promptBuildRequestForProcessOptions(
	agent *agentcore.AgentInstance,
	opts processOptions,
	history []providers.Message,
	summary string,
	currentMessage string,
	media []string,
) agentctx.PromptBuildRequest {
	req := agentctx.PromptBuildRequest{
		History:           history,
		Summary:           summary,
		CurrentMessage:    currentMessage,
		Media:             append([]string(nil), media...),
		Channel:           opts.Channel,
		ChatID:            opts.ChatID,
		SenderID:          opts.SenderID,
		SenderDisplayName: opts.SenderDisplayName,
		ActiveSkills:      activeSkillNames(agent, opts),
		Overlays:          promptOverlaysForOptions(opts),
	}
	profile := opts.TurnProfile
	hasCallableTools := true
	if profile.Enabled && agent != nil {
		hasCallableTools = turnProfileHasCallableTools(profile, agent.Tools.ToProviderDefs())
	}
	if turnProfileSystemPromptOff(profile) {
		req.SuppressDefaultSystemPrompt = true
		req.SuppressSkillContext = true
		req.ToolUseFallback = hasCallableTools
	}
	if profile.Enabled && !hasCallableTools {
		req.SuppressToolUseRule = true
	}
	if turnProfileSkillsOff(profile) {
		req.SuppressSkillContext = true
	}
	if turnProfileCustomSkills(profile) {
		req.AllowedSkills = append([]string(nil), profile.AllowedSkills...)
	}
	if profile.Enabled && profile.ToolsMode == config.TurnProfileModeCustom {
		req.AllowedTools = append([]string(nil), profile.AllowedTools...)
	}
	return req
}

func promptOverlaysForOptions(opts processOptions) []agentctx.PromptPart {
	systemPrompt := strings.TrimSpace(opts.SystemPromptOverride)
	if systemPrompt == "" {
		return nil
	}

	return []agentctx.PromptPart{
		{
			ID:      "instruction.subturn_profile",
			Layer:   agentctx.PromptLayerInstruction,
			Slot:    agentctx.PromptSlotWorkspace,
			Source:  agentctx.PromptSource{ID: agentctx.PromptSourceSubTurnProfile, Name: "subturn.profile"},
			Title:   "SubTurn System Instructions",
			Content: systemPrompt,
			Stable:  false,
			Cache:   agentctx.PromptCacheNone,
		},
	}
}
