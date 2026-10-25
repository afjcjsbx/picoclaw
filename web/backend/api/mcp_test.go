package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
)

func TestHandleGetMCPStatusFallbackRedactsSecrets(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	cfg := config.DefaultConfig()
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers["secure"] = config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"server", "--api-key", "sk-fake-dashboard-secret"},
		Env:     map[string]string{"OPENAI_API_KEY": "sk-fake-dashboard-secret"},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerMCPRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/agents/mcp", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "sk-fake-dashboard-secret") {
		t.Fatalf("response leaked secret: %s", recorder.Body.String())
	}
	var response picomcp.DashboardResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RuntimeAvailable || len(response.Servers) != 1 ||
		response.Servers[0].Status != picomcp.ConnectionDisconnected {
		t.Fatalf("unexpected fallback response: %#v", response)
	}
}

func TestReconcileMCPDashboardResponseUsesLatestConfiguration(t *testing.T) {
	deferred := true
	cfg := config.DefaultConfig()
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"disabled": {
			Enabled:  false,
			Deferred: &deferred,
			Type:     "stdio",
			Command:  "npx",
		},
	}

	response := reconcileMCPDashboardResponse(picomcp.DashboardResponse{
		Enabled:          true,
		RuntimeAvailable: true,
		Servers: []picomcp.DashboardServer{
			{
				Name:    "disabled",
				Enabled: true,
				Status:  picomcp.ConnectionConnected,
				Tools:   []picomcp.DashboardTool{{Name: "stale-tool"}},
			},
			{
				Name:    "removed",
				Enabled: true,
				Status:  picomcp.ConnectionConnected,
			},
		},
	}, cfg)

	if !response.RuntimeAvailable || len(response.Servers) != 1 {
		t.Fatalf("response = %#v", response)
	}
	server := response.Servers[0]
	if server.Name != "disabled" || server.Enabled || server.Status != picomcp.ConnectionDisabled {
		t.Fatalf("server = %#v", server)
	}
	if !server.Deferred || len(server.Tools) != 0 {
		t.Fatalf("stale live data survived config change: %#v", server)
	}
}

func TestConfigAPIRedactsAndPreservesMCPSecrets(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	cfg := config.DefaultConfig()
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers["secure"] = config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"server", "--token=argument-secret"},
		Env:     map[string]string{"OPENAI_API_KEY": "environment-secret"},
		Headers: map[string]string{"Authorization": "header-secret"},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerConfigRoutes(mux)

	getRecorder := httptest.NewRecorder()
	mux.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body=%s", getRecorder.Code, getRecorder.Body.String())
	}
	for _, secret := range []string{"argument-secret", "environment-secret", "header-secret"} {
		if strings.Contains(getRecorder.Body.String(), secret) {
			t.Fatalf("GET /api/config leaked %q", secret)
		}
	}

	var webConfig map[string]any
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &webConfig); err != nil {
		t.Fatal(err)
	}
	putRecorder := httptest.NewRecorder()
	mux.ServeHTTP(
		putRecorder,
		httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(getRecorder.Body.String())),
	)
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	assertStoredMCPSecrets(t, configPath, "secure")

	patchBody := `{"tools":{"mcp":{"servers":{"secure":{"enabled":false,"deferred":true}}}}}`
	patchRecorder := httptest.NewRecorder()
	mux.ServeHTTP(patchRecorder, httptest.NewRequest(http.MethodPatch, "/api/config", strings.NewReader(patchBody)))
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}
	assertStoredMCPSecrets(t, configPath, "secure")
	stored, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server := stored.Tools.MCP.Servers["secure"]
	if server.Enabled || server.Deferred == nil || !*server.Deferred {
		t.Fatalf("server settings were not patched: %#v", server)
	}
}

func TestConfigAPIPreservesMCPSecretsWhenServerIsRenamed(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	cfg := config.DefaultConfig()
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers["before"] = config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "npx",
		Args:    []string{"server", "--token=argument-secret"},
		Env:     map[string]string{"OPENAI_API_KEY": "environment-secret"},
		Headers: map[string]string{"Authorization": "header-secret"},
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerConfigRoutes(mux)

	getRecorder := httptest.NewRecorder()
	mux.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	var webConfig map[string]any
	if err := json.Unmarshal(getRecorder.Body.Bytes(), &webConfig); err != nil {
		t.Fatal(err)
	}
	tools := webConfig["tools"].(map[string]any)
	mcpConfig := tools["mcp"].(map[string]any)
	servers := mcpConfig["servers"].(map[string]any)
	servers["after"] = servers["before"]
	delete(servers, "before")
	body, err := json.Marshal(webConfig)
	if err != nil {
		t.Fatal(err)
	}
	putRecorder := httptest.NewRecorder()
	mux.ServeHTTP(putRecorder, httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(string(body))))
	if putRecorder.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", putRecorder.Code, putRecorder.Body.String())
	}
	assertStoredMCPSecrets(t, configPath, "after")
}

func TestConfigAPIPatchRemovesMCPServer(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	cfg := config.DefaultConfig()
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers["remove-me"] = config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "npx",
	}
	cfg.Tools.MCP.Servers["keep-me"] = config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "node",
	}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerConfigRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPatch,
		"/api/config",
		strings.NewReader(`{"tools":{"mcp":{"servers":{"remove-me":null}}}}`),
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	stored, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := stored.Tools.MCP.Servers["remove-me"]; exists {
		t.Fatalf("removed MCP server still exists: %#v", stored.Tools.MCP.Servers)
	}
	if got := stored.Tools.MCP.Servers["keep-me"].Command; got != "node" {
		t.Fatalf("remaining MCP server command = %q, want node", got)
	}
}

func TestConfigAPIPatchCreatesMCPServer(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	cfg := config.DefaultConfig()
	cfg.Tools.MCP.Enabled = true
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerConfigRoutes(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodPatch,
		"/api/config",
		strings.NewReader(
			`{"tools":{"mcp":{"servers":{"new-server":{"enabled":true,"deferred":true,"type":"stdio","command":"npx","args":["-y","example-server"]}}}}}`,
		),
	))
	if recorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	stored, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server, ok := stored.Tools.MCP.Servers["new-server"]
	if !ok || !server.Enabled || server.Deferred == nil || !*server.Deferred || server.Command != "npx" ||
		strings.Join(server.Args, " ") != "-y example-server" {
		t.Fatalf("new MCP server was not stored: %#v", server)
	}
}

func assertStoredMCPSecrets(t *testing.T, configPath, name string) {
	t.Helper()
	stored, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server := stored.Tools.MCP.Servers[name]
	if server.Env["OPENAI_API_KEY"] != "environment-secret" || server.Headers["Authorization"] != "header-secret" ||
		server.Args[1] != "--token=argument-secret" {
		t.Fatalf("stored MCP secrets changed: %#v", server)
	}
}
