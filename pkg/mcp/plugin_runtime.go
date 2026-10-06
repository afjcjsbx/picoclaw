package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

// PluginRuntimeOptions separates process lifetime from handshake timeout.
// Environment is an exact host-prepared environment, never ambient inheritance.
type PluginRuntimeOptions struct {
	Stderr      io.Writer
	Lifetime    context.Context
	OAuthName   string
	Directory   string
	Environment []string
	Timeout     time.Duration
}

// pluginHTTPTransport binds configured headers to the original origin. The same
// check covers redirects and legacy SSE endpoint events, before any request is sent.
type pluginHTTPTransport struct {
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

func (t *pluginHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !sameOrigin(t.origin, req.URL) {
		return nil, fmt.Errorf("plugin HTTP request changed origin")
	}
	clone := req.Clone(req.Context())
	for key, value := range t.headers {
		// Client-generated headers (including authorization and protocol headers)
		// always win; empty values also count as explicitly generated headers.
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

// ConnectPluginServer uses portable transport meanings, without changing the
// historical meaning of "sse" in PicoClaw's native MCP configuration.
func (m *Manager) ConnectPluginServer(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
	opts PluginRuntimeOptions,
) error {
	if opts.Lifetime == nil {
		return fmt.Errorf("plugin lifetime context is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	// SSE binds its long-lived GET request to Connect's context. Keep that
	// context alive after the handshake; stop only the initialization timer.
	sessionCtx, cancel := context.WithCancel(opts.Lifetime)
	timer := time.AfterFunc(opts.Timeout, cancel)
	stop := context.AfterFunc(ctx, cancel)
	connected := false
	defer func() {
		timer.Stop()
		stop()
		if !connected {
			cancel()
		}
	}()
	if cfg.OAuth != nil {
		if err := validateOAuthConfig(cfg); err != nil {
			return err
		}
	}
	var transport sdk.Transport
	switch cfg.Type {
	case "stdio":
		cmd := exec.CommandContext(sessionCtx, cfg.Command, cfg.Args...)
		cmd.Dir = opts.Directory
		cmd.Stderr = opts.Stderr
		cmd.Env = append([]string{}, opts.Environment...)
		transport = &isolatedCommandTransport{Command: cmd, TerminateDuration: time.Second}
	case "streamable-http", "sse":
		var oauthHandler auth.OAuthHandler
		if cfg.OAuth != nil {
			oauthName := opts.OAuthName
			if oauthName == "" {
				oauthName = name
			}
			oauthHandler = &storedOAuthHandler{name: oauthName, store: newOAuthStore(oauthName, cfg)}
		}
		origin, err := url.Parse(cfg.URL)
		if err != nil {
			return err
		}
		var base http.RoundTripper = http.DefaultTransport
		if oauthHandler != nil {
			base = oauthHTTPTransport{}
		}
		client := &http.Client{
			Transport: &pluginHTTPTransport{base: base, origin: origin, headers: cfg.Headers},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if oauthHandler != nil {
					return rejectOAuthRedirect(req, via)
				}
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				if !sameOrigin(origin, req.URL) {
					return fmt.Errorf("plugin redirect changed origin")
				}
				return nil
			},
		}
		if cfg.Type == "sse" && oauthHandler == nil {
			transport = &sdk.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: client}
		} else {
			transport = &sdk.StreamableClientTransport{
				Endpoint:     cfg.URL,
				HTTPClient:   client,
				OAuthHandler: oauthHandler,
				// The optional GET listener is not supported by every hosted server.
				DisableStandaloneSSE: cfg.Type != "sse",
			}
		}
	default:
		return fmt.Errorf("unsupported plugin MCP transport %q", cfg.Type)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "picoclaw-plugins", Version: "1.0.0"}, nil)
	session, err := client.Connect(sessionCtx, transport, nil)
	if err != nil {
		return err
	}
	serverTools, err := listServerTools(sessionCtx, name, session, session.InitializeResult())
	if err != nil {
		_ = session.Close()
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		_ = session.Close()
		return fmt.Errorf("manager is closed")
	}
	if _, exists := m.servers[name]; exists {
		_ = session.Close()
		return fmt.Errorf("duplicate MCP server %q", name)
	}
	if !timer.Stop() || sessionCtx.Err() != nil {
		_ = session.Close()
		return fmt.Errorf("plugin initialization canceled or timed out")
	}
	m.servers[name] = &ServerConnection{
		Name:    name,
		Config:  cfg,
		Client:  client,
		Session: session,
		Tools:   serverTools,
		plugin:  true,
		cancel:  cancel,
	}
	connected = true
	return nil
}
