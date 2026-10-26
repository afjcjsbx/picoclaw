package mcp

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

func TestSameOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		origin string
		target string
		want   bool
	}{
		{"identical", "https://example.test/mcp", "https://example.test/tools", true},
		{"explicit default port", "https://example.test/mcp", "https://example.test:443/tools", true},
		{"scheme upgrade with default ports", "http://example.test/mcp", "https://example.test/mcp", true},
		{
			"scheme upgrade with matching explicit port",
			"http://example.test:8443/mcp",
			"https://example.test:8443/mcp",
			true,
		},
		{"scheme downgrade", "https://example.test/mcp", "http://example.test/mcp", false},
		{"different host", "https://example.test/mcp", "https://evil.test/mcp", false},
		{"different port", "https://example.test/mcp", "https://example.test:8443/mcp", false},
		{"different scheme", "https://example.test/mcp", "ftp://example.test/mcp", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			origin := mustParseURL(t, tt.origin)
			target := mustParseURL(t, tt.target)
			if got := sameOrigin(origin, target); got != tt.want {
				t.Fatalf("sameOrigin(%q, %q) = %v, want %v", tt.origin, tt.target, got, tt.want)
			}
		})
	}
}

func TestSameOriginRedirectPolicy(t *testing.T) {
	t.Parallel()

	origin := mustParseURL(t, "https://example.test/mcp")
	policy := sameOriginRedirectPolicy(origin)

	if err := policy(&http.Request{URL: mustParseURL(t, "https://example.test/tools")}, nil); err != nil {
		t.Fatalf("same-origin redirect error = %v, want nil", err)
	}
	if err := policy(&http.Request{URL: mustParseURL(t, "https://evil.test/mcp")}, nil); err == nil {
		t.Fatal("cross-origin redirect error = nil, want refusal")
	} else if !strings.Contains(err.Error(), "changed origin") {
		t.Fatalf("cross-origin redirect error = %v, want an origin change", err)
	}

	via := make([]*http.Request, 10)
	if err := policy(&http.Request{URL: mustParseURL(t, "https://example.test/tools")}, via); err == nil {
		t.Fatal("redirect chain of 10 requests error = nil, want refusal")
	}
}
