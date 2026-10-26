package agent

import (
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// promoteToPrimary switches the remaining iterations of a turn from the light
// routing tier to the agent's primary model. It is used both for the proactive
// guards (prompt size / tool intensity) and when every light candidate failed.
func (p *Pipeline) promoteToPrimary(ts *turnState, exec *turnExecution, reason string) bool {
	if p == nil || ts == nil || ts.agent == nil || exec == nil || !exec.usedLight {
		return false
	}
	if len(ts.agent.Candidates) == 0 || ts.agent.Provider == nil {
		return false
	}

	lightModel := exec.llmModelName
	candidates := append([]providers.FallbackCandidate(nil), ts.agent.Candidates...)
	model := resolvedCandidateModel(candidates, ts.agent.Model)
	primaryModelName := resolvedCandidateModelName(candidates, ts.agent.Model)
	defaultProvider := ""
	if p.Cfg != nil {
		defaultProvider = p.Cfg.Agents.Defaults.Provider
	}

	exec.activeCandidates = candidates
	exec.activeModel = model
	exec.activeModelConfig = resolveActiveModelConfig(
		p.Cfg,
		ts.agent.Workspace,
		candidates,
		model,
		defaultProvider,
	)
	exec.activeProvider = ts.agent.Provider
	exec.usedLight = false
	exec.escalatedToPrimary = true

	logger.InfoCF("agent", "Model routing: escalated to primary model", map[string]any{
		"agent_id":      ts.agent.ID,
		"turn_id":       ts.turnID,
		"from_model":    lightModel,
		"to_model":      primaryModelName,
		"reason":        reason,
		"iteration":     ts.currentIteration(),
		"prompt_tokens": exec.lastPromptTokens(),
		"tool_streak":   exec.toolIterationStreak,
	})
	return true
}

// lastPromptTokens returns the prompt-token count reported by the last light
// response, or zero when the provider did not include usage.
func (exec *turnExecution) lastPromptTokens() int {
	if exec == nil || exec.response == nil || exec.response.Usage == nil {
		return 0
	}
	return exec.response.Usage.PromptTokens
}

// maybeEscalateLightModel applies the mid-turn routing guards after a
// successful light-model response. Escalation happens at most once per turn:
// promoteToPrimary clears usedLight.
func (p *Pipeline) maybeEscalateLightModel(ts *turnState, exec *turnExecution) {
	if p == nil || ts == nil || ts.agent == nil || ts.agent.Router == nil ||
		exec == nil || exec.response == nil || !exec.usedLight {
		return
	}

	if len(exec.response.ToolCalls) == 0 {
		exec.toolIterationStreak = 0
	} else {
		exec.toolIterationStreak++
	}

	promptTokens := exec.lastPromptTokens()
	switch {
	case promptTokens > ts.agent.Router.EscalationPromptTokens():
		p.promoteToPrimary(ts, exec, "light prompt tokens exceeded escalation threshold")
	case exec.toolIterationStreak >= ts.agent.Router.EscalationToolIterations():
		p.promoteToPrimary(ts, exec, "light tool-call streak reached escalation threshold")
	}
}
