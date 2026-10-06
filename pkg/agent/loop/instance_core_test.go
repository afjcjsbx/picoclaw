package loop

import (
	"testing"

	agentcore "github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestProviderForFallbackCandidateRejectsMissingCrossProvider(t *testing.T) {
	agent := &agentcore.AgentInstance{CandidateProviders: map[string]providers.LLMProvider{}}
	activeProvider := &mockProvider{}
	activeCandidates := []providers.FallbackCandidate{{Provider: "openai", Model: "gpt-4o"}}

	provider, err := providerForFallbackCandidate(
		agent,
		activeProvider,
		activeCandidates,
		providers.FallbackCandidate{Provider: "openrouter", Model: "new-model"},
	)
	if err == nil || provider != nil {
		t.Fatalf("provider = %T, error = %v, want missing cross-provider error", provider, err)
	}
}

func TestProviderForConfiguredFallbackRejectsMissingProvider(t *testing.T) {
	primary := providers.FallbackCandidate{Provider: "openai", Model: "primary", ConfigKey: "config:primary"}
	fallback := providers.FallbackCandidate{Provider: "openai", Model: "backup", ConfigKey: "config:backup"}
	provider, err := providerForFallbackCandidate(
		&agentcore.AgentInstance{CandidateProviders: map[string]providers.LLMProvider{}},
		&mockProvider{},
		[]providers.FallbackCandidate{primary, fallback},
		fallback,
	)
	if err == nil || provider != nil {
		t.Fatalf("provider = %T, error = %v, want configured provider initialization error", provider, err)
	}
}

func TestApplyBeforeLLMModelRewriteSwitchesProvider(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Provider: "openai"}},
		ModelList: []*config.ModelConfig{{
			ModelName: "hook-model",
			Provider:  "anthropic",
			Model:     "claude-sonnet",
			APIKeys:   config.SimpleSecureStrings("sk-ant-test"),
		}},
	}
	candidate, ok := agentcore.ResolveModelCandidate(cfg, "openai", "hook-model")
	if !ok {
		t.Fatal("resolveModelCandidate() did not resolve hook model")
	}
	replacement := &mockProvider{}
	agent := &agentcore.AgentInstance{
		Workspace: t.TempDir(),
		CandidateProviders: map[string]providers.LLMProvider{
			agentcore.CandidateProviderKey(candidate): replacement,
		},
	}
	exec := &turnExecution{llmModel: "hook-model", activeProvider: &mockProvider{}}
	pipeline := &Pipeline{Cfg: cfg}

	if err := pipeline.applyBeforeLLMModelRewrite(&turnState{agent: agent}, exec); err != nil {
		t.Fatalf("applyBeforeLLMModelRewrite() error = %v", err)
	}
	if exec.activeProvider != replacement {
		t.Fatalf("active provider = %T, want hook-selected candidate provider", exec.activeProvider)
	}
	if exec.activeModel != "claude-sonnet" {
		t.Fatalf("active model = %q, want %q", exec.activeModel, "claude-sonnet")
	}
}
