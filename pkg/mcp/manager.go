package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// ServerConfig defines how to launch an MCP server
type ServerConfig struct {
	Name    string
	Type    string
	URL     string
	Headers map[string]string
	Cmd     string
	Args    []string
	Env     []string
}

type Manager struct {
	registry *tools.ToolRegistry
	clients  map[string]Client
	mu       sync.RWMutex
}

func NewManager(registry *tools.ToolRegistry) *Manager {
	return &Manager{
		registry: registry,
		clients:  make(map[string]Client),
	}
}

// StartAndRegister starts an MCP server, completes the handshake, and registers its tools in the ToolRegistry
func (m *Manager) StartAndRegister(ctx context.Context, cfg ServerConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// check if it exists before trying to create the client
	if _, exists := m.clients[cfg.Name]; exists {
		return fmt.Errorf("mcp server %s is already running", cfg.Name)
	}

	var client Client
	var err error

	if cfg.Type == "sse" {
		logger.InfoCF("mcp_manager", "Connecting to remote MCP server via SSE", map[string]any{"url": cfg.URL})
		client, err = NewSSEClient(ctx, cfg.URL, cfg.Headers)
	} else if cfg.Type == "http" {
		logger.InfoCF(
			"mcp_manager",
			"Connecting to remote MCP server via Stateless HTTP",
			map[string]any{"url": cfg.URL},
		)
		client = NewHTTPClient(cfg.URL, cfg.Headers)
	} else {
		logger.InfoCF("mcp_manager", "Starting local MCP server", map[string]any{"cmd": cfg.Cmd})
		client, err = NewStdioClient(ctx, cfg.Cmd, cfg.Args, cfg.Env)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", cfg.Name, err)
	}

	// MCP Initialization Handshake
	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return fmt.Errorf("mcp initialization failed for %s: %w", cfg.Name, err)
	}

	// Retrieve exposed tools
	toolsList, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("failed to list tools from %s: %w", cfg.Name, err)
	}

	// Registers tools dynamically in the ToolRegistry
	for _, tDef := range toolsList.Tools {
		originalName := tDef.Name

		// We prefix the name for the LLM to avoid collisions
		tDef.Name = fmt.Sprintf("%s_%s", cfg.Name, originalName)

		adapter := NewMCPToolAdapter(client, tDef, originalName)
		m.registry.Register(adapter)

		logger.InfoCF("mcp_manager", "Registered MCP tool", map[string]any{
			"server":   cfg.Name,
			"llm_name": tDef.Name,
			"mcp_name": originalName,
		})
	}

	m.clients[cfg.Name] = client
	return nil
}

// Shutdown ensures a clean shutdown of all MCP server processes
func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, client := range m.clients {
		logger.InfoCF("mcp_manager", "Shutting down MCP server", map[string]any{"server": name})
		client.Close()
		delete(m.clients, name)
	}
}

// InitFromConfig reads the server map from the global configuration and starts them all
func (m *Manager) InitFromConfig(ctx context.Context, cfg config.MCPConfig) {
	for name, srvCfg := range cfg.Servers {
		logger.InfoCF("mcp_manager", "Starting MCP server from config", map[string]any{"server": name})

		err := m.StartAndRegister(ctx, ServerConfig{
			Name:    name,
			Type:    srvCfg.Type,
			URL:     srvCfg.URL,
			Headers: srvCfg.Headers,
			Cmd:     srvCfg.Command,
			Args:    srvCfg.Args,
			Env:     BuildEnv(srvCfg.Env),
		})
		if err != nil {
			logger.ErrorCF("mcp_manager", "Failed to start MCP server", map[string]any{
				"server": name,
				"error":  err,
			})
			continue
		}

		logger.InfoCF("mcp_manager", "Successfully initialized MCP server", map[string]any{"server": name})
	}
}
