// Package adaptive implements Adaptive Model Routing — an opt-in feature that
// runs a cheap/local model first, validates the outcome, and automatically
// escalates to a cloud model on failure.
//
// This is orthogonal to the failover mechanism (which handles provider errors)
// and to the routing feature (which pre-routes based on message complexity).
// Adaptive routing validates _outcome quality_, not provider health.
package adaptive

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// RunFunc executes a full LLM iteration with the given candidates and model.
// It returns the final content, iteration count, and any error.
// The adaptive runner calls this function once for the local model attempt and,
// if escalation is needed, once more for the cloud model attempt.
type RunFunc func(
	candidates []providers.FallbackCandidate,
	model string,
) (content string, iterations int, err error)

// Result holds the outcome of an adaptive routing attempt.
type Result struct {
	Content        string
	Iterations     int
	Escalated      bool   // true if cloud model was used
	LocalModel     string // model used for local attempt
	CloudModel     string // model used for cloud attempt (empty if not escalated)
	ValidationInfo string // reason from the validator
}

// Runner orchestrates adaptive model routing. It is safe for concurrent use
// from multiple goroutines (all state is in the config, which is read-only).
type Runner struct {
	localCandidates []providers.FallbackCandidate
	cloudCandidates []providers.FallbackCandidate
	localModel      string
	cloudModel      string
	maxEscalations  int
	validator       Validator
}

// NewRunner creates an adaptive runner from resolved candidates and config.
// Returns nil if the configuration is invalid (e.g. no candidates resolved).
// The provider parameter is used only when LLM validation mode is configured;
// it may be nil for heuristic-only usage.
func NewRunner(
	localCandidates []providers.FallbackCandidate,
	cloudCandidates []providers.FallbackCandidate,
	localModel string,
	cloudModel string,
	cfg *config.AdaptiveRoutingConfig,
	provider providers.LLMProvider,
) *Runner {
	if len(localCandidates) == 0 || len(cloudCandidates) == 0 {
		return nil
	}

	maxEsc := cfg.MaxEscalations
	if maxEsc <= 0 {
		maxEsc = 1
	}

	validator := newValidator(cfg, provider)

	return &Runner{
		localCandidates: localCandidates,
		cloudCandidates: cloudCandidates,
		localModel:      localModel,
		cloudModel:      cloudModel,
		maxEscalations:  maxEsc,
		validator:       validator,
	}
}

// newValidator creates the appropriate Validator based on config.
func newValidator(cfg *config.AdaptiveRoutingConfig, provider providers.LLMProvider) Validator {
	minScore := cfg.Validation.MinScore
	if minScore <= 0 {
		minScore = defaultMinScore
	}

	switch cfg.Validation.Mode {
	case "llm":
		if cfg.Validation.ValidatorModel == "" {
			logger.WarnCF("adaptive", "LLM validation mode requires validator_model, falling back to heuristic", nil)
			return NewHeuristicValidator(minScore)
		}
		llmVal := NewLLMValidator(provider, cfg.Validation.ValidatorModel, cfg.Validation)
		if llmVal == nil {
			logger.WarnCF(
				"adaptive",
				"LLM validator could not be created (nil provider?), falling back to heuristic",
				nil,
			)
			return NewHeuristicValidator(minScore)
		}
		logger.InfoCF("adaptive", "LLM validation mode enabled", map[string]any{
			"validator_model":       cfg.Validation.ValidatorModel,
			"min_score":             minScore,
			"max_tool_output_chars": cfg.Validation.MaxToolOutputChars,
			"max_assistant_chars":   cfg.Validation.MaxAssistantChars,
			"redact_secrets":        cfg.Validation.RedactSecrets,
		})
		return llmVal
	default:
		// "heuristic" or empty string
		return NewHeuristicValidator(minScore)
	}
}

// Run executes adaptive model routing:
//  1. Run with local model candidates.
//  2. Validate the outcome.
//  3. If validation passes, return the local result.
//  4. If validation fails, discard the local result and re-run with cloud candidates.
//  5. If the cloud run fails (provider error), the normal failover chain handles it.
//
// The ctx and userMessage parameters are forwarded to the validator. The
// heuristic validator ignores them; the LLM validator uses them to build the
// validation prompt.
func (r *Runner) Run(ctx context.Context, userMessage string, runFn RunFunc) (*Result, error) {
	// Phase 1: Local model attempt.
	logger.InfoCF("adaptive", "Attempting local model", map[string]any{
		"model":      r.localModel,
		"candidates": len(r.localCandidates),
	})

	localContent, localIter, localErr := runFn(r.localCandidates, r.localModel)

	// Build a synthetic LLMResponse for the validator from the run result.
	localResp := buildResponseForValidation(localContent, localErr)

	vr := r.validator.Validate(ctx, localResp, localErr, userMessage)

	logger.InfoCF("adaptive", "Local model validation result", map[string]any{
		"model":  r.localModel,
		"score":  vr.Score,
		"passed": vr.Passed,
		"reason": vr.Reason,
	})

	if vr.Passed {
		return &Result{
			Content:        localContent,
			Iterations:     localIter,
			Escalated:      false,
			LocalModel:     r.localModel,
			ValidationInfo: vr.Reason,
		}, nil
	}

	// Phase 2: Escalation to cloud model.
	// The local result is discarded — we re-run from scratch.
	logger.InfoCF("adaptive", "Escalating to cloud model", map[string]any{
		"local_model":  r.localModel,
		"cloud_model":  r.cloudModel,
		"local_score":  vr.Score,
		"local_reason": vr.Reason,
		"candidates":   len(r.cloudCandidates),
	})

	cloudContent, cloudIter, cloudErr := runFn(r.cloudCandidates, r.cloudModel)
	if cloudErr != nil {
		return nil, fmt.Errorf("adaptive: cloud escalation failed: %w", cloudErr)
	}

	return &Result{
		Content:        cloudContent,
		Iterations:     cloudIter,
		Escalated:      true,
		LocalModel:     r.localModel,
		CloudModel:     r.cloudModel,
		ValidationInfo: fmt.Sprintf("local failed (%s), escalated to cloud", vr.Reason),
	}, nil
}

// LocalModel returns the configured local model name.
func (r *Runner) LocalModel() string { return r.localModel }

// CloudModel returns the configured cloud escalation model name.
func (r *Runner) CloudModel() string { return r.cloudModel }

// buildResponseForValidation creates a minimal LLMResponse from the runFn output
// so the validator can score it using the same interface.
func buildResponseForValidation(content string, err error) *providers.LLMResponse {
	if err != nil {
		return nil
	}
	return &providers.LLMResponse{
		Content:      content,
		FinishReason: "stop",
	}
}
