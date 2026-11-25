package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func ptrFloat(v float64) *float64 { return &v }

func TestResolveSampling_Precedence(t *testing.T) {
	defaults := &config.AgentDefaults{MaxTokens: 1000, ContextWindow: 50000, Temperature: ptrFloat(0.3)}
	override := &config.ModelConfig{MaxTokens: 200, ContextWindow: 9000, Temperature: ptrFloat(0)}

	if got := resolveMaxTokens(defaults, override); got != 200 {
		t.Errorf("model max_tokens must win, got %d", got)
	}
	if got := resolveMaxTokens(defaults, &config.ModelConfig{}); got != 1000 {
		t.Errorf("empty model entry must inherit defaults, got %d", got)
	}
	if got := resolveMaxTokens(&config.AgentDefaults{}, nil); got != 8192 {
		t.Errorf("no config anywhere must use built-in default, got %d", got)
	}
	if got := resolveTemperature(defaults, override); got != 0 {
		t.Errorf("explicit model temperature 0 must win over defaults, got %v", got)
	}
	if got := resolveTemperature(defaults, &config.ModelConfig{}); got != 0.3 {
		t.Errorf("empty model entry must inherit defaults, got %v", got)
	}
	if got := resolveTemperature(&config.AgentDefaults{}, nil); got != 0.7 {
		t.Errorf("no config anywhere must use built-in default, got %v", got)
	}
	if got := resolveContextWindow(defaults, override, 200); got != 9000 {
		t.Errorf("model context_window must win, got %d", got)
	}
	if got := resolveContextWindow(defaults, &config.ModelConfig{}, 1000); got != 50000 {
		t.Errorf("empty model entry must inherit defaults, got %d", got)
	}
	if got := resolveContextWindow(&config.AgentDefaults{}, &config.ModelConfig{}, 512); got != 2048 {
		t.Errorf("unset everywhere must be 4x max_tokens, got %d", got)
	}
}

func TestNewAgentInstance_PrimaryModelOverridesSampling(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Workspace: t.TempDir(), ModelName: "big", MaxTokens: 1234, Temperature: ptrFloat(0.9),
		}},
		ModelList: []*config.ModelConfig{
			{ModelName: "big", Model: "openai/gpt-4o", MaxTokens: 512, Temperature: ptrFloat(0), ContextWindow: 9000},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})

	if agent.MaxTokens != 512 || agent.Temperature != 0 || agent.ContextWindow != 9000 {
		t.Fatalf("got max_tokens=%d temperature=%v context_window=%d, want 512/0/9000",
			agent.MaxTokens, agent.Temperature, agent.ContextWindow)
	}
}

func TestNewAgentInstance_NoModelOverrideKeepsGlobalDefaults(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Workspace: t.TempDir(), ModelName: "plain", MaxTokens: 1234, Temperature: ptrFloat(0.9),
		}},
		ModelList: []*config.ModelConfig{{ModelName: "plain", Model: "openai/gpt-4o"}},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})

	if agent.MaxTokens != 1234 || agent.Temperature != 0.9 || agent.ContextWindow != 1234*4 {
		t.Fatalf("got max_tokens=%d temperature=%v context_window=%d, want 1234/0.9/%d",
			agent.MaxTokens, agent.Temperature, agent.ContextWindow, 1234*4)
	}
}

func TestApplyCandidateSampling(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{MaxTokens: 1000}}}
	p := &Pipeline{Cfg: cfg}
	agent := &AgentInstance{MaxTokens: 1000, Temperature: 0.7}

	primary := &config.ModelConfig{ModelName: "primary"}
	small := &config.ModelConfig{ModelName: "small", MaxTokens: 256, Temperature: ptrFloat(0.1)}

	// A hook may rewrite max_tokens; a candidate equal to the active model must not undo it.
	opts := map[string]any{"max_tokens": 77, "temperature": 0.7}
	p.applyCandidateSampling(agent, primary, primary, opts)
	if opts["max_tokens"] != 77 {
		t.Errorf("same-model candidate must leave hook-rewritten max_tokens, got %v", opts["max_tokens"])
	}

	// A fallback with its own values replaces them.
	p.applyCandidateSampling(agent, primary, small, opts)
	if opts["max_tokens"] != 256 || opts["temperature"] != 0.1 {
		t.Errorf("fallback sampling not applied, got %v", opts)
	}

	// A fallback with no override while the active model has one gets the global default back.
	opts = map[string]any{"max_tokens": 256, "temperature": 0.1}
	p.applyCandidateSampling(agent, small, primary, opts)
	if opts["max_tokens"] != 1000 || opts["temperature"] != 0.7 {
		t.Errorf("fallback without override must fall back to global defaults, got %v", opts)
	}
}
