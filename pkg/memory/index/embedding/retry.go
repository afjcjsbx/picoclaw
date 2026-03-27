package embedding

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// HTTPError represents an HTTP error with status code.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// RetryConfig controls retry behavior for embedding operations.
type RetryConfig struct {
	MaxAttempts int           // default 3
	BaseDelay   time.Duration // default 500ms
	MaxDelay    time.Duration // default 8s
	JitterPct   float64       // default 0.2 (20%)
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   500 * time.Millisecond,
		MaxDelay:    8 * time.Second,
		JitterPct:   0.2,
	}
}

// IsRetryableError checks if an error is retryable based on common transient
// error patterns (429, 5xx, rate limit, transient).
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"429",
		"rate limit",
		"too many requests",
		"500",
		"502",
		"503",
		"504",
		"internal server error",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
		"transient",
		"temporary",
		"connection reset",
		"connection refused",
		"eof",
		"timeout",
	}
	for _, pattern := range retryablePatterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// IsRetryableStatusCode checks if an HTTP status code indicates a retryable error.
func IsRetryableStatusCode(code int) bool {
	switch code {
	case 429:
		return true
	default:
		return code >= 500 && code < 600
	}
}

// WithRetry executes fn with retry logic using exponential backoff and jitter.
// Returns the result or the last error after all attempts are exhausted.
func WithRetry[T any](ctx context.Context, cfg RetryConfig, fn func() (T, error)) (T, error) {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}

	var lastErr error
	var zero T

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// Check context before each attempt.
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		default:
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Don't retry non-retryable errors.
		if !IsRetryableError(err) {
			return zero, err
		}

		// Don't sleep after the last attempt.
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		// Calculate delay with exponential backoff.
		delay := cfg.BaseDelay * time.Duration(1<<uint(attempt))
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}

		// Apply jitter.
		if cfg.JitterPct > 0 {
			jitter := float64(delay) * cfg.JitterPct
			delta := (rand.Float64()*2 - 1) * jitter
			delay = time.Duration(float64(delay) + delta)
			if delay < 0 {
				delay = 0
			}
		}

		// Wait for delay or context cancellation.
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}

	return zero, fmt.Errorf("all %d attempts failed: %w", cfg.MaxAttempts, lastErr)
}
