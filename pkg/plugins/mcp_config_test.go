package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMCPHeaderEnv(t *testing.T) {
	t.Setenv("PICOCLAW_TEST_MCP_TOKEN", "test-token")
	packageHeaders := map[string]string{
		"Authorization": "Bearer ${PICOCLAW_TEST_MCP_TOKEN}",
		"X-Test":        "prefix-${PICOCLAW_TEST_MCP_TOKEN}",
	}
	cfg := config.MCPServerConfig{Type: "streamable-http", Headers: packageHeaders}
	if err := resolveMCPHeaderEnv(&cfg, false); err != nil {
		t.Fatal(err)
	}
	if cfg.Headers["Authorization"] != "Bearer test-token" || cfg.Headers["X-Test"] != "prefix-test-token" ||
		packageHeaders["Authorization"] != "Bearer ${PICOCLAW_TEST_MCP_TOKEN}" {
		t.Fatal("header references were not resolved without mutating the package")
	}
	for _, value := range []string{"${PICOCLAW_TEST_MCP_MISSING}", "${BAD-NAME}", "${BROKEN"} {
		bad := config.MCPServerConfig{Type: "streamable-http", Headers: map[string]string{"Authorization": value}}
		if err := resolveMCPHeaderEnv(&bad, false); err == nil || strings.Contains(err.Error(), "test-token") {
			t.Fatalf("reference %q was accepted or leaked a value", value)
		}
	}
	bad := config.MCPServerConfig{
		Type:    "streamable-http",
		Headers: map[string]string{"Authorization": "${PICOCLAW_TEST_MCP_TOKEN}"},
	}
	t.Setenv("PICOCLAW_TEST_MCP_TOKEN", "bad\r\nheader: value")
	if err := resolveMCPHeaderEnv(&bad, false); err == nil {
		t.Fatal("accepted a resolved header with a newline")
	}
	if err := resolveMCPHeaderEnv(&bad, true); err != nil || len(bad.Headers) != 0 {
		t.Fatalf("host authorization override failed: headers=%d err=%v", len(bad.Headers), err)
	}
}

func TestMCPBearerTokenFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "api-key"), []byte("secret-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	packageHeaders := map[string]string{"authorization": "package-value", "X-Test": "present"}
	cfg := config.MCPServerConfig{Type: "streamable-http", Headers: packageHeaders}
	if err := applyMCPBearerToken(&cfg, dataDir, "api-key"); err != nil {
		t.Fatal(err)
	}
	if cfg.Headers["Authorization"] != "Bearer secret-token" || cfg.Headers["X-Test"] != "present" ||
		packageHeaders["authorization"] != "package-value" || len(cfg.Headers) != 2 {
		t.Fatalf("headers were not merged correctly: %#v", cfg.Headers)
	}
	for _, filename := range []string{"", "../outside", "missing"} {
		if err := applyMCPBearerToken(&cfg, dataDir, filename); err == nil {
			t.Fatalf("accepted invalid credential path %q", filename)
		}
	}
	if err := os.WriteFile(filepath.Join(dataDir, "bad-key"), []byte("secret\r\nInjected: value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyMCPBearerToken(&cfg, dataDir, "bad-key"); err == nil {
		t.Fatal("accepted header injection")
	}
	stdio := config.MCPServerConfig{Type: "stdio"}
	if err := applyMCPBearerToken(&stdio, dataDir, "api-key"); err == nil {
		t.Fatal("accepted bearer token on stdio server")
	}
}

func TestMCPServerBoundaries(t *testing.T) {
	for _, invalid := range []any{
		nil,
		map[string]any{"type": "http", "url": "https://example.org"},
		map[string]any{"type": "stdio", "command": "/bin/sh"},
		map[string]any{"type": "stdio", "command": "echo", "url": "https://example.org"},
		map[string]any{"type": "stdio", "command": "echo", "env": map[string]any{"PLUGIN_ROOT": "/tmp"}},
		map[string]any{"type": "stdio", "command": "echo", "args": []any{nil}},
		map[string]any{"type": "streamable-http", "url": "http://example.org/mcp"},
		map[string]any{"type": "sse", "url": "https://user:password@example.org"},
		map[string]any{"type": "sse", "url": "https://example.org/#fragment"},
		map[string]any{"type": "sse", "url": "https://example.org", "headers": map[string]string{"X-Test": "a", "x-test": "b"}},
		map[string]any{"type": "sse", "url": "https://example.org", "headers": map[string]string{"X-Test": "a\r\nb"}},
	} {
		data, _ := json.Marshal(
			map[string]any{
				"$schema": MCPSchema,
				"mcpServers": map[string]any{
					"bad":  invalid,
					"good": map[string]any{"type": "stdio", "command": "echo"},
				},
			},
		)
		servers, diagnostics, err := ParseMCP(data)
		if err != nil || len(servers) != 1 || len(diagnostics) != 1 {
			t.Fatalf("%s: servers=%v diagnostics=%v err=%v", data, servers, diagnostics, err)
		}
	}
}

func TestMCPDocumentValidation(t *testing.T) {
	for _, data := range []string{"null", "{}", `{"$schema":"wrong","mcpServers":{}}`, `{"$schema":"` + MCPSchema + `","mcpServers":null}`, `{"$schema":"` + MCPSchema + `","mcpServers":{},"extra":true}`} {
		if _, _, err := ParseMCP([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	for _, endpoint := range []string{"http://localhost/mcp", "http://127.0.0.1/mcp", "http://[::1]/mcp", "https://example.org/mcp"} {
		if err := validateEndpoint(endpoint); err != nil {
			t.Fatalf("%s: %v", endpoint, err)
		}
	}
}

func TestMCPOverrideCanSetDeferredWithoutChangingPortableMCPFormat(t *testing.T) {
	data := []byte(`{"type":"streamable-http","url":"https://example.org/mcp","deferred":false}`)
	spec, err := parseServerOverride(data)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Deferred == nil || *spec.Deferred {
		t.Fatalf("deferred override = %v, want false", spec.Deferred)
	}
	cfg, _, _, err := spec.RuntimeConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Deferred == nil || *cfg.Deferred {
		t.Fatalf("runtime deferred override = %v, want false", cfg.Deferred)
	}
	if _, err := parseServer(data); err == nil {
		t.Fatal("portable MCP package config unexpectedly accepted host-only deferred setting")
	}
}
