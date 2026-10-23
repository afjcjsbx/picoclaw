// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/providers/common"
	"github.com/sipeed/picoclaw/pkg/seahorse"
)

// overloadProvider fails with errs in order, then answers "summary".
type overloadProvider struct {
	mu    sync.Mutex
	errs  []error
	calls int
}

func (p *overloadProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls <= len(p.errs) {
		return nil, p.errs[p.calls-1]
	}
	return &providers.LLMResponse{Content: "summary", ToolCalls: []providers.ToolCall{}}, nil
}

func (p *overloadProvider) GetDefaultModel() string { return "overload-mock" }

func httpStatusError(code int) error {
	return &common.HTTPError{StatusCode: code, BodyPreview: "{}"}
}

func TestIsOverloadStatus(t *testing.T) {
	textual := func(code int) error {
		return fmt.Errorf("API request failed:\n  Status: %d\n  Body:   {}", code)
	}
	for _, code := range []int{429, 502, 503} {
		if !isOverloadStatus(httpStatusError(code)) || !isOverloadStatus(textual(code)) {
			t.Errorf("status %d must be retryable", code)
		}
	}
	// Other failures are not this retry's business: bad requests and auth never
	// clear on their own, and 500/504/timeouts have their own handling.
	for _, err := range []error{
		nil,
		httpStatusError(400), httpStatusError(401), httpStatusError(500), httpStatusError(504),
		textual(404), errors.New("boom"), context.Canceled,
	} {
		if isOverloadStatus(err) {
			t.Errorf("%v must not be retried as overload", err)
		}
	}
}

func TestChatRetryingOverload(t *testing.T) {
	fast := llmRetry{max: 2, backoff: time.Millisecond}
	cases := []struct {
		name      string
		errs      []error
		wantCalls int
		wantErr   bool
	}{
		{"502 then success", []error{httpStatusError(502)}, 2, false},
		{"503 and 429 then success", []error{httpStatusError(503), httpStatusError(429)}, 3, false},
		{
			"gives up after max retries",
			[]error{httpStatusError(502), httpStatusError(502), httpStatusError(502), httpStatusError(502)},
			3,
			true,
		},
		{"400 is not retried", []error{httpStatusError(400)}, 1, true},
		{"unknown error is not retried", []error{errors.New("boom")}, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &overloadProvider{errs: tc.errs}
			resp, err := chatRetryingOverload(context.Background(), p, nil, nil, "m", nil, fast)
			if p.calls != tc.wantCalls {
				t.Fatalf("provider calls = %d, want %d", p.calls, tc.wantCalls)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && resp.Content != "summary" {
				t.Fatalf("content = %q", resp.Content)
			}
		})
	}

	t.Run("no retries when max is zero", func(t *testing.T) {
		p := &overloadProvider{errs: []error{httpStatusError(502)}}
		if _, err := chatRetryingOverload(context.Background(), p, nil, nil, "m", nil, llmRetry{}); err == nil {
			t.Fatal("expected the first failure to be returned")
		}
		if p.calls != 1 {
			t.Fatalf("provider calls = %d, want 1", p.calls)
		}
	})

	t.Run("a canceled context interrupts the backoff", func(t *testing.T) {
		p := &overloadProvider{errs: []error{httpStatusError(429), httpStatusError(429)}}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := chatRetryingOverload(ctx, p, nil, nil, "m", nil, llmRetry{max: 2, backoff: time.Hour})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want the context error", err)
		}
		if time.Since(start) > 5*time.Second {
			t.Fatal("the backoff was not interrupted")
		}
		if p.calls != 1 {
			t.Fatalf("provider calls = %d, want 1", p.calls)
		}
	})
}

func TestLLMRetryFromConfig(t *testing.T) {
	if got := llmRetryFromConfig(nil); got.max != 2 || got.backoff != 2*time.Second {
		t.Fatalf("nil config = %+v, want the main loop defaults", got)
	}
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
		MaxLLMRetries: 5, LLMRetryBackoffSecs: 3,
	}}}
	if got := llmRetryFromConfig(cfg); got.max != 5 || got.backoff != 3*time.Second {
		t.Fatalf("configured = %+v, want 5 retries and a 3s backoff", got)
	}
}

// A 502 on the first summarization request is retried, so the real summary is
// stored instead of the truncated-transcript fallback.
func TestLegacySummarizer_RetriesOverloadedUpstream(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:           t.TempDir(),
				ModelName:           "test-model",
				MaxTokens:           1000,
				MaxToolIterations:   10,
				LLMRetryBackoffSecs: 1,
			},
		},
	}
	provider := &overloadProvider{errs: []error{httpStatusError(502)}}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	agent := al.registry.GetDefaultAgent()
	if err := agent.Sessions.SetHistory(context.Background(), "overload", alternatingHistory(3, "hello")); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "overload",
		Reason:     ContextCompressReasonManual,
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (the 502, then the retry)", provider.calls)
	}
	if summary, _ := agent.Sessions.GetSummary(context.Background(), "overload"); summary != "summary" {
		t.Fatalf("summary = %q, want the model's summary and not the fallback", summary)
	}
}

func TestSeahorseCompleteFn_RetriesOverloadedUpstream(t *testing.T) {
	complete := func(p providers.LLMProvider, retry llmRetry) (string, error) {
		return providerToCompleteFn(p, "m", retry)(context.Background(), "summarize", seahorse.CompleteOptions{})
	}

	p := &overloadProvider{errs: []error{httpStatusError(503)}}
	got, err := complete(p, llmRetry{max: 2, backoff: time.Millisecond})
	if err != nil || got != "summary" || p.calls != 2 {
		t.Fatalf("got %q, err %v after %d calls; want the summary after one retry", got, err, p.calls)
	}

	p = &overloadProvider{errs: []error{httpStatusError(503)}}
	if _, err := complete(p, llmRetry{}); err == nil || p.calls != 1 {
		t.Fatalf("without retries the first failure must surface (err=%v, calls=%d)", err, p.calls)
	}
}
