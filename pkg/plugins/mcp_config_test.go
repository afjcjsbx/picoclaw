package plugins

import (
	"encoding/json"
	"testing"
)

func TestMCPServerBoundaries(t *testing.T) {
	for _, invalid := range []any{
		nil, map[string]any{"type": "http", "url": "https://example.org"},
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
		data, _ := json.Marshal(map[string]any{"$schema": MCPSchema, "mcpServers": map[string]any{"bad": invalid, "good": map[string]any{"type": "stdio", "command": "echo"}}})
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
