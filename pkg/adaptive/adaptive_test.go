package adaptive

import (
	"context"
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func makeCandidate(provider, model string) providers.FallbackCandidate {
	return providers.FallbackCandidate{Provider: provider, Model: model}
}

func defaultAdaptiveCfg() *config.AdaptiveRoutingConfig {
	return &config.AdaptiveRoutingConfig{
		Enabled:              true,
		LocalFirstModel:      "ollama/qwen2.5-coder",
		CloudEscalationModel: "openai/gpt-4.1-mini",
		MaxEscalations:       1,
		Validation: config.AdaptiveValidationConfig{
			Mode:     "heuristic",
			MinScore: 0.75,
		},
	}
}

func TestNewRunner_NilOnEmptyLocalCandidates(t *testing.T) {
	r := NewRunner(nil, []providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"local", "cloud", defaultAdaptiveCfg(), nil)
	if r != nil {
		t.Error("expected nil runner when local candidates are empty")
	}
}

func TestNewRunner_NilOnEmptyCloudCandidates(t *testing.T) {
	r := NewRunner([]providers.FallbackCandidate{makeCandidate("ollama", "qwen")}, nil,
		"local", "cloud", defaultAdaptiveCfg(), nil)
	if r != nil {
		t.Error("expected nil runner when cloud candidates are empty")
	}
}

func TestNewRunner_DefaultMaxEscalations(t *testing.T) {
	cfg := defaultAdaptiveCfg()
	cfg.MaxEscalations = 0
	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", cfg, nil,
	)
	if r == nil {
		t.Fatal("expected non-nil runner")
	}
	if r.maxEscalations != 1 {
		t.Errorf("maxEscalations = %d, want 1", r.maxEscalations)
	}
}

func TestRun_LocalSuccess_NoEscalation(t *testing.T) {
	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", defaultAdaptiveCfg(), nil,
	)

	calls := 0
	result, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			calls++
			if model != "ollama/qwen" {
				t.Errorf("expected local model on first call, got %s", model)
			}
			return "Hello from local!", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (local only)", calls)
	}
	if result.Escalated {
		t.Error("expected no escalation")
	}
	if result.Content != "Hello from local!" {
		t.Errorf("content = %q, want %q", result.Content, "Hello from local!")
	}
	if result.LocalModel != "ollama/qwen" {
		t.Errorf("LocalModel = %q, want %q", result.LocalModel, "ollama/qwen")
	}
}

func TestRun_LocalFails_EscalatesToCloud(t *testing.T) {
	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", defaultAdaptiveCfg(), nil,
	)

	calls := 0
	result, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			calls++
			if calls == 1 {
				// Local model fails with error
				return "", 0, errors.New("connection refused")
			}
			// Cloud model succeeds
			if model != "openai/gpt-4" {
				t.Errorf("expected cloud model on second call, got %s", model)
			}
			return "Hello from cloud!", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (local + cloud)", calls)
	}
	if !result.Escalated {
		t.Error("expected escalation")
	}
	if result.Content != "Hello from cloud!" {
		t.Errorf("content = %q, want %q", result.Content, "Hello from cloud!")
	}
	if result.CloudModel != "openai/gpt-4" {
		t.Errorf("CloudModel = %q, want %q", result.CloudModel, "openai/gpt-4")
	}
}

func TestRun_LocalEmptyResponse_EscalatesToCloud(t *testing.T) {
	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", defaultAdaptiveCfg(), nil,
	)

	calls := 0
	result, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			calls++
			if calls == 1 {
				// Local model returns empty
				return "", 1, nil
			}
			return "Cloud response", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Escalated {
		t.Error("expected escalation on empty response")
	}
	if result.Content != "Cloud response" {
		t.Errorf("content = %q, want %q", result.Content, "Cloud response")
	}
}

func TestRun_BothFail_ReturnsError(t *testing.T) {
	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", defaultAdaptiveCfg(), nil,
	)

	calls := 0
	_, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			calls++
			return "", 0, errors.New("all providers down")
		},
	)

	if err == nil {
		t.Fatal("expected error when both local and cloud fail")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestRun_PassesCandidatesCorrectly(t *testing.T) {
	localCands := []providers.FallbackCandidate{
		makeCandidate("ollama", "qwen"),
		makeCandidate("ollama", "phi"),
	}
	cloudCands := []providers.FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	r := NewRunner(localCands, cloudCands, "ollama/qwen", "openai/gpt-4", defaultAdaptiveCfg(), nil)

	calls := 0
	_, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			calls++
			if calls == 1 {
				if len(candidates) != 2 || candidates[0].Provider != "ollama" {
					t.Errorf("local candidates mismatch: %+v", candidates)
				}
				return "", 0, errors.New("local fail")
			}
			if len(candidates) != 2 || candidates[0].Provider != "openai" {
				t.Errorf("cloud candidates mismatch: %+v", candidates)
			}
			return "ok", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_LLMValidationModeFallsBackToHeuristic(t *testing.T) {
	cfg := defaultAdaptiveCfg()
	cfg.Validation.Mode = "llm"

	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", cfg, nil,
	)

	// Should still work (falls back to heuristic because no provider/validator_model)
	result, err := r.Run(
		context.Background(),
		"hello",
		func(candidates []providers.FallbackCandidate, model string) (string, int, error) {
			return "Hello!", 1, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Escalated {
		t.Error("expected no escalation with good response")
	}
}

func TestRunner_Accessors(t *testing.T) {
	r := NewRunner(
		[]providers.FallbackCandidate{makeCandidate("ollama", "qwen")},
		[]providers.FallbackCandidate{makeCandidate("openai", "gpt-4")},
		"ollama/qwen", "openai/gpt-4", defaultAdaptiveCfg(), nil,
	)

	if r.LocalModel() != "ollama/qwen" {
		t.Errorf("LocalModel = %q, want %q", r.LocalModel(), "ollama/qwen")
	}
	if r.CloudModel() != "openai/gpt-4" {
		t.Errorf("CloudModel = %q, want %q", r.CloudModel(), "openai/gpt-4")
	}
}
