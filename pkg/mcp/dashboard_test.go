package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestBuildDashboardResponseRedactsSecrets(t *testing.T) {
	apiKey := "sk-test-api-key-value"
	headerToken := "header-secret-token"
	cfg := config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "npx",
		Args: []string{
			"-y",
			"example-server",
			"--api-key",
			apiKey,
			"TOKEN=ghp_exampletoken123456",
		},
		Env: map[string]string{
			"OPENAI_API_KEY": apiKey,
			"LOG_LEVEL":      "debug",
		},
		Headers: map[string]string{"Authorization": headerToken},
	}
	response := BuildDashboardResponse(true, false, true, map[string]ServerStatus{
		"example": {
			Name:   "example",
			Config: cfg,
			State:  ConnectionError,
			Error:  "failed with token=" + headerToken,
			Tools: []*sdkmcp.Tool{{
				Name:        "search",
				Description: "Uses " + apiKey,
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "Search term"},
					},
					"required": []any{"query"},
				},
			}},
		},
	})
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{apiKey, headerToken, "ghp_exampletoken123456"} {
		if strings.Contains(text, secret) {
			t.Fatalf("dashboard response leaked %q: %s", secret, text)
		}
	}
	if !strings.Contains(response.Servers[0].Command, RedactedValue) {
		t.Fatalf("command was not redacted: %q", response.Servers[0].Command)
	}
	if response.Servers[0].LaunchCommand != "npx" {
		t.Fatalf("launch command = %q, want npx", response.Servers[0].LaunchCommand)
	}
	if strings.Contains(strings.Join(response.Servers[0].Args, " "), apiKey) {
		t.Fatalf("args were not redacted: %#v", response.Servers[0].Args)
	}
	params := response.Servers[0].Tools[0].Parameters
	if len(params) != 1 || params[0].Name != "query" || !params[0].Required || params[0].Type != "string" {
		t.Fatalf("unexpected parameter summary: %#v", params)
	}
}

func TestBuildDashboardResponseKeepsConfiguredEnabledWhenIntegrationDisabled(t *testing.T) {
	response := BuildDashboardResponse(false, false, false, map[string]ServerStatus{
		"example": {
			Name: "example",
			Config: config.MCPServerConfig{
				Enabled: true,
			},
		},
	})
	if len(response.Servers) != 1 {
		t.Fatalf("servers = %#v", response.Servers)
	}
	server := response.Servers[0]
	if server.Enabled {
		t.Fatal("effective enabled state = true, want false")
	}
	if !server.ConfiguredEnabled {
		t.Fatal("configured enabled state = false, want true")
	}
}

