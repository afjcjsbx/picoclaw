package mcp

import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

// ConnectionState is the observable lifecycle state of a configured MCP server.
type ConnectionState string

const (
	ConnectionDisabled     ConnectionState = "disabled"
	ConnectionDisconnected ConnectionState = "disconnected"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionError        ConnectionState = "error"
)

// ServerStatus is a point-in-time runtime snapshot. Config and tool schemas are
// intentionally kept internal to PicoClaw; API layers must convert this value to
// DashboardServer before returning it to a browser.
type ServerStatus struct {
	Name   string
	Config config.MCPServerConfig
	State  ConnectionState
	Error  string
	Tools  []*sdkmcp.Tool
}

func cloneServerStatus(status ServerStatus) ServerStatus {
	status.Tools = append([]*sdkmcp.Tool(nil), status.Tools...)
	status.Config.Args = append([]string(nil), status.Config.Args...)
	status.Config.Env = cloneStringMap(status.Config.Env)
	status.Config.Headers = cloneStringMap(status.Config.Headers)
	return status
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func (m *Manager) setServerStatus(name string, cfg config.MCPServerConfig, state ConnectionState, err error, tools []*sdkmcp.Tool) {
	if m == nil {
		return
	}
	status := ServerStatus{
		Name:   name,
		Config: cfg,
		State:  state,
		Tools:  append([]*sdkmcp.Tool(nil), tools...),
	}
	if err != nil {
		status.Error = err.Error()
	}
	m.mu.Lock()
	m.statuses[name] = status
	m.mu.Unlock()
}

func (m *Manager) setServerStatusLocked(name string, cfg config.MCPServerConfig, state ConnectionState, err error, tools []*sdkmcp.Tool) {
	status := ServerStatus{
		Name:   name,
		Config: cfg,
		State:  state,
		Tools:  append([]*sdkmcp.Tool(nil), tools...),
	}
	if err != nil {
		status.Error = err.Error()
	}
	m.statuses[name] = status
}

// GetServerStatuses returns a defensive copy of all known configured server states.
func (m *Manager) GetServerStatuses() map[string]ServerStatus {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]ServerStatus, len(m.statuses))
	for name, status := range m.statuses {
		result[name] = cloneServerStatus(status)
	}
	return result
}

func initialServerState(globalEnabled bool, cfg config.MCPServerConfig) ConnectionState {
	if !globalEnabled || !cfg.Enabled {
		return ConnectionDisabled
	}
	return ConnectionDisconnected
}
