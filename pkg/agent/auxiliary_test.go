package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

// auxRecorder answers "summary" and remembers what it was asked.
type auxRecorder struct {
	mu     sync.Mutex
	models []string
	opts   []map[string]any
}

func (p *auxRecorder) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.models = append(p.models, model)
	p.opts = append(p.opts, opts)
	return &providers.LLMResponse{Content: "summary", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *auxRecorder) GetDefaultModel() string { return "recording-mock" }

func auxTestConfig(t *testing.T, aux config.AuxiliaryModels) *config.Config {
	t.Helper()
	return &config.Config{
		Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
			Workspace:         t.TempDir(),
			ModelName:         "primary",
			MaxTokens:         1000,
			ContextWindow:     20000,
			MaxToolIterations: 10,
			Auxiliary:         aux,
		}},
		ModelList: []*config.ModelConfig{
			{ModelName: "primary", Model: "openai/gpt-4o", APIKeys: config.SimpleSecureStrings("k")},
			{
				ModelName:     "cheap",
				Model:         "openai/gpt-4o-mini",
				APIKeys:       config.SimpleSecureStrings("k"),
				MaxTokens:     300,
				ThinkingLevel: "off",
			},
			{ModelName: "vision-model", Model: "openai/gpt-4o-vision", APIKeys: config.SimpleSecureStrings("k")},
		},
	}
}

func compactAgentHistory(t *testing.T, al *AgentLoop) {
	t.Helper()
	agent := al.registry.GetDefaultAgent()
	if err := agent.Sessions.SetHistory(context.Background(), "aux", alternatingHistory(3, "hello")); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	if err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "aux",
		Reason:     ContextCompressReasonManual,
	}); err != nil {
		t.Fatalf("Compact: %v", err)
	}
}

func TestAuxiliaryCompaction_RoutesSummarizationToConfiguredModel(t *testing.T) {
	primary, aux := &auxRecorder{}, &auxRecorder{}
	cfg := auxTestConfig(t, config.AuxiliaryModels{Compaction: "cheap"})
	al := NewAgentLoop(cfg, bus.NewMessageBus(), primary)
	agent := al.registry.GetDefaultAgent()
	if len(agent.CompactionCandidates) != 1 {
		t.Fatalf("CompactionCandidates = %d, want 1", len(agent.CompactionCandidates))
	}
	agent.CandidateProviders[candidateProviderKey(agent.CompactionCandidates[0])] = aux

	compactAgentHistory(t, al)

	if len(primary.models) != 0 {
		t.Fatalf("primary model was called %d times, want 0", len(primary.models))
	}
	if len(aux.models) == 0 || aux.models[0] != "gpt-4o-mini" {
		t.Fatalf("auxiliary models = %v, want gpt-4o-mini", aux.models)
	}
	if got := aux.opts[0]["max_tokens"]; got != 300 {
		t.Errorf("max_tokens = %v, want the auxiliary entry's own 300", got)
	}
	if got := aux.opts[0]["thinking_level"]; got != "off" {
		t.Errorf("thinking_level = %v, want the auxiliary entry's own off", got)
	}
}

func TestRetryLLMCallUsesStricterCheckpointOrTargetCap(t *testing.T) {
	tests := []struct {
		name       string
		callerCap  int
		targetCap  int
		wantTokens int
	}{
		{name: "checkpoint cap wins", callerCap: 300, targetCap: 500, wantTokens: 300},
		{name: "auxiliary cap wins", callerCap: 500, targetCap: 300, wantTokens: 300},
		{name: "auxiliary inherits larger default", callerCap: 400, targetCap: 0, wantTokens: 400},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary, aux := &auxRecorder{}, &auxRecorder{}
			cfg := auxTestConfig(t, config.AuxiliaryModels{Compaction: "cheap"})
			cfg.ModelList[1].MaxTokens = tt.targetCap
			al := NewAgentLoop(cfg, bus.NewMessageBus(), primary)
			agent := al.registry.GetDefaultAgent()
			agent.CandidateProviders[candidateProviderKey(agent.CompactionCandidates[0])] = aux
			lcm := &legacyContextManager{al: al}

			if _, err := lcm.retryLLMCall(context.Background(), agent, "summarize", 1, tt.callerCap); err != nil {
				t.Fatalf("retryLLMCall: %v", err)
			}
			if len(primary.models) != 0 {
				t.Fatalf("primary model was called %d times, want 0", len(primary.models))
			}
			if len(aux.opts) != 1 {
				t.Fatalf("auxiliary calls = %d, want 1", len(aux.opts))
			}
			if got := aux.opts[0]["max_tokens"]; got != tt.wantTokens {
				t.Errorf("max_tokens = %v, want %d", got, tt.wantTokens)
			}
			if got := aux.opts[0]["thinking_level"]; got != "off" {
				t.Errorf("thinking_level = %v, want off", got)
			}
		})
	}
}

