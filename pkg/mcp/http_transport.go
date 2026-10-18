package mcp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// originBoundTransport binds configured headers to the original origin. The
// same check covers redirects and legacy SSE endpoint events, before any
// request is sent. Client-generated headers (including authorization and
// protocol headers) always win over configured ones; empty values also count
// as explicitly generated headers.
type originBoundTransport struct {
	base    http.RoundTripper
	origin  *url.URL
	headers map[string]string
}

func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// sameOriginRedirectPolicy follows redirects only while they stay on the
// configured origin, bounding the chain like net/http's default policy. It is
// shared by every MCP HTTP client so configured headers can never be replayed
// to a different origin.
func sameOriginRedirectPolicy(origin *url.URL) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if !sameOrigin(origin, req.URL) {
			return fmt.Errorf("MCP HTTP redirect changed origin")
		}
		return nil
	}
}

func (t *originBoundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sameOrigin(t.origin, req.URL) {
		return nil, fmt.Errorf("MCP HTTP request changed origin")
	}
	clone := req.Clone(req.Context())
	for key, value := range t.headers {
		present := false
		for existing := range clone.Header {
			if strings.EqualFold(existing, key) {
				present = true
				break
			}
		}
		if !present && !strings.EqualFold(key, "Host") && !strings.EqualFold(key, "Content-Length") {
			clone.Header.Set(key, value)
		}
	}
	return t.base.RoundTrip(clone)
}
