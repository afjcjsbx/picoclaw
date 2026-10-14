package integrationtools

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// countingBody is an io.ReadCloser that yields a large, bounded number of
// bytes (prefix first, then 'x' filler) and records how many were actually
// read, so tests can assert that a provider clamps its response buffering.
type countingBody struct {
	prefix    string
	remaining int
	read      int
}

func (b *countingBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, io.EOF
	}
	p = p[:min(len(p), b.remaining)]
	n := len(p)
	for i := range p {
		p[i] = 'x'
	}
	if b.read < len(b.prefix) {
		copy(p, b.prefix[b.read:])
	}
	b.remaining -= n
	b.read += n
	return n, nil
}

func (b *countingBody) Close() error { return nil }

// oversizedClient points a provider at an in-memory response without a real
// server, reusing the roundTripFunc defined in web_test.go.
func oversizedClient(body *countingBody) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       body,
				Request:    &http.Request{},
			}, nil
		}),
	}
}

// TestSearchProviders_ClampResponseSize verifies that every search provider
// bounds how much of an upstream response it buffers in memory.
func TestSearchProviders_ClampResponseSize(t *testing.T) {
	// Deliberately larger than maxSearchResponseBytes so the clamp is exercised.
	const payload = maxSearchResponseBytes + (1 << 20)

	// Streaming JSON decoders bail out at the first invalid byte, so the body
	// opens like a valid response and never closes: only the clamp stops them.
	const prefix = `{"results":[{"title":"`

	tests := []struct {
		name     string
		provider func(*http.Client) SearchProvider
	}{
		{
			name: "brave",
			provider: func(c *http.Client) SearchProvider {
				return &BraveSearchProvider{keyPool: NewAPIKeyPool([]string{"test-key"}), client: c}
			},
		},
		{
			name: "tavily",
			provider: func(c *http.Client) SearchProvider {
				return &TavilySearchProvider{keyPool: NewAPIKeyPool([]string{"test-key"}), client: c}
			},
		},
		{
			name: "duckduckgo",
			provider: func(c *http.Client) SearchProvider {
				return &DuckDuckGoSearchProvider{client: c}
			},
		},
		{
			name: "perplexity",
			provider: func(c *http.Client) SearchProvider {
				return &PerplexitySearchProvider{keyPool: NewAPIKeyPool([]string{"test-key"}), client: c}
			},
		},
		{
			name: "searxng",
			provider: func(c *http.Client) SearchProvider {
				return &SearXNGSearchProvider{baseURL: "http://searxng.test", client: c}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &countingBody{prefix: prefix, remaining: payload}
			p := tt.provider(oversizedClient(body))

			_, err := p.Search(context.Background(), "picoclaw", 5, "")
			if err == nil || !strings.Contains(err.Error(), "response exceeds") {
				t.Fatalf("Search error = %v, want a \"response exceeds\" error", err)
			}

			// The clamp reads one byte past the cap to detect oversized bodies.
			if limit := maxSearchResponseBytes + 1; body.read > limit {
				t.Fatalf("provider read %d bytes from the response, want <= %d", body.read, limit)
			}
		})
	}
}

// TestSearchProviders_TruncateErrorBody verifies that a large upstream error
// body is not echoed in full into the returned error.
func TestSearchProviders_TruncateErrorBody(t *testing.T) {
	body := strings.Repeat("e", 100*maxErrorBodyBytes)
	client := &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    &http.Request{},
			}, nil
		}),
	}
	p := &BraveSearchProvider{keyPool: NewAPIKeyPool([]string{"test-key"}), client: client}

	_, err := p.Search(context.Background(), "picoclaw", 5, "")
	if err == nil {
		t.Fatal("Search error = nil, want an API error")
	}
	if got, limit := len(err.Error()), 2*maxErrorBodyBytes; got > limit {
		t.Fatalf("error is %d bytes, want <= %d", got, limit)
	}
}

func TestErrorBody_KeepsValidUTF8(t *testing.T) {
	// A multi-byte rune straddling the cut must be dropped, not split.
	body := []byte(strings.Repeat("a", maxErrorBodyBytes-1) + "é" + "tail")
	got := errorBody(body)
	if !utf8.ValidString(got) {
		t.Fatalf("errorBody returned invalid UTF-8: %q", got[len(got)-8:])
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("errorBody = %q..., want a trailing ellipsis", got[:8])
	}
}
