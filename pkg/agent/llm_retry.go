// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"net/http"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// llmRetry is how many times, and how patiently, an LLM call is retried after a
// transient upstream failure. It comes from the same settings as the main
// loop's retries (max_llm_retries, llm_retry_backoff_secs).
type llmRetry struct {
	max     int           // retries after the first attempt
	backoff time.Duration // wait before retry n is n * backoff
}

func llmRetryFromConfig(cfg *config.Config) llmRetry {
	retry := llmRetry{max: 2, backoff: 2 * time.Second}
	if cfg == nil {
		return retry
	}
	if n := cfg.Agents.Defaults.MaxLLMRetries; n > 0 {
		retry.max = n
	}
	if secs := cfg.Agents.Defaults.LLMRetryBackoffSecs; secs > 0 {
		retry.backoff = time.Duration(secs) * time.Second
	}
	return retry
}

// isOverloadStatus reports whether err is an upstream 429 (rate limited), 502
// (bad gateway) or 503 (overloaded): failures that usually clear on their own.
func isOverloadStatus(err error) bool {
	failErr := providers.ClassifyError(err, "", "")
	if failErr == nil {
		return false
	}
	switch failErr.Status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	}
	return false
}

// chatRetryingOverload calls provider.Chat and retries while the upstream
// answers 429, 502 or 503, waiting backoff, 2*backoff, ... between attempts and
// giving up early when ctx is canceled. Any other outcome is returned as is.
// It is meant for the background requests (summarization) that have no retry
// loop of their own; the main loop classifies and retries errors itself.
func chatRetryingOverload(
	ctx context.Context,
	provider providers.LLMProvider,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
	retry llmRetry,
) (*providers.LLMResponse, error) {
	for attempt := 0; ; attempt++ {
		resp, err := provider.Chat(ctx, messages, toolDefs, model, opts)
		if err == nil || attempt >= retry.max || !isOverloadStatus(err) {
			return resp, err
		}
		wait := time.Duration(attempt+1) * retry.backoff
		logger.WarnCF("agent", "Upstream overloaded or rate limited, retrying after backoff", map[string]any{
			"model":   model,
			"attempt": attempt + 1,
			"backoff": wait.String(),
			"error":   err.Error(),
		})
		if sleepErr := sleepWithContext(ctx, wait); sleepErr != nil {
			return nil, sleepErr
		}
	}
}
