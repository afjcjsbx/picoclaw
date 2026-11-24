package agent

import "github.com/sipeed/picoclaw/pkg/config"

const (
	defaultMaxTokens   = 8192
	defaultTemperature = 0.7
)

// resolveMaxTokens returns the max output tokens for a model: the model entry's
// own max_tokens, else agents.defaults.max_tokens, else the built-in default.
func resolveMaxTokens(defaults *config.AgentDefaults, mc *config.ModelConfig) int {
	if mc != nil && mc.MaxTokens > 0 {
		return mc.MaxTokens
	}
	if defaults != nil && defaults.MaxTokens > 0 {
		return defaults.MaxTokens
	}
	return defaultMaxTokens
}

// resolveTemperature follows the same precedence as resolveMaxTokens. A nil
// pointer means "unset" so an explicit temperature of 0 is honored at either level.
func resolveTemperature(defaults *config.AgentDefaults, mc *config.ModelConfig) float64 {
	if mc != nil && mc.Temperature != nil {
		return *mc.Temperature
	}
	if defaults != nil && defaults.Temperature != nil {
		return *defaults.Temperature
	}
	return defaultTemperature
}

// resolveContextWindow returns the context window for a model: the model entry's
// own context_window, else agents.defaults.context_window, else 4x maxTokens.
//
// Most models have context windows well above their output limits (e.g. GPT-4o
// 128k ctx / 16k out, other models 200k ctx / 8k out). 4x is a conservative
// lower bound that avoids premature summarization while remaining safe: the
// reactive forceCompression handles any overshoot.
func resolveContextWindow(defaults *config.AgentDefaults, mc *config.ModelConfig, maxTokens int) int {
	if mc != nil && mc.ContextWindow > 0 {
		return mc.ContextWindow
	}
	if defaults != nil && defaults.ContextWindow > 0 {
		return defaults.ContextWindow
	}
	return maxTokens * 4
}

// llmSampling returns the max_tokens and temperature for a request to the model
// described by mc. Without a model config (injected providers) the agent's own
// values apply.
func (a *AgentInstance) llmSampling(cfg *config.Config, mc *config.ModelConfig) (int, float64) {
	if cfg == nil || mc == nil {
		return a.MaxTokens, a.Temperature
	}
	return resolveMaxTokens(&cfg.Agents.Defaults, mc), resolveTemperature(&cfg.Agents.Defaults, mc)
}

// applyCandidateSampling swaps max_tokens and temperature in callOpts when a
// fallback candidate resolves to different values than the model the turn
// started on. Candidates that match the active model leave callOpts untouched,
// so values rewritten by a BeforeLLM hook survive for them.
func (p *Pipeline) applyCandidateSampling(
	agent *AgentInstance,
	active, candidate *config.ModelConfig,
	callOpts map[string]any,
) {
	if candidate == nil {
		return
	}
	activeMax, activeTemp := agent.llmSampling(p.Cfg, active)
	candMax, candTemp := agent.llmSampling(p.Cfg, candidate)
	if candMax != activeMax {
		callOpts["max_tokens"] = candMax
	}
	if candTemp != activeTemp {
		callOpts["temperature"] = candTemp
	}
}
