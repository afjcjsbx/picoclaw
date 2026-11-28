package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
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
	cfg.Tools.MCP.Discovery.Enabled = true
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

func TestLocalCommandExecutionHelper(t *testing.T) {
	if os.Getenv("PICOCLAW_LOCAL_EXECUTION_HELPER") != "1" {
		return
	}
	marker := os.Getenv("PICOCLAW_LOCAL_EXECUTION_MARKER")
	if marker == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestConfigAPICannotEnableLocalCommandExecution(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			t.Setenv(config.LocalCommandExecutionEnv, "false")
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")
			marker := filepath.Join(dir, "local-command-started")
			envFile := filepath.Join(dir, ".env")
			const secretLine = "PRIVATE_FILE_CONTENT_WITHOUT_EQUALS"
			if err := os.WriteFile(envFile, []byte(secretLine), 0o600); err != nil {
				t.Fatal(err)
			}
			command := []string{os.Args[0], "-test.run=^TestLocalCommandExecutionHelper$"}
			childEnv := map[string]string{
				"PICOCLAW_LOCAL_EXECUTION_HELPER": "1",
				"PICOCLAW_LOCAL_EXECUTION_MARKER": marker,
				config.LocalCommandExecutionEnv:   "true",
			}

			cfg := config.DefaultConfig()
			cfg.Tools.MCP.Enabled = true
			if err := config.SaveConfig(configPath, cfg); err != nil {
				t.Fatal(err)
			}

			payload := map[string]any{
				"allow_local_command_execution": true,
				"tools": map[string]any{
					"mcp": map[string]any{
						"enabled": true,
						"servers": map[string]any{
							"remote": map[string]any{
								"enabled":  true,
								"type":     "stdio",
								"command":  command[0],
								"args":     command[1:],
								"env":      childEnv,
								"env_file": envFile,
							},
						},
					},
				},
				"hooks": map[string]any{
					"enabled": true,
					"processes": map[string]any{
						"remote": map[string]any{
							"enabled": true,
							"command": command,
							"env":     childEnv,
						},
					},
				},
			}
			if method == http.MethodPut {
				data, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				var full map[string]any
				if err := json.Unmarshal(data, &full); err != nil {
					t.Fatal(err)
				}
				for key, value := range payload {
					full[key] = value
				}
				payload = full
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}

			handler := NewHandler(configPath)
			mux := http.NewServeMux()
			handler.registerConfigRoutes(mux)
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(method, "/api/config", bytes.NewReader(body)))
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s status = %d, body=%s", method, recorder.Code, recorder.Body.String())
			}
			if config.LocalCommandExecutionAllowed() {
				t.Fatalf("%s config write enabled parent-process command execution", method)
			}

			stored, err := config.LoadConfig(configPath)
			if err != nil {
				t.Fatalf("LoadConfig() after %s error = %v", method, err)
			}
			server := stored.Tools.MCP.Servers["remote"]

			nativeManager := picomcp.NewManager()
			defer nativeManager.Close()
			err = nativeManager.LoadFromMCPConfig(t.Context(), stored.Tools.MCP, stored.WorkspacePath())
			requireLocalCommandExecutionDisabled(t, err)
			if strings.Contains(err.Error(), secretLine) {
				t.Fatalf("native MCP exposed env_file contents: %v", err)
			}
			requireLocalExecutionMarkerAbsent(t, marker)

			pluginManager := picomcp.NewManager()
			defer pluginManager.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = pluginManager.ConnectPluginServer(ctx, "remote", server, picomcp.PluginRuntimeOptions{
				Lifetime:    ctx,
				Environment: localCommandTestEnv(server.Env),
				Timeout:     time.Second,
			})
			requireLocalCommandExecutionDisabled(t, err)
			requireLocalExecutionMarkerAbsent(t, marker)

			hookCfg := stored.Hooks.Processes["remote"]
			hook, err := agent.NewProcessHook(t.Context(), "remote", agent.ProcessHookOptions{
				Command: hookCfg.Command,
				Env:     localCommandTestEnv(hookCfg.Env),
			})
			if hook != nil {
				_ = hook.Close()
			}
			requireLocalCommandExecutionDisabled(t, err)
			requireLocalExecutionMarkerAbsent(t, marker)
		})
	}
}

func requireLocalCommandExecutionDisabled(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
		t.Fatalf("error = %v, want local command execution disabled", err)
	}
}

func requireLocalExecutionMarkerAbsent(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("disabled local command executed: marker stat error = %v", err)
	}
}

func localCommandTestEnv(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	return env
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
