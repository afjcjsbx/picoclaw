package integrationtools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type searchProviderFunc func(context.Context, string, int, string) (string, error)

func (f searchProviderFunc) Search(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	return f(ctx, query, count, rangeCode)
}

func keepProviderOrder(_ []webSearchProvider) {
	// Tests use this hook to make fallback order deterministic.
}

func TestIsEmptySearchResult(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   bool
	}{
		{name: "blank", result: "  \n", want: true},
		{name: "no results", result: "No results for: test", want: true},
		{name: "extraction failed", result: "No results found or extraction failed. Query: test", want: true},
		{name: "header only", result: "Results for: test (via Perplexity)", want: true},
		{name: "valid result", result: "Results for: test\n1. Example\n   https://example.com", want: false},
		{name: "synthesized answer", result: "Results for: test (via Gemini)\nAnswer text", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEmptySearchResult(tt.result); got != tt.want {
				t.Fatalf("isEmptySearchResult(%q) = %v, want %v", tt.result, got, tt.want)
			}
		})
	}
}

func TestDuckDuckGoSearchProviderRejectsNonOKStatus(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{
		client: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				rec.WriteHeader(http.StatusForbidden)
				_, _ = rec.WriteString("blocked")
				return rec.Result(), nil
			}),
		},
	}

	_, err := provider.Search(context.Background(), "test", 5, "")
	if err == nil || !strings.Contains(err.Error(), "status 403") {
		t.Fatalf("Search() error = %v, want status 403", err)
	}
}

func TestWebSearchMultiQueriesConcurrentlyAndReplacesFailedOrEmptyProviders(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	providers := []webSearchProvider{
		{
			name: "failing",
			provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
				started <- "failing"
				<-release
				return "", errors.New("upstream failed")
			}),
			maxResults: 5,
		},
		{
			name: "empty",
			provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
				started <- "empty"
				<-release
				return "No results for: test", nil
			}),
			maxResults: 5,
		},
		{
			name: "success",
			provider: searchProviderFunc(func(
				_ context.Context,
				query string,
				count int,
				rangeCode string,
			) (string, error) {
				started <- "success"
				if query != "test" || count != 4 || rangeCode != "w" {
					return "", fmt.Errorf("unexpected arguments: query=%q count=%d range=%q", query, count, rangeCode)
				}
				return "valid result", nil
			}),
			maxResults: 5,
		},
	}
	tool := &WebSearchTool{
		multiProviders:   providers,
		multiConcurrency: 2,
		shuffleProviders: keepProviderOrder,
	}

	type executeResult struct {
		result *ToolResult
	}
	done := make(chan executeResult, 1)
	go func() {
		done <- executeResult{result: tool.Execute(context.Background(), map[string]any{
			"query": "test",
			"count": 4,
			"range": "w",
		})}
	}()

	waitForProvider := func() string {
		t.Helper()
		select {
		case got := <-started:
			return got
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for initial providers")
			return ""
		}
	}
	first := waitForProvider()
	second := waitForProvider()
	if !((first == "failing" && second == "empty") || (first == "empty" && second == "failing")) {
		t.Fatalf("initial providers = %q, %q; want failing and empty", first, second)
	}

	select {
	case got := <-started:
		t.Fatalf("provider %q started before an in-flight provider finished", got)
	default:
	}

	close(release)
	select {
	case got := <-done:
		if got.result.IsError {
			t.Fatalf("Execute() error: %s", got.result.ForLLM)
		}
		if got.result.ForLLM != "valid result" || got.result.ForUser != "valid result" {
			t.Fatalf("Execute() = %q/%q, want valid result", got.result.ForLLM, got.result.ForUser)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for multi search")
	}

	select {
	case got := <-started:
		if got != "success" {
			t.Fatalf("fallback provider = %q, want success", got)
		}
	default:
	}
}

func TestWebSearchMultiCancelsOtherProvidersAfterSuccess(t *testing.T) {
	started := make(chan struct{}, 1)
	canceled := make(chan struct{})
	tool := &WebSearchTool{
		multiProviders: []webSearchProvider{
			{
				name: "slow",
				provider: searchProviderFunc(func(ctx context.Context, _ string, _ int, _ string) (string, error) {
					started <- struct{}{}
					<-ctx.Done()
					close(canceled)
					return "", ctx.Err()
				}),
				maxResults: 5,
			},
			{
				name: "fast",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					<-started
					return "fast result", nil
				}),
				maxResults: 5,
			},
		},
		multiConcurrency: 2,
		shuffleProviders: keepProviderOrder,
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "test"})
	if result.IsError || result.ForLLM != "fast result" {
		t.Fatalf("Execute() = %#v, want fast result", result)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("losing provider was not canceled")
	}
}

