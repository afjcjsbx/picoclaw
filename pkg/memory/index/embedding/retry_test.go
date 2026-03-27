package embedding

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestIsRetryableStatusCode(t *testing.T) {
	tests := []struct {
		code int
		want bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{403, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
	}
	for _, tt := range tests {
		if got := IsRetryableStatusCode(tt.code); got != tt.want {
			t.Errorf("IsRetryableStatusCode(%d) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestWithRetry_Success(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		JitterPct:   0.2,
	}

	calls := 0
	result, err := WithRetry(context.Background(), cfg, func() (string, error) {
		calls++
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("got %q, want %q", result, "ok")
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWithRetry_EventualSuccess(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		JitterPct:   0.0,
	}

	calls := 0
	result, err := WithRetry(context.Background(), cfg, func() (string, error) {
		calls++
		if calls < 3 {
			return "", &HTTPError{StatusCode: http.StatusTooManyRequests, Message: "rate limited"}
		}
		return "ok", nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("got %q, want %q", result, "ok")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetry_AllFail(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		JitterPct:   0.0,
	}

	calls := 0
	_, err := WithRetry(context.Background(), cfg, func() (string, error) {
		calls++
		return "", &HTTPError{StatusCode: 500, Message: "server error"}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetry_NonRetryable(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		JitterPct:   0.0,
	}

	calls := 0
	_, err := WithRetry(context.Background(), cfg, func() (string, error) {
		calls++
		return "", errors.New("non-retryable error")
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call for non-retryable, got %d", calls)
	}
}

func TestWithRetry_ContextCancelled(t *testing.T) {
	cfg := RetryConfig{
		MaxAttempts: 10,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    1 * time.Second,
		JitterPct:   0.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := WithRetry(ctx, cfg, func() (string, error) {
		return "", &HTTPError{StatusCode: 429, Message: "rate limited"}
	})

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}
