package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

func TestLoadEnvFile(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		expected  map[string]string
		expectErr bool
	}{
		{
			name: "basic env file",
			content: `API_KEY=secret123
DATABASE_URL=postgres://localhost/db
PORT=8080`,
			expected: map[string]string{
				"API_KEY":      "secret123",
				"DATABASE_URL": "postgres://localhost/db",
				"PORT":         "8080",
			},
			expectErr: false,
		},
		{
			name: "with comments and empty lines",
			content: `# This is a comment
API_KEY=secret123

# Another comment
DATABASE_URL=postgres://localhost/db

PORT=8080`,
			expected: map[string]string{
				"API_KEY":      "secret123",
				"DATABASE_URL": "postgres://localhost/db",
				"PORT":         "8080",
			},
			expectErr: false,
		},
		{
			name: "with quoted values",
			content: `API_KEY="secret with spaces"
NAME='single quoted'
PLAIN=no-quotes`,
			expected: map[string]string{
				"API_KEY": "secret with spaces",
				"NAME":    "single quoted",
				"PLAIN":   "no-quotes",
			},
			expectErr: false,
		},
		{
			name: "with spaces around equals",
			content: `API_KEY = secret123
DATABASE_URL= postgres://localhost/db
PORT =8080`,
			expected: map[string]string{
				"API_KEY":      "secret123",
				"DATABASE_URL": "postgres://localhost/db",
				"PORT":         "8080",
			},
			expectErr: false,
		},
		{
			name:      "invalid format - no equals",
			content:   `INVALID_LINE`,
			expectErr: true,
		},
		{
			name:      "empty file",
			content:   ``,
			expected:  map[string]string{},
			expectErr: false,
		},
		{
			name: "only comments",
			content: `# Comment 1
# Comment 2`,
			expected:  map[string]string{},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			envFile := filepath.Join(tmpDir, ".env")

			if err := os.WriteFile(envFile, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			result, err := loadEnvFile(envFile)

			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d variables, got %d", len(tt.expected), len(result))
			}

			for key, expectedValue := range tt.expected {
				if actualValue, ok := result[key]; !ok {
					t.Errorf("Expected key %s not found", key)
				} else if actualValue != expectedValue {
					t.Errorf("For key %s: expected %q, got %q", key, expectedValue, actualValue)
				}
			}
		})
	}
}

func TestLoadEnvFileNotFound(t *testing.T) {
	_, err := loadEnvFile("/nonexistent/file.env")
	if err == nil {
		t.Error("Expected error for nonexistent file")
	}
}

func TestInheritedMCPEnv(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin", "HOME=/home/test", "XDG_CACHE_HOME=/tmp/cache",
		"PICOCLAW_API_KEY=private", "CUSTOM_TOKEN=private", "EMPTY=", "INVALID",
	}

	filtered := inheritedMCPEnv(parent, false)
	if len(filtered) != 3 || filtered["PATH"] != "/usr/bin" ||
		filtered["HOME"] != "/home/test" || filtered["XDG_CACHE_HOME"] != "/tmp/cache" {
		t.Fatalf("filtered environment = %v, want only safe process variables", filtered)
	}
	if _, ok := filtered["PICOCLAW_API_KEY"]; ok {
		t.Fatal("PicoClaw credential leaked to MCP server")
	}

	full := inheritedMCPEnv(parent, true)
	if len(full) != 6 || full["CUSTOM_TOKEN"] != "private" || full["EMPTY"] != "" {
		t.Fatalf("inherited environment = %v, want all valid parent variables", full)
	}
}

func TestEnvFilePriority(t *testing.T) {
	// Create a temporary .env file
	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, ".env")

	envContent := `API_KEY=from_file
DATABASE_URL=from_file
SHARED_VAR=from_file`

	if err := os.WriteFile(envFile, []byte(envContent), 0o644); err != nil {
		t.Fatalf("Failed to create .env file: %v", err)
	}

	// Load envFile
	envVars, err := loadEnvFile(envFile)
	if err != nil {
		t.Fatalf("Failed to load env file: %v", err)
	}

	// Verify envFile variables
	if envVars["API_KEY"] != "from_file" {
		t.Errorf("Expected API_KEY=from_file, got %s", envVars["API_KEY"])
	}

	// Simulate config.Env overriding envFile
	configEnv := map[string]string{
		"SHARED_VAR": "from_config",
		"NEW_VAR":    "from_config",
	}

	// Merge: envFile first, then config overrides
	merged := make(map[string]string)
	for k, v := range envVars {
		merged[k] = v
	}
	for k, v := range configEnv {
		merged[k] = v
	}

	// Verify priority: config.Env should override envFile
	if merged["SHARED_VAR"] != "from_config" {
		t.Errorf(
			"Expected SHARED_VAR=from_config (config should override file), got %s",
			merged["SHARED_VAR"],
		)
	}
	if merged["API_KEY"] != "from_file" {
		t.Errorf("Expected API_KEY=from_file, got %s", merged["API_KEY"])
	}
	if merged["NEW_VAR"] != "from_config" {
		t.Errorf("Expected NEW_VAR=from_config, got %s", merged["NEW_VAR"])
	}
}

