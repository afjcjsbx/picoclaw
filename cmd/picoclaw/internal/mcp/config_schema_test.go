package mcp

import (
	"encoding/json"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestMCPConfigSchemaMatchesServerStruct pins the embedded MCP config schema to
// config.MCPServerConfig: every field of a fully populated server must pass
// document validation, and unknown fields must still be rejected. Without this
// test the schema silently drifts from the Go struct used for serialization.
func TestMCPConfigSchemaMatchesServerStruct(t *testing.T) {
	deferred := true
	cfg := &config.Config{}
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.MaxInlineTextChars = 4096
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"full": {
			Enabled:    true,
			Deferred:   &deferred,
			Command:    "npx",
			Args:       []string{"-y", "@example/server"},
			Env:        map[string]string{"TOKEN": "value"},
			EnvFile:    "server.env",
			InheritEnv: true,
			Type:       "http",
			URL:        "https://example.org/mcp",
			Headers:    map[string]string{"X-Trace": "1"},
			OAuth: &config.MCPOAuthConfig{
				ClientID:     "client",
				Issuer:       "https://example.org",
				Scopes:       []string{"read"},
				CallbackPort: 8123,
			},
		},
	}

	data, err := json.Marshal(normalizedConfigForSave(cfg))
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := validateConfigDocument(data); err != nil {
		t.Fatalf("fully populated MCPServerConfig must satisfy the schema: %v", err)
	}

	const unknownField = `{"tools":{"mcp":{"enabled":true,"servers":{"x":{"enabled":true,"command":"npx","bogus":1}}}}}`
	if err := validateConfigDocument([]byte(unknownField)); err == nil {
		t.Fatal("schema must reject unknown server fields; the positive test above proves nothing otherwise")
	}
}
