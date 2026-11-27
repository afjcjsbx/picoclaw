package integrationtools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fallbackStubProvider struct {
	result string
	err    error
	calls  []string
}

func (p *fallbackStubProvider) Search(
	_ context.Context,
	query string,
	_ int,
	_ string,
) (string, error) {
	p.calls = append(p.calls, query)
	return p.result, p.err
}

func TestWebSearchToolFallsBackAfterCaptcha(t *testing.T) {
	tests := []struct {
		name     string
		provider SearchProvider
	}{
		{
			name: "sogou",
			provider: &SogouSearchProvider{
				client: &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						rec := httptest.NewRecorder()
						fmt.Fprint(rec, `<html><body>
<div class="anti-box" id="anti-box"><form id="verify-form">验证码</form></div>
</body></html>`)
						return rec.Result(), nil
					}),
				},
			},
		},
		{
			name: "duckduckgo",
			provider: &DuckDuckGoSearchProvider{
				client: &http.Client{
					Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
						rec := httptest.NewRecorder()
						fmt.Fprint(rec, `<html><body>
<script src="anomaly.js"></script><div id="challenge">CAPTCHA</div>
</body></html>`)
						return rec.Result(), nil
					}),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secondary := &fallbackStubProvider{result: "Results for: Veroli meteo (via Secondary)\n1. Forecast"}
			unused := &fallbackStubProvider{result: "unused"}
			tool := &WebSearchTool{
				providerResolver: func(string) []searchProviderCandidate {
					return []searchProviderCandidate{
						{provider: tt.provider, maxResults: 5},
						{provider: secondary, maxResults: 5},
						{provider: unused, maxResults: 5},
					}
				},
			}

			result := tool.Execute(context.Background(), map[string]any{"query": "Veroli meteo"})
			if result.IsError {
				t.Fatalf("Execute() returned error: %s", result.ForLLM)
			}
			if result.ForLLM != secondary.result {
				t.Fatalf("output = %q, want %q", result.ForLLM, secondary.result)
			}
			if len(secondary.calls) != 1 {
				t.Fatalf("secondary provider calls = %v, want one call", secondary.calls)
			}
			if len(unused.calls) != 0 {
				t.Fatalf("unused provider should not be called after success, calls=%v", unused.calls)
			}
		})
	}
}

func TestWebSearchToolDoesNotFallBackAfterEmptyResults(t *testing.T) {
	primary := &fallbackStubProvider{result: "No results for: Veroli meteo"}
	secondary := &fallbackStubProvider{result: "Results for: Veroli meteo (via Secondary)\n1. Forecast"}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{provider: primary, maxResults: 5},
				{provider: secondary, maxResults: 5},
			}
		},
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "Veroli meteo"})
	if result.IsError {
		t.Fatalf("Execute() returned error: %s", result.ForLLM)
	}
	if result.ForLLM != primary.result {
		t.Fatalf("output = %q, want %q", result.ForLLM, primary.result)
	}
	if len(secondary.calls) != 0 {
		t.Fatalf("secondary provider should not be called after empty results, calls=%v", secondary.calls)
	}
}

func TestWebSearchToolDoesNotFallBackAfterNonCaptchaError(t *testing.T) {
	primary := &fallbackStubProvider{err: errors.New("primary unavailable")}
	secondary := &fallbackStubProvider{result: "Results for: Veroli meteo (via Secondary)\n1. Forecast"}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{provider: primary, maxResults: 5},
				{provider: secondary, maxResults: 5},
			}
		},
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "Veroli meteo"})
	if !result.IsError {
		t.Fatalf("Execute() should return an error, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "primary unavailable") {
		t.Fatalf("error = %q, want primary provider error", result.ForLLM)
	}
	if len(secondary.calls) != 0 {
		t.Fatalf("secondary provider should not be called after a non-CAPTCHA error, calls=%v", secondary.calls)
	}
}

func TestWebSearchToolReportsCaptchaWhenAllProvidersBlocked(t *testing.T) {
	primary := &fallbackStubProvider{err: fmt.Errorf("Sogou blocked automated search with %w", errProviderCaptcha)}
	secondary := &fallbackStubProvider{
		err: fmt.Errorf("DuckDuckGo blocked automated search with %w", errProviderCaptcha),
	}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{provider: primary, maxResults: 5},
				{provider: secondary, maxResults: 5},
			}
		},
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "Veroli meteo"})
	if !result.IsError {
		t.Fatalf("Execute() should return an error, got: %s", result.ForLLM)
	}
	for _, want := range []string{"all providers blocked by CAPTCHA", "DuckDuckGo"} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("error %q missing %q", result.ForLLM, want)
		}
	}
}

func TestWebSearchProviderOrderPutsExplicitProviderFirstWithoutDuplicates(t *testing.T) {
	opts := WebSearchToolOptions{
		Provider:          "brave",
		BraveEnabled:      true,
		BraveAPIKeys:      []string{"brave-key"},
		TavilyEnabled:     true,
		TavilyAPIKeys:     []string{"tavily-key"},
		SogouEnabled:      true,
		DuckDuckGoEnabled: true,
	}

	got := webSearchProviderOrder(opts, "golang concurrency")
	want := []string{"brave", "tavily", "duckduckgo", "sogou"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("provider order = %v, want %v", got, want)
	}
}

func TestDuckDuckGoCaptchaDetectionIgnoresResultContent(t *testing.T) {
	body := []byte(`<html><body><form action="/html/?q=anomaly.js"><input value="anomaly.js"></form></body></html>`)
	if isDuckDuckGoCaptcha(http.StatusOK, body) {
		t.Fatal("ordinary result content mentioning anomaly.js must not be treated as CAPTCHA")
	}
}