func TestBuildDashboardResponseResolvesDeferredAgainstDiscovery(t *testing.T) {
	t.Parallel()

	explicitDeferred := true
	explicitVisible := false
	tests := []struct {
		name             string
		discoveryEnabled bool
		deferred         *bool
		want             bool
	}{
		{"discovery off overrides deferred true", false, &explicitDeferred, false},
		{"discovery off keeps explicit visible", false, &explicitVisible, false},
		{"discovery on follows deferred nil", true, nil, true},
		{"discovery on follows deferred true", true, &explicitDeferred, true},
		{"discovery on follows deferred false", true, &explicitVisible, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			response := BuildDashboardResponse(true, tt.discoveryEnabled, false, map[string]ServerStatus{
				"example": {
					Name: "example",
					Config: config.MCPServerConfig{
						Enabled:  true,
						Deferred: tt.deferred,
					},
				},
			})
			if len(response.Servers) != 1 {
				t.Fatalf("servers = %#v", response.Servers)
			}
			if got := response.Servers[0].Deferred; got != tt.want {
				t.Fatalf("Deferred = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedactAndRestoreServerConfig(t *testing.T) {
	original := config.MCPServerConfig{
		Enabled: true,
		Type:    "http",
		Command: "env OPENAI_API_KEY=command-secret npx",
		URL:     "https://user:password@example.test/mcp?token=query-secret&mode=safe",
		Headers: map[string]string{
			"Authorization": "Bearer header-secret",
			"X-Custom":      "custom-value",
		},
	}
	redacted := RedactServerConfig(original)
	if strings.Contains(redacted.Command, "command-secret") {
		t.Fatalf("command was not redacted: %q", redacted.Command)
	}
	if strings.Contains(redacted.URL, "password") || strings.Contains(redacted.URL, "query-secret") {
		t.Fatalf("URL was not redacted: %q", redacted.URL)
	}
	if redacted.Headers["Authorization"] != RedactedValue || redacted.Headers["X-Custom"] != RedactedValue {
		t.Fatalf("headers were not fully redacted: %#v", redacted.Headers)
	}
	restored := RestoreServerConfigRedactions(redacted, original)
	if restored.Command != original.Command || restored.URL != original.URL ||
		restored.Headers["Authorization"] != original.Headers["Authorization"] ||
		restored.Headers["X-Custom"] != original.Headers["X-Custom"] {
		t.Fatalf("redacted values were not restored: %#v", restored)
	}
}

func TestSanitizeDashboardResponseWithServersRedactsConfiguredValues(t *testing.T) {
	const opaqueSecret = "opaque-value-not-token-shaped"
	response := DashboardResponse{Servers: []DashboardServer{{
		Name:    "secure",
		Command: "server --value " + opaqueSecret,
		Error:   "connection rejected " + opaqueSecret,
	}}}
	SanitizeDashboardResponseWithServers(&response, map[string]config.MCPServerConfig{
		"secure": {Env: map[string]string{"SERVICE_TOKEN": opaqueSecret}},
	})
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), opaqueSecret) {
		t.Fatalf("configured value leaked after proxy sanitization: %s", data)
	}
}

func TestBuildDashboardResponseKeepsNonSensitiveConfigValues(t *testing.T) {
	const apiKey = "opaque-api-key-value"
	cfg := config.MCPServerConfig{
		Enabled: true,
		Type:    "stdio",
		Command: "npx",
		Env: map[string]string{
			"LOG_LEVEL":      "debug",
			"NODE_ENV":       "production",
			"OPENAI_API_KEY": apiKey,
		},
		Headers: map[string]string{
			"Accept":        "application/json",
			"Authorization": "opaque-header-secret",
		},
	}
	response := BuildDashboardResponse(true, false, true, map[string]ServerStatus{
		"debug-production-server": {
			Name:   "debug-production-server",
			Config: cfg,
			State:  ConnectionConnected,
			Tools: []*sdkmcp.Tool{{
				Name:        "debug_tool",
				Description: "debug production application/json information",
			}},
		},
	})
	server := response.Servers[0]
	if server.Name != "debug-production-server" {
		t.Fatalf("server name = %q, want %q", server.Name, "debug-production-server")
	}
	if server.Tools[0].Name != "debug_tool" {
		t.Fatalf("tool name = %q, want %q", server.Tools[0].Name, "debug_tool")
	}
	if server.Tools[0].Description != "debug production application/json information" {
		t.Fatalf("tool description = %q", server.Tools[0].Description)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{apiKey, "opaque-header-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("dashboard response leaked %q: %s", secret, data)
		}
	}
}

func TestSanitizeDashboardResponseWithServersKeepsServerNames(t *testing.T) {
	response := DashboardResponse{Servers: []DashboardServer{{
		Name: "debug-server",
	}}}
	SanitizeDashboardResponseWithServers(&response, map[string]config.MCPServerConfig{
		"debug-server": {Env: map[string]string{"LOG_LEVEL": "debug"}},
	})
	if response.Servers[0].Name != "debug-server" {
		t.Fatalf("server name = %q, want %q", response.Servers[0].Name, "debug-server")
	}
}

func TestRestoreServerConfigRedactionsMatchesReorderedArgs(t *testing.T) {
	existing := config.MCPServerConfig{Args: []string{
		"--token",
		"standalone-secret",
		"--safe",
		"value",
		"--url=https://example.test/mcp?token=query-secret",
	}}
	redacted := RedactServerConfig(existing)
	incoming := existing
	incoming.Args = []string{
		redacted.Args[4],
		"--new-flag",
		"new-value",
		redacted.Args[0],
		redacted.Args[1],
		redacted.Args[2],
		redacted.Args[3],
	}
	restored := RestoreServerConfigRedactions(incoming, existing)
	want := []string{
		existing.Args[4],
		"--new-flag",
		"new-value",
		existing.Args[0],
		existing.Args[1],
		existing.Args[2],
		existing.Args[3],
	}
	if strings.Join(restored.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("restored args = %#v, want %#v", restored.Args, want)
	}
}

func TestRestoreServerConfigRedactionsKeepsStandaloneValuesWithTheirFlag(t *testing.T) {
	existing := config.MCPServerConfig{Args: []string{
		"--api-key", "secret-a",
		"--db-password", "secret-b",
	}}
	redacted := RedactServerConfig(existing)
	incoming := existing
	incoming.Args = []string{
		redacted.Args[2], redacted.Args[3],
		redacted.Args[0], redacted.Args[1],
	}
	restored := RestoreServerConfigRedactions(incoming, existing)
	want := []string{
		"--db-password", "secret-b",
		"--api-key", "secret-a",
	}
	if strings.Join(restored.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("restored args = %#v, want %#v", restored.Args, want)
	}
}

func TestRedactServerConfigMasksSensitiveFlagForms(t *testing.T) {
	cfg := config.MCPServerConfig{
		Args: []string{
			"--token=inline-secret",
			"--api-key",
			"separate-secret",
			"POSTGRES_PASSWORD=db-secret",
			"--database-url",
			"postgresql://db-user:db-secret@db.internal/app",
			"--dsn=mysql://db-user:db-secret@db.internal/app",
			"--safe",
			"visible",
		},
		Env: map[string]string{
			"DATABASE_URL": "postgresql://db-user:db-secret@db.internal/app",
		},
	}
	redacted := RedactServerConfig(cfg)
	got := redacted.Args
	want := []string{
		"--token=" + RedactedValue,
		"--api-key",
		RedactedValue,
		"POSTGRES_PASSWORD=" + RedactedValue,
		"--database-url",
		RedactedValue,
		"--dsn=" + RedactedValue,
		"--safe",
		"visible",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	if redacted.Env["DATABASE_URL"] != RedactedValue {
		t.Fatalf("database URL was not fully redacted: %#v", redacted.Env)
	}
}
