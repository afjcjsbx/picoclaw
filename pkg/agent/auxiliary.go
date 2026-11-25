package agent

import (
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// resolveAuxiliaryCandidates resolves the model_list entry named by an
// auxiliary slot and registers its provider in out. It returns nil, so the
// caller keeps using the agent's own model, when the slot is unset, names a
// model that is not in model_list, or names the primary model itself.
func resolveAuxiliaryCandidates(
	cfg *config.Config,
	defaults *config.AgentDefaults,
	slot, name, workspace string,
	primary []providers.FallbackCandidate,
	primaryProvider providers.LLMProvider,
	out map[string]providers.LLMProvider,
) []providers.FallbackCandidate {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	resolved := resolveModelCandidates(cfg, defaults.Provider, name, nil)
	// A name missing from model_list would otherwise resolve to a raw
	// provider/model reference, so a typo would silently hit the wrong model.
	if len(resolved) == 0 || resolved[0].ConfigIndex == 0 {
		logger.WarnCF("agent", "auxiliary model not found in model_list; using the agent's own model",
			map[string]any{"slot": slot, "model": name})
		return nil
	}
	if len(primary) > 0 {
		if resolved[0].StableKey() == primary[0].StableKey() {
			return nil
		}
		inheritPrimaryProviderForCandidates(cfg, workspace, primary[0], resolved, primaryProvider, out)
	}
	populateCandidateProvidersFromCandidates(cfg, workspace, resolved, out)
	return resolved
}

// auxTarget is where a background LLM call is sent.
type auxTarget struct {
	provider providers.LLMProvider
	model    string              // model ID as the provider expects it
	cfg      *config.ModelConfig // nil when the call runs on the agent's own model
}

// compactionTarget returns the provider and model used to summarize history:
// the auxiliary.compaction entry when one is configured and usable, else the
// agent's own model.
func (a *AgentInstance) compactionTarget(cfg *config.Config) auxTarget {
	own := auxTarget{provider: a.Provider, model: resolvedCandidateModel(a.Candidates, a.Model)}
	if cfg == nil || len(a.CompactionCandidates) == 0 {
		return own
	}
	candidate := a.CompactionCandidates[0]
	provider, err := providerForFallbackCandidate(a, a.Provider, a.Candidates, candidate)
	if err != nil {
		logger.WarnCF("agent", "auxiliary compaction model unavailable; using the agent's own model",
			map[string]any{"agent_id": a.ID, "model": candidate.Model, "error": err.Error()})
		return own
	}
	target := auxTarget{provider: provider, model: candidate.Model}
	if mc, err := resolvedCandidateModelConfig(cfg, candidate, a.Workspace); err == nil {
		target.cfg = mc
	}
	return target
}

// maxTokens returns the output budget for the call: the auxiliary entry's own
// (max_tokens, else the global default), or fallback for the agent's own model.
func (t auxTarget) maxTokens(defaults *config.AgentDefaults, fallback int) int {
	if t.cfg == nil {
		return fallback
	}
	return resolveMaxTokens(defaults, t.cfg)
}

// applyThinking sets the auxiliary entry's thinking_level on opts. It does
// nothing for the agent's own model, which keeps its existing behavior.
func (t auxTarget) applyThinking(opts map[string]any, agentID string) {
	if t.cfg == nil {
		return
	}
	applyThinkingOption(opts, t.provider, thinkingSettingsFromModelConfig(t.cfg), false, agentID)
}