func TestLoadFromMCPConfig_EmptyWorkspaceWithRelativeEnvFile(t *testing.T) {
	mgr := NewManager()

	mcpCfg := config.MCPConfig{
		ToolConfig: config.ToolConfig{
			Enabled: true,
		},
		Servers: map[string]config.MCPServerConfig{
			"test-server": {
				Enabled: true,
				Command: "echo",
				Args:    []string{"ok"},
				EnvFile: ".env",
			},
		},
	}

	err := mgr.LoadFromMCPConfig(context.Background(), mcpCfg, "")
	if err == nil {
		t.Fatal("expected error for relative env_file with empty workspace path, got nil")
	}

	if !strings.Contains(err.Error(), "workspace path is empty") {
		t.Fatalf("expected workspace path validation error, got: %v", err)
	}
}

func TestNewManager_InitialState(t *testing.T) {
	mgr := NewManager()
	if mgr == nil {
		t.Fatal("expected manager instance, got nil")
	}
	if len(mgr.GetServers()) != 0 {
		t.Fatalf("expected no servers on new manager, got %d", len(mgr.GetServers()))
	}
}

func TestConnectServerPublishesRuntimeEvents(t *testing.T) {
	originalConnectServerFunc := connectServerFunc
	t.Cleanup(func() {
		connectServerFunc = originalConnectServerFunc
	})

	eventBus := runtimeevents.NewBus()
	defer func() {
		if err := eventBus.Close(); err != nil {
			t.Errorf("event bus close failed: %v", err)
		}
	}()

	_, eventsCh, err := eventBus.Channel().OfKind(
		runtimeevents.KindMCPServerConnected,
		runtimeevents.KindMCPServerFailed,
	).SubscribeChan(t.Context(), runtimeevents.SubscribeOptions{Name: "mcp-events", Buffer: 2})
	if err != nil {
		t.Fatalf("SubscribeChan failed: %v", err)
	}

	connectServerFunc = func(
		_ context.Context,
		name string,
		cfg config.MCPServerConfig,
	) (*ServerConnection, error) {
		if name == "bad" {
			return nil, fmt.Errorf("connect failed")
		}
		return &ServerConnection{
			Config: cfg,
			Tools:  []*sdkmcp.Tool{{Name: "echo"}},
		}, nil
	}

	mgr := NewManager(WithRuntimeEvents(eventBus))
	err = mgr.ConnectServer(context.Background(), "good", config.MCPServerConfig{
		Type:    "stdio",
		Command: "echo",
	})
	if err != nil {
		t.Fatalf("ConnectServer(good) error = %v", err)
	}
	connected := receiveMCPRuntimeEvent(t, eventsCh)
	if connected.Kind != runtimeevents.KindMCPServerConnected ||
		connected.Source.Name != "good" ||
		connected.Severity != runtimeevents.SeverityInfo {
		t.Fatalf("connected event = %+v", connected)
	}
	if connected.Attrs["server"] != "good" ||
		connected.Attrs["type"] != "stdio" ||
		connected.Attrs["tool_count"] != 1 {
		t.Fatalf("connected attrs = %#v", connected.Attrs)
	}
	if status := mgr.GetServerStatuses()["good"]; status.State != ConnectionConnected || len(status.Tools) != 1 {
		t.Fatalf("connected status = %#v", status)
	}

	err = mgr.ConnectServer(context.Background(), "bad", config.MCPServerConfig{
		Type:    "stdio",
		Command: "echo",
	})
	if err == nil {
		t.Fatal("expected ConnectServer(bad) to fail")
	}
	failed := receiveMCPRuntimeEvent(t, eventsCh)
	if failed.Kind != runtimeevents.KindMCPServerFailed ||
		failed.Source.Name != "bad" ||
		failed.Severity != runtimeevents.SeverityError {
		t.Fatalf("failed event = %+v", failed)
	}
	if failed.Attrs["server"] != "bad" || failed.Attrs["error"] != "connect failed" {
		t.Fatalf("failed attrs = %#v", failed.Attrs)
	}
	if status := mgr.GetServerStatuses()["bad"]; status.State != ConnectionError || status.Error != "connect failed" {
		t.Fatalf("failed status = %#v", status)
	}
}

