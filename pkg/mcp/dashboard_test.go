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
	if restored.Command != original.Command || restored.URL != original.URL || restored.Headers["Authorization"] != original.Headers["Authorization"] || restored.Headers["X-Custom"] != original.Headers["X-Custom"] {
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
