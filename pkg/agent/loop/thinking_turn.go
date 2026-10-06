package loop

import (
	agentcore "github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func applyTurnThinkingOptions(
	exec *turnExecution,
	agent *agentcore.AgentInstance,
	provider providers.LLMProvider,
	warnUnsupported bool,
) {
	if exec == nil || exec.llmOpts == nil {
		return
	}
	delete(exec.llmOpts, "thinking_level")
	settings := agentcore.ActiveThinkingSettings(agent, exec.activeModelConfig)
	agentID := ""
	if agent != nil {
		agentID = agent.ID
	}
	agentcore.ApplyThinkingOption(exec.llmOpts, provider, settings, warnUnsupported, agentID)
	exec.suppressReasoning = shouldSuppressReasoningFor(settings)
}

func shouldSuppressReasoningFor(settings agentcore.ThinkingSettings) bool {
	return settings.Configured && settings.Level == agentcore.ThinkingOff
}
