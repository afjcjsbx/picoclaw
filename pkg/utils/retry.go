// PicoClaw - Ultra-lightweight personal AI agent
//
// Shared retry/backoff helper. Several channels and the channel manager
// implemented their own "call an operation, wait, try again" loops with
// duplicated cancellation, backoff and logging logic. Retry centralizes that so
// the semantics (max attempts, backoff curve, cancellation, non-retryable
// errors) stay consistent across the codebase.

package utils

import (
	"context"
	"time"
)

// BackoffFunc returns how long to wait before the next attempt after the given
// 0-based attempt failed with err. Returning a non-positive duration retries
// immediately.
type BackoffFunc func(attempt int, err error) time.Duration

// RetryOptions configures Retry.
type RetryOptions struct {
	// Attempts is the maximum number of attempts, including the first one. A
	// value <= 0 means "retry until success, a non-retryable error, or context
	// cancellation".
	Attempts int

	// Backoff returns the delay before the next attempt. If nil, retries happen
	// immediately.
	Backoff BackoffFunc

	// Retryable reports whether err should be retried. If nil, every error is
	// retried until Attempts is exhausted (or until cancellation when
	// Attempts <= 0).
	Retryable func(err error) bool

	// OnRetry is invoked before waiting, e.g. for logging/observability. The
	// attempt argument is 1-based (the attempt that just failed). Optional.
	OnRetry func(attempt int, err error, delay time.Duration)
}

// Retry runs fn until it succeeds, returns a non-retryable error, or exhausts
// opts.Attempts. It stops immediately when ctx is canceled, returning ctx.Err().
// On success it returns fn's result; otherwise it returns the zero value and the
// last error.
func Retry[T any](
	ctx context.Context,
	opts RetryOptions,
	fn func(ctx context.Context) (T, error),
) (T, error) {
	var zero T

	retryable := opts.Retryable
	if retryable == nil {
		retryable = func(error) bool { return true }
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}

		result, err := fn(ctx)
		if err == nil {
			return result, nil
		}

		if opts.Attempts > 0 && attempt+1 >= opts.Attempts {
			return zero, err
		}
		if !retryable(err) {
			return zero, err
		}

		var delay time.Duration
		if opts.Backoff != nil {
			delay = opts.Backoff(attempt, err)
		}
		if opts.OnRetry != nil {
			opts.OnRetry(attempt+1, err, delay)
		}

		if delay <= 0 {
			continue
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

// RetryErr is Retry for operations that return no value.
func RetryErr(ctx context.Context, opts RetryOptions, fn func(ctx context.Context) error) error {
	_, err := Retry(ctx, opts, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	return err
}

// ConstantBackoff waits delay between every attempt.
func ConstantBackoff(delay time.Duration) BackoffFunc {
	return func(int, error) time.Duration { return delay }
}

// LinearBackoff waits base, 2*base, 3*base, ... between attempts.
func LinearBackoff(base time.Duration) BackoffFunc {
	return func(attempt int, _ error) time.Duration {
		return time.Duration(attempt+1) * base
	}
}

// ExponentialBackoff waits base, 2*base, 4*base, ... between attempts, capped at
// maxDelay. A maxDelay <= 0 disables the cap.
func ExponentialBackoff(base, maxDelay time.Duration) BackoffFunc {
	return func(attempt int, _ error) time.Duration {
		delay := base
		for i := 0; i < attempt; i++ {
			if maxDelay > 0 && delay >= maxDelay {
				return maxDelay
			}
			delay *= 2
		}
		if maxDelay > 0 && delay > maxDelay {
			return maxDelay
		}
		return delay
	}
}