func receiveMCPRuntimeEvent(t *testing.T, ch <-chan runtimeevents.Event) runtimeevents.Event {
	t.Helper()

	select {
	case evt, ok := <-ch:
		if !ok {
			t.Fatal("runtime event channel closed before expected event")
		}
		return evt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime event")
		return runtimeevents.Event{}
	}
}

func TestLoadFromMCPConfig_DisabledOrEmptyServers(t *testing.T) {
	mgr := NewManager()

	err := mgr.LoadFromMCPConfig(
		context.Background(),
		config.MCPConfig{ToolConfig: config.ToolConfig{Enabled: false}},
		"/tmp",
	)
	if err != nil {
		t.Fatalf("expected nil error when MCP disabled, got: %v", err)
	}

	err = mgr.LoadFromMCPConfig(
		context.Background(),
		config.MCPConfig{ToolConfig: config.ToolConfig{Enabled: true}},
		"/tmp",
	)
	if err != nil {
		t.Fatalf("expected nil error when no servers configured, got: %v", err)
	}
}

func TestGetServers_ReturnsCopy(t *testing.T) {
	mgr := NewManager()
	mgr.servers["s1"] = &ServerConnection{}

	servers := mgr.GetServers()
	delete(servers, "s1")

	if _, ok := mgr.GetServer("s1"); !ok {
		t.Fatal("expected internal manager state to remain unchanged")
	}
}

func TestGetAllTools_FiltersEmptyTools(t *testing.T) {
	mgr := NewManager()
	mgr.servers["empty"] = &ServerConnection{Tools: nil}
	mgr.servers["with-tools"] = &ServerConnection{Tools: []*sdkmcp.Tool{{}}}

	all := mgr.getAllTools()
	if _, ok := all["empty"]; ok {
		t.Fatal("expected server without tools to be excluded")
	}
	if _, ok := all["with-tools"]; !ok {
		t.Fatal("expected server with tools to be included")
	}
}

func TestCallTool_ErrorsForClosedOrMissingServer(t *testing.T) {
	t.Run("manager closed", func(t *testing.T) {
		mgr := NewManager()
		mgr.closed.Store(true)

		_, err := mgr.CallTool(context.Background(), "s1", "tool", nil)
		if err == nil || !strings.Contains(err.Error(), "manager is closed") {
			t.Fatalf("expected manager closed error, got: %v", err)
		}
	})

	t.Run("server missing", func(t *testing.T) {
		mgr := NewManager()

		_, err := mgr.CallTool(context.Background(), "missing", "tool", nil)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected server not found error, got: %v", err)
		}
	})
}

