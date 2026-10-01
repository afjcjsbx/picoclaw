package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
)

const gatewayMCPStatusPath = "/internal/mcp/status"

var gatewayMCPDo = (&http.Client{Timeout: 3 * time.Second}).Do

func (h *Handler) registerMCPRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents/mcp", h.handleGetMCPStatus)
}

func (h *Handler) handleGetMCPStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	response := staticMCPDashboardResponse(cfg)
	if upstream, ok := h.fetchGatewayMCPStatus(r, cfg); ok {
		response = reconcileMCPDashboardResponse(upstream, cfg)
	}
	picomcp.SanitizeDashboardResponseWithServers(&response, cfg.Tools.MCP.Servers)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// reconcileMCPDashboardResponse overlays the current persisted configuration
// onto a potentially stale gateway snapshot. The gateway only adopts MCP
// changes after a reload, so using its server list directly would make a
// removed or disabled server reappear in the WebUI until then.
func reconcileMCPDashboardResponse(upstream picomcp.DashboardResponse, cfg *config.Config) picomcp.DashboardResponse {
	configured := staticMCPDashboardResponse(cfg)
	configured.RuntimeAvailable = upstream.RuntimeAvailable

	liveServers := make(map[string]picomcp.DashboardServer, len(upstream.Servers))
	for _, server := range upstream.Servers {
		liveServers[server.Name] = server
	}

	for i := range configured.Servers {
		server := &configured.Servers[i]
		live, ok := liveServers[server.Name]
		if !ok || !server.Enabled {
			continue
		}
		server.Status = live.Status
		server.Error = live.Error
		server.ToolCount = live.ToolCount
		server.Tools = live.Tools
	}

	return configured
}

func (h *Handler) fetchGatewayMCPStatus(ctxRequest *http.Request, cfg *config.Config) (picomcp.DashboardResponse, bool) {
	if !h.gatewayAvailableForProxy() {
		return picomcp.DashboardResponse{}, false
	}
	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil || pidData.Token == "" {
		return picomcp.DashboardResponse{}, false
	}
	target := h.gatewayProxyURL()
	target.Path = gatewayMCPStatusPath
	req, err := http.NewRequestWithContext(ctxRequest.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return picomcp.DashboardResponse{}, false
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	resp, err := gatewayMCPDo(req)
	if err != nil {
		return picomcp.DashboardResponse{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		return picomcp.DashboardResponse{}, false
	}
	var result picomcp.DashboardResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result); err != nil {
		return picomcp.DashboardResponse{}, false
	}
	if cfg != nil {
		picomcp.SanitizeDashboardResponseWithServers(&result, cfg.Tools.MCP.Servers)
	} else {
		picomcp.SanitizeDashboardResponse(&result)
	}
	return result, true
}

func staticMCPDashboardResponse(cfg *config.Config) picomcp.DashboardResponse {
	if cfg == nil {
		return picomcp.DashboardResponse{Servers: []picomcp.DashboardServer{}}
	}
	statuses := make(map[string]picomcp.ServerStatus, len(cfg.Tools.MCP.Servers))
	for name, serverCfg := range cfg.Tools.MCP.Servers {
		state := picomcp.ConnectionDisconnected
		if !cfg.Tools.MCP.Enabled || !serverCfg.Enabled {
			state = picomcp.ConnectionDisabled
		}
		statuses[name] = picomcp.ServerStatus{
			Name:   name,
			Config: serverCfg,
			State:  state,
		}
	}
	return picomcp.BuildDashboardResponse(
		cfg.Tools.MCP.Enabled,
		cfg.Tools.MCP.Discovery.Enabled,
		false,
		statuses,
	)
}

func redactMCPConfigForWeb(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	clone.Tools = cfg.Tools
	clone.Tools.MCP = cfg.Tools.MCP
	clone.Tools.MCP.Servers = make(map[string]config.MCPServerConfig, len(cfg.Tools.MCP.Servers))
	for name, serverCfg := range cfg.Tools.MCP.Servers {
		clone.Tools.MCP.Servers[name] = picomcp.RedactServerConfig(serverCfg)
	}
	return &clone
}

func preserveMCPConfigRedactions(incoming, existing *config.Config) {
	if incoming == nil || existing == nil || len(incoming.Tools.MCP.Servers) == 0 {
		return
	}
	for name, serverCfg := range incoming.Tools.MCP.Servers {
		stored, ok := existing.Tools.MCP.Servers[name]
		if !ok {
			stored, ok = matchingRedactedMCPServer(serverCfg, existing.Tools.MCP.Servers)
		}
		if ok {
			incoming.Tools.MCP.Servers[name] = picomcp.RestoreServerConfigRedactions(serverCfg, stored)
		}
	}
}

func matchingRedactedMCPServer(incoming config.MCPServerConfig, servers map[string]config.MCPServerConfig) (config.MCPServerConfig, bool) {
	var match config.MCPServerConfig
	found := false
	for _, candidate := range servers {
		if !reflect.DeepEqual(incoming, picomcp.RedactServerConfig(candidate)) {
			continue
		}
		if found {
			return config.MCPServerConfig{}, false
		}
		match = candidate
		found = true
	}
	return match, found
}
