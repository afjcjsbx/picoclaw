package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type resolvedAgentModel struct {
	name               string
	provider           providers.LLMProvider
	candidates         []providers.FallbackCandidate
	candidateProviders map[string]providers.LLMProvider
	thinkingLevel      ThinkingLevel
	thinkingConfigured bool
}

type sessionModelKey struct{ agentID, sessionKey string }

// sessionModelOverride owns only providers it creates; its AgentInstance copy
// shares tools and session storage with the configured agent.
type sessionModelOverride struct {
	model   *resolvedAgentModel
	refs    int
	retired bool
}

func resolveAgentModelSelection(cfg *config.Config, agent *AgentInstance, value string) (*resolvedAgentModel, error) {
	value = strings.TrimSpace(value)
	if cfg == nil || agent == nil {
		return nil, fmt.Errorf("runtime control is unavailable")
	}
	for _, modelCfg := range cfg.ModelList {
		if modelCfg != nil && modelCfg.ModelName == value {
			candidates := resolveModelCandidates(cfg, cfg.Agents.Defaults.Provider, value, agent.Fallbacks)
			if len(candidates) == 0 {
				return nil, fmt.Errorf("model %q did not resolve to any provider candidates", value)
			}
			primaryCfg, err := resolvedCandidateModelConfig(cfg, candidates[0], agent.Workspace)
			if err != nil {
				return nil, err
			}
			provider, _, err := providers.CreateProviderFromConfig(primaryCfg)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize model %q: %w", value, err)
			}
			return &resolvedAgentModel{
				name:               value,
				provider:           provider,
				candidates:         candidates,
				thinkingLevel:      parseThinkingLevel(primaryCfg.ThinkingLevel),
				thinkingConfigured: isConfiguredThinkingLevel(primaryCfg.ThinkingLevel),
			}, nil
		}
	}
	return nil, fmt.Errorf("model %q not found in model_list or providers", value)
}

func newSessionModelOverride(cfg *config.Config, agent *AgentInstance, value string) (*sessionModelOverride, error) {
	model, err := resolveAgentModelSelection(cfg, agent, value)
	if err != nil {
		return nil, err
	}
	model.candidateProviders = make(map[string]providers.LLMProvider)
	if len(model.candidates) > 1 {
		inheritPrimaryProviderForCandidates(
			cfg, agent.Workspace, model.candidates[0], model.candidates[1:], model.provider,
			model.candidateProviders,
		)
		populateCandidateProvidersFromCandidates(cfg, agent.Workspace, model.candidates[1:], model.candidateProviders)
	}
	for _, candidates := range [][]providers.FallbackCandidate{agent.ImageCandidates, agent.LightCandidates} {
		if len(model.candidates) > 0 {
			inheritPrimaryProviderForCandidates(
				cfg, agent.Workspace, model.candidates[0], candidates, model.provider,
				model.candidateProviders,
			)
		}
		populateCandidateProvidersFromCandidates(cfg, agent.Workspace, candidates, model.candidateProviders)
	}
	return &sessionModelOverride{model: model}, nil
}

func (o *sessionModelOverride) apply(agent *AgentInstance) *AgentInstance {
	if o == nil || o.model == nil || agent == nil {
		return agent
	}
	modelMu := agent.modelStateMutex()
	modelMu.RLock()
	clone := *agent
	modelMu.RUnlock()

	clone.modelMu = &sync.RWMutex{}
	clone.Model = o.model.name
	clone.Provider = o.model.provider
	clone.Candidates = append([]providers.FallbackCandidate(nil), o.model.candidates...)
	clone.CandidateProviders = o.model.candidateProviders
	clone.ThinkingLevel = o.model.thinkingLevel
	clone.ThinkingLevelConfigured = o.model.thinkingConfigured
	return &clone
}

func (o *sessionModelOverride) close() {
	if o == nil || o.model == nil {
		return
	}
	providersToClose := make([]providers.LLMProvider, 0, 1+len(o.model.candidateProviders))
	providersToClose = append(providersToClose, o.model.provider)
	for _, provider := range o.model.candidateProviders {
		providersToClose = append(providersToClose, provider)
	}
	closeUniqueStatefulProviders(providersToClose...)
}

func (al *AgentLoop) setSessionModelPreset(agentID, sessionKey string, agent *AgentInstance, value string) error {
	if agent == nil || strings.TrimSpace(sessionKey) == "" {
		return fmt.Errorf("session context is unavailable")
	}
	if agentID == "" {
		agentID = agent.ID
	}
	override, err := newSessionModelOverride(al.GetConfig(), agent, value)
	if err != nil {
		return err
	}
	key := sessionModelKey{agentID: agentID, sessionKey: sessionKey}
	al.sessionModelsMu.Lock()
	if al.sessionModels == nil {
		al.sessionModels = make(map[sessionModelKey]*sessionModelOverride)
	}
	previous := al.sessionModels[key]
	al.sessionModels[key] = override
	closePrevious := retireSessionModelOverride(previous)
	al.sessionModelsMu.Unlock()
	if closePrevious {
		previous.close()
	}
	return nil
}

func (al *AgentLoop) acquireSessionModelOverride(key sessionModelKey) *sessionModelOverride {
	al.sessionModelsMu.Lock()
	defer al.sessionModelsMu.Unlock()
	override := al.sessionModels[key]
	if override != nil {
		override.refs++
	}
	return override
}

func (al *AgentLoop) releaseSessionModelOverride(override *sessionModelOverride) {
	al.sessionModelsMu.Lock()
	if override.refs > 0 {
		override.refs--
	}
	closeOverride := override.retired && override.refs == 0
	al.sessionModelsMu.Unlock()
	if closeOverride {
		override.close()
	}
}

// sessionModel returns the model a session selected through model_preset, or
// nil when the session follows the agent's model. The result is immutable.
func (al *AgentLoop) sessionModel(key sessionModelKey) *resolvedAgentModel {
	al.sessionModelsMu.Lock()
	defer al.sessionModelsMu.Unlock()
	if override := al.sessionModels[key]; override != nil {
		return override.model
	}
	return nil
}

// clearSessionModelOverride drops a session's model_preset so the agent's
// model applies again; in-flight turns keep the override until they release it.
func (al *AgentLoop) clearSessionModelOverride(key sessionModelKey) {
	al.sessionModelsMu.Lock()
	override := al.sessionModels[key]
	delete(al.sessionModels, key)
	closeOverride := retireSessionModelOverride(override)
	al.sessionModelsMu.Unlock()
	if closeOverride {
		override.close()
	}
}

func (al *AgentLoop) clearSessionModelOverrides() {
	al.sessionModelsMu.Lock()
	var closeOverrides []*sessionModelOverride
	for key, override := range al.sessionModels {
		delete(al.sessionModels, key)
		if retireSessionModelOverride(override) {
			closeOverrides = append(closeOverrides, override)
		}
	}
	al.sessionModelsMu.Unlock()
	for _, override := range closeOverrides {
		override.close()
	}
}

func retireSessionModelOverride(override *sessionModelOverride) bool {
	if override == nil {
		return false
	}
	override.retired = true
	return override.refs == 0
}