func TestConnectServer_StreamableHTTPRequestResponseMode(t *testing.T) {
	t.Parallel()

	for _, transportType := range []string{"http", "streamable-http"} {
		t.Run(transportType, func(t *testing.T) {
			t.Parallel()

			server := sdkmcp.NewServer(&sdkmcp.Implementation{
				Name:    "streamable-test-server",
				Version: "1.0.0",
			}, nil)
			sdkmcp.AddTool(server, &sdkmcp.Tool{
				Name:        "echo",
				Description: "Echo test tool",
			}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args map[string]any) (*sdkmcp.CallToolResult, any, error) {
				return &sdkmcp.CallToolResult{
					Content: []sdkmcp.Content{
						&sdkmcp.TextContent{Text: "ok"},
					},
				}, nil, nil
			})

			type observedRequest struct {
				Method        string
				SessionID     string
				Authorization string
			}

			var (
				mu       sync.Mutex
				observed []observedRequest
			)

			handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
				return server
			}, nil)
			httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				observed = append(observed, observedRequest{
					Method:        r.Method,
					SessionID:     r.Header.Get("Mcp-Session-Id"),
					Authorization: r.Header.Get("Authorization"),
				})
				mu.Unlock()
				handler.ServeHTTP(w, r)
			}))
			defer httpServer.Close()

			conn, err := connectServer(context.Background(), "streamable", config.MCPServerConfig{
				Enabled: true,
				Type:    transportType,
				URL:     httpServer.URL,
				Headers: map[string]string{
					"Authorization": "Bearer test-token",
				},
			})
			if err != nil {
				t.Fatalf("connectServer(%q) error = %v", transportType, err)
			}
			if got := len(conn.Tools); got != 1 {
				t.Fatalf("len(conn.Tools) = %d, want 1", got)
			}
			if got := conn.Session.ID(); got == "" {
				t.Fatal("expected non-empty streamable session ID")
			}
			if err := conn.Session.Close(); err != nil {
				t.Fatalf("Session.Close() error = %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			var (
				getCount            int
				postCount           int
				deleteCount         int
				postWithSession     bool
				deleteWithSession   bool
				requestsWithAuth    int
				requestsWithoutAuth []string
			)

			for _, req := range observed {
				switch req.Method {
				case http.MethodGet:
					getCount++
				case http.MethodPost:
					postCount++
					if req.SessionID != "" {
						postWithSession = true
					}
				case http.MethodDelete:
					deleteCount++
					if req.SessionID != "" {
						deleteWithSession = true
					}
				}

				if req.Authorization == "Bearer test-token" {
					requestsWithAuth++
				} else {
					requestsWithoutAuth = append(requestsWithoutAuth, req.Method)
				}
			}

			if getCount != 0 {
				t.Fatalf("expected no standalone GET requests for %q transport, saw %d", transportType, getCount)
			}
			if postCount == 0 {
				t.Fatal("expected POST requests during streamable HTTP handshake")
			}
			if deleteCount != 1 {
				t.Fatalf("DELETE count = %d, want 1", deleteCount)
			}
			if !postWithSession {
				t.Fatal("expected at least one POST request with Mcp-Session-Id")
			}
			if !deleteWithSession {
				t.Fatal("expected DELETE request with Mcp-Session-Id")
			}
			if requestsWithAuth != len(observed) {
				t.Fatalf("Authorization header missing on requests: %v", requestsWithoutAuth)
			}
		})
	}
}

func TestConnectServer_DoesNotForwardHeadersToRedirectTargets(t *testing.T) {
	t.Parallel()

	leaked := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/mcp", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	conn, err := connectServer(context.Background(), "redirect", config.MCPServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     origin.URL + "/mcp",
		Headers: map[string]string{"Authorization": "Bearer secret-token"},
	})
	if conn != nil {
		_ = conn.Session.Close()
	}
	if err == nil {
		t.Fatal("connectServer() followed a cross-origin redirect, want error")
	}
	select {
	case header := <-leaked:
		t.Fatalf("redirect target received a request; Authorization header = %q", header)
	default:
	}
}

func TestLoadFromMCPConfig_AllServersFailedAfterPreviousLoad(t *testing.T) {
	originalConnectServerFunc := connectServerFunc
	t.Cleanup(func() { connectServerFunc = originalConnectServerFunc })

	connectServerFunc = func(_ context.Context, name string, _ config.MCPServerConfig) (*ServerConnection, error) {
		if name == "failing" {
			return nil, errors.New("connect refused")
		}
		conn, _, err := newScriptedServerConnection("session-"+name, nil, nil)
		return conn, err
	}

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	preexisting := config.MCPServerConfig{Enabled: true}
	if err := mgr.ConnectServer(context.Background(), "preexisting", preexisting); err != nil {
		t.Fatalf("ConnectServer(preexisting) error = %v", err)
	}

	err := mgr.LoadFromMCPConfig(context.Background(), config.MCPConfig{
		ToolConfig: config.ToolConfig{Enabled: true},
		Servers: map[string]config.MCPServerConfig{
			"failing": {Enabled: true, Type: "http", URL: "https://example.invalid/mcp"},
		},
	}, "")
	if err == nil {
		t.Fatal("expected an aggregated error when every configured server failed, got nil")
	}
}

func TestConnectServer_FollowsSameOriginRedirect(t *testing.T) {
	t.Parallel()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "redirect-test-server",
		Version: "1.0.0",
	}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo test tool",
	}, func(context.Context, *sdkmcp.CallToolRequest, map[string]any) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}},
		}, nil, nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return server
	}, nil)

	var (
		mu                sync.Mutex
		redirectedAuth    []string
		redirectedRequest int
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			http.Redirect(w, r, "/mcp2", http.StatusTemporaryRedirect)
			return
		}
		mu.Lock()
		redirectedRequest++
		redirectedAuth = append(redirectedAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	conn, err := connectServer(context.Background(), "redirect", config.MCPServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     httpServer.URL + "/mcp",
		Headers: map[string]string{"Authorization": "Bearer test-token"},
	})
	if err != nil {
		t.Fatalf("connectServer() error = %v", err)
	}
	if got := len(conn.Tools); got != 1 {
		t.Fatalf("len(conn.Tools) = %d, want 1", got)
	}
	if err := conn.Session.Close(); err != nil {
		t.Fatalf("Session.Close() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if redirectedRequest == 0 {
		t.Fatal("same-origin redirect target never received a request")
	}
	for _, header := range redirectedAuth {
		if header != "Bearer test-token" {
			t.Fatalf("redirect target Authorization = %q, want the configured header", header)
		}
	}
}

