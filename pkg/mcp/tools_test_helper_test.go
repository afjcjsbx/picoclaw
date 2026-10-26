package mcp

import "github.com/modelcontextprotocol/go-sdk/mcp"

// allTools groups the tools of every connected server, skipping servers that
// expose none. Tests use it to inspect the live registry; production code
// reads one server at a time, so it lives with the tests.
func allTools(m *Manager) map[string][]*mcp.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]*mcp.Tool)
	for name, conn := range m.servers {
		if len(conn.Tools) > 0 {
			result[name] = conn.Tools
		}
	}
	return result
}