func TestWebSearchMultiReturnsNoResultsWhenAllProvidersAreEmpty(t *testing.T) {
	tool := &WebSearchTool{
		multiProviders: []webSearchProvider{
			{
				name: "empty",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					return "", nil
				}),
				maxResults: 5,
			},
			{
				name: "also-empty",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					return "No results found or extraction failed.", nil
				}),
				maxResults: 5,
			},
		},
		multiConcurrency: 2,
		shuffleProviders: keepProviderOrder,
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "test"})
	if result.IsError {
		t.Fatalf("Execute() error: %s", result.ForLLM)
	}
	if result.ForLLM != "No results for: test" {
		t.Fatalf("Execute() = %q, want no-results message", result.ForLLM)
	}
}

func TestWebSearchMultiReturnsLastErrorWhenAllProvidersFail(t *testing.T) {
	tool := &WebSearchTool{
		multiProviders: []webSearchProvider{
			{
				name: "first",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					return "", errors.New("first failed")
				}),
				maxResults: 5,
			},
			{
				name: "last",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					return "", errors.New("last failed")
				}),
				maxResults: 5,
			},
		},
		multiConcurrency: 1,
		shuffleProviders: keepProviderOrder,
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "test"})
	if !result.IsError {
		t.Fatalf("Execute() = %#v, want error", result)
	}
	if !strings.Contains(result.ForLLM, "all search providers failed") ||
		!strings.Contains(result.ForLLM, "last: last failed") {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
}

func TestWebSearchMultiCapsCountPerProvider(t *testing.T) {
	var gotCount atomic.Int32
	tool := &WebSearchTool{
		multiProviders: []webSearchProvider{
			{
				name: "limited",
				provider: searchProviderFunc(func(_ context.Context, _ string, count int, _ string) (string, error) {
					gotCount.Store(int32(count))
					return "result", nil
				}),
				maxResults: 3,
			},
		},
		multiConcurrency: 3,
		shuffleProviders: keepProviderOrder,
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "test", "count": 10})
	if result.IsError {
		t.Fatalf("Execute() error: %s", result.ForLLM)
	}
	if got := gotCount.Load(); got != 3 {
		t.Fatalf("provider count = %d, want 3", got)
	}
}

func TestWebSearchMultiStopsWhenContextIsCanceled(t *testing.T) {
	started := make(chan struct{})
	tool := &WebSearchTool{
		multiProviders: []webSearchProvider{
			{
				name: "waiting",
				provider: searchProviderFunc(func(ctx context.Context, _ string, _ int, _ string) (string, error) {
					close(started)
					<-ctx.Done()
					return "", ctx.Err()
				}),
				maxResults: 5,
			},
		},
		multiConcurrency: 1,
		shuffleProviders: keepProviderOrder,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *ToolResult, 1)
	go func() {
		done <- tool.Execute(ctx, map[string]any{"query": "test"})
	}()
	<-started
	cancel()

	select {
	case result := <-done:
		if !result.IsError || !strings.Contains(result.ForLLM, "context canceled") {
			t.Fatalf("Execute() = %#v, want context cancellation", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
}

func TestWebSearchMultiRandomizesCandidateOrder(t *testing.T) {
	var order []string
	tool := &WebSearchTool{
		multiProviders: []webSearchProvider{
			{
				name: "first",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					return "first result", nil
				}),
				maxResults: 5,
			},
			{
				name: "second",
				provider: searchProviderFunc(func(context.Context, string, int, string) (string, error) {
					return "second result", nil
				}),
				maxResults: 5,
			},
		},
		multiConcurrency: 1,
		shuffleProviders: func(providers []webSearchProvider) {
			providers[0], providers[1] = providers[1], providers[0]
			for _, provider := range providers {
				order = append(order, provider.name)
			}
		},
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "test"})
	if result.IsError || result.ForLLM != "second result" {
		t.Fatalf("Execute() = %#v, want second result", result)
	}
	if strings.Join(order, ",") != "second,first" {
		t.Fatalf("candidate order = %v, want [second first]", order)
	}
}

func TestNewWebSearchToolMultiIncludesOnlyReadyProviders(t *testing.T) {
	tool, err := NewWebSearchTool(WebSearchToolOptions{
		Provider:          "multi",
		MultiConcurrency:  2,
		BraveEnabled:      true,
		SogouEnabled:      true,
		SogouMaxResults:   5,
		DuckDuckGoEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewWebSearchTool() error: %v", err)
	}
	if tool == nil {
		t.Fatal("expected multi search tool")
	}
	if len(tool.multiProviders) != 2 {
		t.Fatalf("ready providers = %d, want 2", len(tool.multiProviders))
	}
	for _, candidate := range tool.multiProviders {
		if candidate.name == "brave" {
			t.Fatal("unready Brave provider should not be included")
		}
	}
}

func TestSingleProviderModeDoesNotEnableMultiSearch(t *testing.T) {
	tool, err := NewWebSearchTool(WebSearchToolOptions{
		SogouEnabled: true,
	})
	if err != nil {
		t.Fatalf("NewWebSearchTool() error: %v", err)
	}
	if tool == nil {
		t.Fatal("expected single search tool")
	}
	if len(tool.multiProviders) != 0 {
		t.Fatalf("single mode has %d multi providers", len(tool.multiProviders))
	}
}