func TestConnectServer_ClientProtocolHeadersWinOverConfigured(t *testing.T) {
	t.Parallel()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "header-precedence-server",
		Version: "1.0.0",
	}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo test tool",
	}, func(context.Context, *sdkmcp.CallToolRequest, map[string]any) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}},
		}, nil, nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return server
	}, nil)

	var (
		mu       sync.Mutex
		observed []observedHeaderRequest
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observed = append(observed, observedHeaderRequest{
			method:      r.Method,
			accept:      r.Header.Get("Accept"),
			contentType: r.Header.Get("Content-Type"),
			custom:      r.Header.Get("X-Custom"),
		})
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	conn, err := connectServer(context.Background(), "headers", config.MCPServerConfig{
		Enabled: true,
		Type:    "http",
		URL:     httpServer.URL,
		Headers: map[string]string{
			"Accept":       "application/bogus",
			"Content-Type": "application/bogus",
			"X-Custom":     "configured",
		},
	})
	if err != nil {
		t.Fatalf("connectServer() error = %v", err)
	}
	if err := conn.Session.Close(); err != nil {
		t.Fatalf("Session.Close() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) == 0 {
		t.Fatal("no requests observed")
	}
	posts := 0
	for _, header := range observed {
		if header.custom != "configured" {
			t.Fatalf("X-Custom = %q, want the configured value", header.custom)
		}
		if header.method != http.MethodPost {
			continue
		}
		posts++
		if header.contentType != "application/json" {
			t.Fatalf("POST Content-Type = %q, want the client protocol header", header.contentType)
		}
		if !strings.Contains(header.accept, "application/json") ||
			!strings.Contains(header.accept, "text/event-stream") {
			t.Fatalf("POST Accept = %q, want the client protocol header", header.accept)
		}
	}
	if posts == 0 {
		t.Fatal("no POST requests observed during the handshake")
	}
}

// observedHeaderRequest is the subset of request headers the precedence test
// inspects, recorded per method because the SDK only generates some of them on
// message-bearing requests.
type observedHeaderRequest struct {
	method      string
	accept      string
	contentType string
	custom      string
}

func TestConnectServer_ClosesPreviousSessionWithSameName(t *testing.T) {
	originalConnectServerFunc := connectServerFunc
	t.Cleanup(func() { connectServerFunc = originalConnectServerFunc })

	var transports []*scriptedTransport
	connectServerFunc = func(_ context.Context, _ string, _ config.MCPServerConfig) (*ServerConnection, error) {
		conn, transport, err := newScriptedServerConnection("session", nil, nil)
		if err != nil {
			return nil, err
		}
		transports = append(transports, transport)
		return conn, nil
	}

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	cfg := config.MCPServerConfig{Enabled: true, Type: "http", URL: "https://example.invalid/mcp"}
	if err := mgr.ConnectServer(context.Background(), "dup", cfg); err != nil {
		t.Fatalf("first ConnectServer() error = %v", err)
	}
	if err := mgr.ConnectServer(context.Background(), "dup", cfg); err != nil {
		t.Fatalf("second ConnectServer() error = %v", err)
	}

	if len(transports) != 2 {
		t.Fatalf("connect calls = %d, want 2", len(transports))
	}
	if !transportClosed(transports[0]) {
		t.Fatal("previous connection session was not closed")
	}
	if transportClosed(transports[1]) {
		t.Fatal("current connection session must stay open")
	}
}

func transportClosed(transport *scriptedTransport) bool {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.closed
}

// A streamable session sends a DELETE on close, so closing a replaced session
// while holding m.mu would stall every MCP operation behind a network call.
// This pins that the close runs after the registry lock is released.
func TestConnectServer_ClosesReplacedSessionOutsideTheManagerLock(t *testing.T) {
	originalConnectServerFunc := connectServerFunc
	t.Cleanup(func() { connectServerFunc = originalConnectServerFunc })

	closeEntered := make(chan struct{})
	releaseClose := make(chan struct{})
	var firstTransport bool
	connectServerFunc = func(_ context.Context, _ string, _ config.MCPServerConfig) (*ServerConnection, error) {
		conn, transport, err := newScriptedServerConnection("session", nil, nil)
		if err != nil {
			return nil, err
		}
		if !firstTransport {
			firstTransport = true
			transport.onClose = func() {
				close(closeEntered)
				<-releaseClose
			}
		}
		return conn, nil
	}

	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close() })

	cfg := config.MCPServerConfig{Enabled: true, Type: "http", URL: "https://example.invalid/mcp"}
	if err := mgr.ConnectServer(context.Background(), "dup", cfg); err != nil {
		t.Fatalf("first ConnectServer() error = %v", err)
	}

	connectDone := make(chan error, 1)
	go func() { connectDone <- mgr.ConnectServer(context.Background(), "dup", cfg) }()

	select {
	case <-closeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("replaced session was not closed")
	}

	// If the manager lock were still held by the closing path, this read would
	// block until releaseClose.
	readDone := make(chan struct{})
	go func() {
		_ = mgr.GetServers()
		close(readDone)
	}()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		close(releaseClose)
		t.Fatal("manager registry stayed locked while the replaced session was closing")
	}

	close(releaseClose)
	select {
	case err := <-connectDone:
		if err != nil {
			t.Fatalf("second ConnectServer() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second ConnectServer() did not finish")
	}
}