func TestAuxiliaryCompaction_UnsetKeepsAgentModel(t *testing.T) {
	primary := &auxRecorder{}
	al := NewAgentLoop(auxTestConfig(t, config.AuxiliaryModels{}), bus.NewMessageBus(), primary)
	agent := al.registry.GetDefaultAgent()
	if n := len(agent.CompactionCandidates); n != 0 {
		t.Fatalf("CompactionCandidates = %d, want 0", n)
	}

	compactAgentHistory(t, al)

	if len(primary.models) == 0 || primary.models[0] != "gpt-4o" {
		t.Fatalf("primary models = %v, want gpt-4o", primary.models)
	}
	if got := primary.opts[0]["max_tokens"]; got != summaryMaxTokens(agent) {
		t.Errorf("max_tokens = %v, want the checkpoint cap %d", got, summaryMaxTokens(agent))
	}
	if _, ok := primary.opts[0]["thinking_level"]; ok {
		t.Error("an unset slot must not add thinking_level to summarization calls")
	}
}

func TestAuxiliaryCompaction_UnusableSlotFallsBackToAgentModel(t *testing.T) {
	for name, slot := range map[string]string{
		"unknown model":    "does-not-exist",
		"the primary self": "primary",
	} {
		t.Run(name, func(t *testing.T) {
			primary := &auxRecorder{}
			cfg := auxTestConfig(t, config.AuxiliaryModels{Compaction: slot})
			al := NewAgentLoop(cfg, bus.NewMessageBus(), primary)
			agent := al.registry.GetDefaultAgent()
			if n := len(agent.CompactionCandidates); n != 0 {
				t.Fatalf("CompactionCandidates = %d, want 0", n)
			}

			compactAgentHistory(t, al)

			if len(primary.models) == 0 {
				t.Fatal("summarization must still run on the agent's own model")
			}
			if got := primary.opts[0]["max_tokens"]; got != summaryMaxTokens(agent) {
				t.Errorf("max_tokens = %v, want the checkpoint cap %d", got, summaryMaxTokens(agent))
			}
			if _, ok := primary.opts[0]["thinking_level"]; ok {
				t.Error("an unusable slot must not add thinking_level to the agent's own model")
			}
		})
	}
}

func TestAuxiliaryCompaction_MissingProviderFallsBackToAgentModel(t *testing.T) {
	primary := &auxRecorder{}
	cfg := auxTestConfig(t, config.AuxiliaryModels{Compaction: "cheap"})
	al := NewAgentLoop(cfg, bus.NewMessageBus(), primary)
	agent := al.registry.GetDefaultAgent()
	delete(agent.CandidateProviders, candidateProviderKey(agent.CompactionCandidates[0]))

	compactAgentHistory(t, al)

	if len(primary.models) == 0 {
		t.Fatal("summarization must fall back to the agent's own model")
	}
	if got := primary.opts[0]["max_tokens"]; got != summaryMaxTokens(agent) {
		t.Errorf("max_tokens = %v, want the checkpoint cap %d", got, summaryMaxTokens(agent))
	}
	if _, ok := primary.opts[0]["thinking_level"]; ok {
		t.Error("a missing auxiliary provider must not leak its thinking_level to the agent's own model")
	}
}

func TestAuxiliaryVision_TakesPrecedenceOverImageModel(t *testing.T) {
	tests := []struct {
		name       string
		imageModel string
		aux        string
		want       string
	}{
		{"legacy image_model only", "vision-model", "", "gpt-4o-vision"},
		{"auxiliary.vision only", "", "vision-model", "gpt-4o-vision"},
		{"auxiliary.vision wins", "cheap", "vision-model", "gpt-4o-vision"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := auxTestConfig(t, config.AuxiliaryModels{Vision: tt.aux})
			cfg.Agents.Defaults.ImageModel = tt.imageModel

			agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &auxRecorder{})

			if len(agent.ImageCandidates) != 1 || agent.ImageCandidates[0].Model != tt.want {
				t.Fatalf("ImageCandidates = %+v, want one candidate for %s", agent.ImageCandidates, tt.want)
			}
		})
	}
}

func TestProviderToCompleteFnWithOptions_MergesExtraOptions(t *testing.T) {
	rec := &auxRecorder{}
	complete := providerToCompleteFnWithOptions(rec, "m", llmRetry{}, map[string]any{"thinking_level": "off"})

	if _, err := complete(context.Background(), "summarize", seahorse.CompleteOptions{MaxTokens: 512}); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if got := rec.opts[0]["thinking_level"]; got != "off" {
		t.Errorf("thinking_level = %v, want off", got)
	}
	if got := rec.opts[0]["max_tokens"]; got != 512 {
		t.Errorf("max_tokens = %v, want seahorse's own 512", got)
	}
}
