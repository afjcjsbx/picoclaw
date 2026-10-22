package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestPluginHTTPTransports(t *testing.T) {
	for _, transport := range []string{"streamable-http", "sse"} {
		t.Run(transport, func(t *testing.T) {
			server := sdk.NewServer(&sdk.Implementation{Name: "plugin-http", Version: "1"}, nil)
			sdk.AddTool(
				server,
				&sdk.Tool{Name: "echo", Description: "echo"},
				func(context.Context, *sdk.CallToolRequest, map[string]any) (*sdk.CallToolResult, any, error) {
					return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil, nil
				},
			)
			var handler http.Handler
			if transport == "sse" {
				handler = sdk.NewSSEHandler(func(*http.Request) *sdk.Server { return server }, nil)
			} else {
				handler = sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
			}
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			manager := NewManager()
			defer manager.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := manager.ConnectPluginServer(
				ctx,
				"test",
				config.MCPServerConfig{Type: transport, URL: httpServer.URL},
				PluginRuntimeOptions{Lifetime: ctx, Timeout: 3 * time.Second},
			); err != nil {
				t.Fatal(err)
			}
			result, err := manager.CallTool(ctx, "test", "echo", map[string]any{})
			if err != nil || result.IsError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

type pluginRoundTripFunc func(*http.Request) (*http.Response, error)

func (f pluginRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPluginHTTPHeaderBoundary(t *testing.T) {
	origin, _ := url.Parse("https://example.org/mcp")
	called := 0
	transport := &pluginHTTPTransport{
		origin:  origin,
		headers: map[string]string{"Authorization": "package", "Mcp-Session-Id": "package", "X-Plugin": "yes"},
		base: pluginRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			called++
			if r.Header.Get("Authorization") != "client" || r.Header.Get("Mcp-Session-Id") != "session" ||
				r.Header.Get("X-Plugin") != "yes" {
				t.Fatal(r.Header)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		}),
	}
	request, _ := http.NewRequest(http.MethodPost, origin.String(), nil)
	request.Header.Set("Authorization", "client")
	request.Header.Set("Mcp-Session-Id", "session")
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if request.Header.Get("X-Plugin") != "" {
		t.Fatal("original request mutated")
	}
	for _, endpoint := range []string{"https://other.example/mcp", "http://example.org/mcp", "https://example.org:444/mcp"} {
		request, _ := http.NewRequest(http.MethodPost, endpoint, nil)
		response, err := transport.RoundTrip(request)
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Fatalf("cross-origin request allowed: %s", endpoint)
		}
	}
	if called != 1 {
		t.Fatalf("sent %d requests", called)
	}
}

func TestPluginHTTPRedirectDoesNotLeakHeaders(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { leaked.Store(true); w.WriteHeader(http.StatusNoContent) },
		),
	)
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	m := NewManager()
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := m.ConnectPluginServer(
		ctx,
		"redirect",
		config.MCPServerConfig{
			Type:    "streamable-http",
			URL:     origin.URL,
			Headers: map[string]string{"X-Private": "value"},
		},
		PluginRuntimeOptions{Lifetime: ctx, Timeout: time.Second},
	)
	if err == nil || leaked.Load() {
		t.Fatalf("err=%v leaked=%v", err, leaked.Load())
	}
}