func TestCallTool_ReconnectsWhenHTTPServerLosesSession(t *testing.T) {
	originalConnectServerFunc := connectServerFunc
	t.Cleanup(func() {
		connectServerFunc = originalConnectServerFunc
	})

	staleConn, staleTransport, err := newScriptedServerConnection(
		"session-1",
		nil,
		fmt.Errorf(`sending "tools/call": failed to connect (session ID: session-1): %w`, sdkmcp.ErrSessionMissing),
	)
	if err != nil {
		t.Fatalf("newScriptedServerConnection(stale) error = %v", err)
	}
	freshConn, freshTransport, err := newScriptedServerConnection(
		"session-2",
		&sdkmcp.CallToolResult{
			Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "reconnected"},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("newScriptedServerConnection(fresh) error = %v", err)
	}

	connectCalls := 0
	connectServerFunc = func(ctx context.Context, name string, cfg config.MCPServerConfig) (*ServerConnection, error) {
		connectCalls++
		if connectCalls == 1 {
			return freshConn, nil
		}
		return nil, fmt.Errorf("unexpected reconnect attempt %d", connectCalls)
	}

	mgr := NewManager()
	mgr.servers["flaky"] = staleConn
	cancelCalled := false
	staleConn.cancel = func() { cancelCalled = true }

	result, err := mgr.CallTool(context.Background(), "flaky", "echo", map[string]any{
		"query": "hello",
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("CallTool() returned unexpected content: %#v", result)
	}

	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("CallTool() content type = %T, want *sdkmcp.TextContent", result.Content[0])
	}
	if text.Text != "reconnected" {
		t.Fatalf("CallTool() text = %q, want %q", text.Text, "reconnected")
	}

	conn, ok := mgr.GetServer("flaky")
	if !ok {
		t.Fatal("expected flaky server to remain connected after reconnect")
	}
	if conn.Session.ID() != "session-2" {
		t.Fatalf("Session.ID() = %q, want %q", conn.Session.ID(), "session-2")
	}
	if connectCalls != 1 {
		t.Fatalf("connectCalls = %d, want 1", connectCalls)
	}
	if staleTransport.toolCallCalls != 1 {
		t.Fatalf("stale toolCallCalls = %d, want 1", staleTransport.toolCallCalls)
	}
	if freshTransport.toolCallCalls != 1 {
		t.Fatalf("fresh toolCallCalls = %d, want 1", freshTransport.toolCallCalls)
	}
	if !cancelCalled {
		t.Fatal("stale connection cancel was not called on reconnect")
	}
}

func TestClose_IdempotentOnEmptyManager(t *testing.T) {
	mgr := NewManager()

	if err := mgr.Close(); err != nil {
		t.Fatalf("first close should succeed, got: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second close should be idempotent, got: %v", err)
	}
}

