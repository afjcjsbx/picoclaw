package channels

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrNotRunning indicates the channel is not running.
	// Manager will not retry.
	ErrNotRunning = errors.New("channel not running")

	// ErrRateLimit indicates the platform returned a rate-limit response (e.g. HTTP 429).
	// Manager waits for the platform-requested retry window when available,
	// otherwise a default delay, and retries.
	ErrRateLimit = errors.New("rate limited")

	// ErrTemporary indicates a transient failure (e.g. network timeout, 5xx).
	// Manager will use exponential backoff and retry.
	ErrTemporary = errors.New("temporary failure")

	// ErrSendFailed indicates a permanent failure (e.g. invalid chat ID, 4xx non-429).
	// Manager will not retry.
	ErrSendFailed = errors.New("send failed")
)

// RateLimitError reports a platform rate limit together with the wait the
// platform asked for. It unwraps to ErrRateLimit so existing errors.Is checks
// keep working, and keeps the raw error for diagnostics.
type RateLimitError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return ErrRateLimit.Error()
	}
	msg := ErrRateLimit.Error()
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *RateLimitError) Unwrap() []error {
	if e == nil || e.Err == nil {
		return []error{ErrRateLimit}
	}
	return []error{ErrRateLimit, e.Err}
}

// RateLimitRetryAfter returns the wait requested by the platform for a
// rate-limit error, if one was provided.
func RateLimitRetryAfter(err error) (time.Duration, bool) {
	var rateErr *RateLimitError
	if errors.As(err, &rateErr) && rateErr.RetryAfter > 0 {
		return rateErr.RetryAfter, true
	}
	return 0, false
}
