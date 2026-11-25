package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/mcp"
)

const mcpStatusPath = "/internal/mcp/status"

func mcpStatusHandler(agentLoop *agent.AgentLoop) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if agentLoop == nil {
			http.Error(w, "agent runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		cfg := agentLoop.GetConfig()
		if cfg == nil {
			http.Error(w, "agent runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		response := mcp.BuildDashboardResponse(
			cfg.Tools.MCP.Enabled,
			cfg.Tools.MCP.Discovery.Enabled,
			true,
			agentLoop.MCPServerStatuses(),
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	})
}