func newScriptedServerConnection(
	sessionID string,
	toolCallResult *sdkmcp.CallToolResult,
	toolCallErr error,
) (*ServerConnection, *scriptedTransport, error) {
	transport := &scriptedTransport{
		sessionID:      sessionID,
		toolCallResult: toolCallResult,
		toolCallErr:    toolCallErr,
	}

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "picoclaw-test",
		Version: "1.0.0",
	}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		return nil, nil, err
	}

	return &ServerConnection{
		Config:  config.MCPServerConfig{Enabled: true, Type: "http", URL: "https://example.invalid/mcp"},
		Session: session,
		Tools: []*sdkmcp.Tool{
			{
				Name:        "echo",
				Description: "Echo test tool",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}, transport, nil
}

type scriptedTransport struct {
	sessionID      string
	toolCallResult *sdkmcp.CallToolResult
	toolCallErr    error

	mu            sync.Mutex
	toolCallCalls int
	closed        bool
	incoming      chan jsonrpc.Message
	// onClose runs after the transport is marked closed and before Close
	// returns. Tests use it to observe close timing, for example to prove the
	// manager lock is not held while a replaced session is closed.
	onClose func()
}

func (t *scriptedTransport) Connect(context.Context) (sdkmcp.Connection, error) {
	if t.incoming == nil {
		t.incoming = make(chan jsonrpc.Message, 4)
	}
	return t, nil
}

func (t *scriptedTransport) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-t.incoming:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

func (t *scriptedTransport) Write(ctx context.Context, msg jsonrpc.Message) error {
	req, ok := msg.(*jsonrpc.Request)
	if !ok {
		return nil
	}

	switch req.Method {
	case "server/discover":
		// Simulate a legacy server so newer clients fall back to initialize
		// without treating the unsupported method as a transport failure.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t.incoming <- &jsonrpc.Response{
			ID: req.ID,
			Error: &jsonrpc.Error{
				Code:    jsonrpc.CodeMethodNotFound,
				Message: "method not found",
			},
		}:
			return nil
		}

	case "initialize":
		payload, err := json.Marshal(&sdkmcp.InitializeResult{
			ProtocolVersion: "2025-11-25",
			ServerInfo: &sdkmcp.Implementation{
				Name:    "scripted-test-server",
				Version: "1.0.0",
			},
			Capabilities: &sdkmcp.ServerCapabilities{
				Tools: &sdkmcp.ToolCapabilities{},
			},
		})
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t.incoming <- &jsonrpc.Response{ID: req.ID, Result: payload}:
			return nil
		}

	case "notifications/initialized":
		return nil

	case "tools/call":
		t.mu.Lock()
		t.toolCallCalls++
		t.mu.Unlock()

		if t.toolCallErr != nil {
			return t.toolCallErr
		}

		payload, err := json.Marshal(t.toolCallResult)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t.incoming <- &jsonrpc.Response{ID: req.ID, Result: payload}:
			return nil
		}
	}

	return fmt.Errorf("unexpected method %q", req.Method)
}

func (t *scriptedTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	close(t.incoming)
	hook := t.onClose
	t.mu.Unlock()

	if hook != nil {
		hook()
	}
	return nil
}

func (t *scriptedTransport) SessionID() string {
	return t.sessionID
}

