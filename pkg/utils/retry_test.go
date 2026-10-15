package utils

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetry_SucceedsOnFirstAttempt(t *testing.T) {
	calls := 0
	got, err := Retry(context.Background(), RetryOptions{Attempts: 3}, func(context.Context) (string, error) {
		calls++
		return "ok", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "ok", got)
	assert.Equal(t, 1, calls)
}

func TestRetry_RetriesUntilSuccess(t *testing.T) {
	calls := 0
	got, err := Retry(context.Background(), RetryOptions{
		Attempts: 5,
		Backoff:  func(int, error) time.Duration { return 0 },
	}, func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("transient")
		}
		return 42, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, 3, calls)
}

func TestRetry_ExhaustsAttemptsAndReturnsLastError(t *testing.T) {
	calls := 0
	sentinel := errors.New("still failing")
	_, err := Retry(context.Background(), RetryOptions{
		Attempts: 3,
		Backoff:  func(int, error) time.Duration { return 0 },
	}, func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, sentinel
	})

	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 3, calls)
}

func TestRetry_StopsOnNonRetryableError(t *testing.T) {
	calls := 0
	permanent := errors.New("permanent")
	_, err := Retry(context.Background(), RetryOptions{
		Attempts:  5,
		Retryable: func(err error) bool { return !errors.Is(err, permanent) },
	}, func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, permanent
	})

	require.ErrorIs(t, err, permanent)
	assert.Equal(t, 1, calls)
}

func TestRetry_StopsOnContextCancellationBeforeAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	_, err := Retry(ctx, RetryOptions{Attempts: 3}, func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, nil
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, calls)
}

func TestRetry_StopsOnContextCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	go func() {
		// Cancel once the first attempt has failed and Retry is sleeping.
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := Retry(ctx, RetryOptions{
		Attempts: 0, // unlimited
		Backoff:  func(int, error) time.Duration { return time.Minute },
	}, func(context.Context) (struct{}, error) {
		calls++
		return struct{}{}, errors.New("boom")
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
}

func TestRetry_UnlimitedAttemptsUntilSuccess(t *testing.T) {
	calls := 0
	got, err := Retry(context.Background(), RetryOptions{
		Attempts: 0,
		Backoff:  func(int, error) time.Duration { return 0 },
	}, func(context.Context) (string, error) {
		calls++
		if calls < 4 {
			return "", errors.New("transient")
		}
		return "done", nil
	})

	require.NoError(t, err)
	assert.Equal(t, "done", got)
	assert.Equal(t, 4, calls)
}

func TestRetry_InvokesBackoffAndOnRetry(t *testing.T) {
	var attempts []int
	var delays []time.Duration
	_, _ = Retry(context.Background(), RetryOptions{
		Attempts: 3,
		Backoff: func(attempt int, _ error) time.Duration {
			attempts = append(attempts, attempt)
			return time.Duration(attempt+1) * time.Millisecond
		},
		OnRetry: func(attempt int, _ error, delay time.Duration) {
			delays = append(delays, delay)
		},
	}, func(context.Context) (struct{}, error) {
		return struct{}{}, errors.New("fail")
	})

	// Backoff is called with a 0-based attempt index; OnRetry with a 1-based one.
	assert.Equal(t, []int{0, 1}, attempts)
	assert.Equal(t, []time.Duration{time.Millisecond, 2 * time.Millisecond}, delays)
}

func TestRetryErr(t *testing.T) {
	calls := 0
	err := RetryErr(context.Background(), RetryOptions{
		Attempts: 3,
		Backoff:  func(int, error) time.Duration { return 0 },
	}, func(context.Context) error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestBackoffPolicies(t *testing.T) {
	assert.Equal(t, 3*time.Second, ConstantBackoff(3*time.Second)(0, nil))
	assert.Equal(t, 3*time.Second, ConstantBackoff(3*time.Second)(7, nil))

	linear := LinearBackoff(time.Second)
	assert.Equal(t, time.Second, linear(0, nil))
	assert.Equal(t, 3*time.Second, linear(2, nil))

	exp := ExponentialBackoff(time.Second, 8*time.Second)
	assert.Equal(t, time.Second, exp(0, nil))
	assert.Equal(t, 2*time.Second, exp(1, nil))
	assert.Equal(t, 4*time.Second, exp(2, nil))
	assert.Equal(t, 8*time.Second, exp(3, nil))
	assert.Equal(t, 8*time.Second, exp(10, nil), "capped at max")

	uncapped := ExponentialBackoff(time.Second, 0)
	assert.Equal(t, 16*time.Second, uncapped(4, nil))
}
