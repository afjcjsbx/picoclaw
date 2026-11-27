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

func TestWebSearchToolFallsBackAfterProviderError(t *testing.T) {
	primary := &fallbackStubProvider{err: errors.New("blocked by captcha")}
	secondary := &fallbackStubProvider{result: "Results for: Veroli meteo (via Secondary)\n1. Forecast"}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{name: "primary", provider: primary, maxResults: 5},
				{name: "secondary", provider: secondary, maxResults: 5},
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
	if len(primary.calls) != 1 || len(secondary.calls) != 1 {
		t.Fatalf("provider calls = primary %v, secondary %v; want one each", primary.calls, secondary.calls)
	}
}

func TestWebSearchToolFallsBackAfterEmptyResults(t *testing.T) {
	primary := &fallbackStubProvider{result: "No results for: Veroli meteo"}
	secondary := &fallbackStubProvider{result: "Results for: Veroli meteo (via Secondary)\n1. Forecast"}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{name: "primary", provider: primary, maxResults: 5},
				{name: "secondary", provider: secondary, maxResults: 5},
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
	if len(primary.calls) != 1 || len(secondary.calls) != 1 {
		t.Fatalf("provider calls = primary %v, secondary %v; want one each", primary.calls, secondary.calls)
	}
}

func TestWebSearchToolStopsAfterSuccessfulProvider(t *testing.T) {
	primary := &fallbackStubProvider{result: "Results for: query (via Primary)\n1. Result"}
	secondary := &fallbackStubProvider{result: "Results for: query (via Secondary)\n1. Result"}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{name: "primary", provider: primary, maxResults: 5},
				{name: "secondary", provider: secondary, maxResults: 5},
			}
		},
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "query"})
	if result.IsError {
		t.Fatalf("Execute() returned error: %s", result.ForLLM)
	}
	if result.ForLLM != primary.result {
		t.Fatalf("output = %q, want %q", result.ForLLM, primary.result)
	}
	if len(secondary.calls) != 0 {
		t.Fatalf("secondary provider should not be called after success, calls=%v", secondary.calls)
	}
}

func TestWebSearchToolReportsAllProviderFailures(t *testing.T) {
	primary := &fallbackStubProvider{err: errors.New("primary unavailable")}
	secondary := &fallbackStubProvider{err: errors.New("secondary rate limited")}
	tool := &WebSearchTool{
		providerResolver: func(string) []searchProviderCandidate {
			return []searchProviderCandidate{
				{name: "primary", provider: primary, maxResults: 5},
				{name: "secondary", provider: secondary, maxResults: 5},
			}
		},
	}

	result := tool.Execute(context.Background(), map[string]any{"query": "query"})
	if !result.IsError {
		t.Fatalf("Execute() should return an error, got: %s", result.ForLLM)
	}
	for _, want := range []string{"primary unavailable", "secondary rate limited"} {
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

func TestSogouSearchProviderReportsCaptcha(t *testing.T) {
	provider := &SogouSearchProvider{
		client: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				fmt.Fprint(rec, `<html><body>
<div class="anti-box" id="anti-box"><form id="verify-form">验证码</form></div>
</body></html>`)
				return rec.Result(), nil
			}),
		},
	}

	_, err := provider.Search(context.Background(), "Veroli meteo", 5, "")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "captcha") {
		t.Fatalf("Search() error = %v, want CAPTCHA error", err)
	}
}

func TestDuckDuckGoSearchProviderReportsCaptcha(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{
		client: &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				rec.WriteHeader(http.StatusAccepted)
				fmt.Fprint(rec, `<html><body>
<script src="anomaly.js"></script><div id="challenge">CAPTCHA</div>
</body></html>`)
				return rec.Result(), nil
			}),
		},
	}

	_, err := provider.Search(context.Background(), "Veroli meteo", 5, "")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "captcha") {
		t.Fatalf("Search() error = %v, want CAPTCHA error", err)
	}
}