// TestManager_CloseWaitsForInFlightCallTool verifies that Close does not close
// server sessions until every in-flight CallTool has completed. This is the
// guarantee that the locked closed-swap in Close must preserve.
func TestManager_CloseWaitsForInFlightCallTool(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "srv", Version: "1"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "block"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, _ map[string]any) (*sdkmcp.CallToolResult, any, error) {
			close(started)
			<-release
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "done"}},
			}, nil, nil
		})

	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	ctx := t.Context()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer serverSession.Close()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "picoclaw", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	mgr := NewManager()
	mgr.mu.Lock()
	mgr.servers["srv"] = &ServerConnection{Session: clientSession}
	mgr.mu.Unlock()

	callDone := make(chan error, 1)
	go func() {
		_, callErr := mgr.CallTool(ctx, "srv", "block", nil)
		callDone <- callErr
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- mgr.Close() }()

	select {
	case <-closeDone:
		t.Fatal("Close returned while a CallTool was still in flight")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	if callErr := <-callDone; callErr != nil {
		t.Fatalf("CallTool error: %v", callErr)
	}
	select {
	case closeErr := <-closeDone:
		if closeErr != nil {
			t.Fatalf("Close error: %v", closeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the in-flight call completed")
	}
}

func TestLoadFromMCPConfigBoundsConcurrentConnects(t *testing.T) {
	originalConnectServerFunc := connectServerFunc
	t.Cleanup(func() { connectServerFunc = originalConnectServerFunc })

	var mu sync.Mutex
	var current, maxObserved int
	connectServerFunc = func(
		_ context.Context,
		name string,
		cfg config.MCPServerConfig,
	) (*ServerConnection, error) {
		mu.Lock()
		current++
		if current > maxObserved {
			maxObserved = current
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		current--
		mu.Unlock()
		return &ServerConnection{Config: cfg}, nil
	}

	servers := make(map[string]config.MCPServerConfig, mcpMaxConcurrentConnect*3)
	for i := 0; i < mcpMaxConcurrentConnect*3; i++ {
		servers[fmt.Sprintf("server-%d", i)] = config.MCPServerConfig{
			Enabled: true,
			Type:    "stdio",
			Command: "echo",
		}
	}

	mgr := NewManager()
	if err := mgr.LoadFromMCPConfig(
		context.Background(),
		config.MCPConfig{Enabled: true, Servers: servers},
		"",
	); err != nil {
		t.Fatalf("LoadFromMCPConfig: %v", err)
	}

	if maxObserved > mcpMaxConcurrentConnect {
		t.Fatalf("max concurrent connects = %d, want <= %d", maxObserved, mcpMaxConcurrentConnect)
	}
	if len(mgr.GetServers()) != len(servers) {
		t.Fatalf("connected servers = %d, want %d", len(mgr.GetServers()), len(servers))
	}
}

func TestShouldReconnectCallError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed ErrSessionMissing", fmt.Errorf("sending: %w", sdkmcp.ErrSessionMissing), true},
		{"ErrSessionMissing text only", fmt.Errorf("failed to connect: session not found"), true},
		{
			"404 with JSON-RPC error body",
			fmt.Errorf(
				`calling "tools/call": sending "tools/call": Unknown mcp-session-id: rejected by transport: Not Found`,
			),
			true,
		},
		{
			"404 with other JSON-RPC error body",
			fmt.Errorf(`sending "tools/call": Session terminated: rejected by transport: Not Found`),
			true,
		},
		{"transient 503", fmt.Errorf(`rejected by transport: sending "tools/call": Service Unavailable`), false},
		{
			"401 with JSON-RPC error body",
			fmt.Errorf(`sending "tools/call": Unauthorized: rejected by transport: Unauthorized`),
			false,
		},
		{"unrelated protocol error", fmt.Errorf(`calling "tools/call": invalid params`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldReconnectCallError(tt.err); got != tt.want {
				t.Fatalf("shouldReconnectCallError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// A server that expires a session answers the stale Mcp-Session-Id with HTTP 404
// plus a JSON-RPC error body; CallTool must re-initialize and retry transparently.
func TestCallTool_ReconnectsWhenServerExpiresSessionWith404JSONRPCBody(t *testing.T) {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "expiring-server", Version: "1.0.0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name:        "echo",
		Description: "Echo test tool",
	}, func(context.Context, *sdkmcp.CallToolRequest, map[string]any) (*sdkmcp.CallToolResult, any, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ok"}}}, nil, nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil)

	var (
		mu      sync.Mutex
		expired string
	)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		isExpired := expired != "" && r.Header.Get("Mcp-Session-Id") == expired
		mu.Unlock()
		if isExpired {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(
				w,
				`{"jsonrpc":"2.0","error":{"code":-32000,"message":"Unknown mcp-session-id"},"id":1}`,
			)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer httpServer.Close()

	conn, err := connectServer(context.Background(), "expiring", config.MCPServerConfig{
		Enabled: true,
		Type:    "streamable-http",
		URL:     httpServer.URL,
	})
	if err != nil {
		t.Fatalf("connectServer() error = %v", err)
	}
	oldID := conn.Session.ID()
	if oldID == "" {
		t.Fatal("expected non-empty streamable session ID")
	}

	mgr := NewManager()
	mgr.servers["expiring"] = conn
	t.Cleanup(func() {
		if current, ok := mgr.GetServer("expiring"); ok {
			_ = current.Session.Close()
		}
	})

	mu.Lock()
	expired = oldID
	mu.Unlock()

	result, err := mgr.CallTool(context.Background(), "expiring", "echo", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result == nil || len(result.Content) != 1 {
		t.Fatalf("CallTool() returned unexpected content: %#v", result)
	}
	if text, ok := result.Content[0].(*sdkmcp.TextContent); !ok || text.Text != "ok" {
		t.Fatalf("CallTool() content = %#v, want text %q", result.Content[0], "ok")
	}

	current, ok := mgr.GetServer("expiring")
	if !ok {
		t.Fatal("expected server to remain registered after reconnect")
	}
	if newID := current.Session.ID(); newID == "" || newID == oldID {
		t.Fatalf("Session.ID() = %q, want a fresh ID different from %q", newID, oldID)
	}
}
