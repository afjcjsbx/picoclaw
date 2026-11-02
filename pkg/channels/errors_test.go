package channels

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestErrorsIs(t *testing.T) {
	wrapped := fmt.Errorf("telegram API: %w", ErrRateLimit)
	if !errors.Is(wrapped, ErrRateLimit) {
		t.Error("wrapped ErrRateLimit should match")
	}
	if errors.Is(wrapped, ErrTemporary) {
		t.Error("wrapped ErrRateLimit should not match ErrTemporary")
	}
}

func TestErrorsIsAllTypes(t *testing.T) {
	sentinels := []error{ErrNotRunning, ErrRateLimit, ErrTemporary, ErrSendFailed}

	for _, sentinel := range sentinels {
		wrapped := fmt.Errorf("context: %w", sentinel)
		if !errors.Is(wrapped, sentinel) {
			t.Errorf("wrapped %v should match itself", sentinel)
		}

		// Verify it doesn't match other sentinel errors
		for _, other := range sentinels {
			if other == sentinel {
				continue
			}
			if errors.Is(wrapped, other) {
				t.Errorf("wrapped %v should not match %v", sentinel, other)
			}
		}
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrNotRunning, "channel not running"},
		{ErrRateLimit, "rate limited"},
		{ErrTemporary, "temporary failure"},
		{ErrSendFailed, "send failed"},
	}

	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("error message = %q, want %q", got, tt.want)
		}
	}
}

func TestRateLimitError(t *testing.T) {
	raw := errors.New("telego: sendMessage: api: 429")
	err := &RateLimitError{RetryAfter: 14 * time.Second, Err: raw}

	if !errors.Is(err, ErrRateLimit) {
		t.Error("RateLimitError should match ErrRateLimit")
	}
	if errors.Is(err, ErrTemporary) {
		t.Error("RateLimitError should not match ErrTemporary")
	}
	if !errors.Is(err, raw) {
		t.Error("RateLimitError should match its wrapped error")
	}

	delay, ok := RateLimitRetryAfter(fmt.Errorf("send: %w", err))
	if !ok || delay != 14*time.Second {
		t.Fatalf("RateLimitRetryAfter = (%v, %v), want (14s, true)", delay, ok)
	}

	if _, ok := RateLimitRetryAfter(ErrRateLimit); ok {
		t.Error("plain ErrRateLimit should not report a retry window")
	}
}
