// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
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

// wait is the pause before retry number retry (0-based): backoff, 2*backoff, ...
func (r llmRetry) wait(retry int) time.Duration {
	return time.Duration(retry+1) * r.backoff
}

// chatRetryingTransient calls provider.Chat and retries while the failure is
// transient according to transientLLMRetryReason (rate limits, 5xx, timeouts,
// network errors), the same classification the main loop uses. It waits
// retry.wait between attempts and gives up early when ctx is canceled. Any other
// outcome is returned as is. It is meant for the background requests
// (summarization) that have no retry loop of their own.
func chatRetryingTransient(
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
		if err == nil || attempt >= retry.max {
			return resp, err
		}
		reason, transient := transientLLMRetryReason(err)
		if !transient {
			return resp, err
		}
		wait := retry.wait(attempt)
		logger.WarnCF("agent", "Transient LLM error, retrying after backoff", map[string]any{
			"model":   model,
			"reason":  reason,
			"attempt": attempt + 1,
			"backoff": wait.String(),
			"error":   err.Error(),
		})
		if sleepErr := sleepWithContext(ctx, wait); sleepErr != nil {
			return nil, sleepErr
		}
	}
}
