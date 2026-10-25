package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// mcpMaxConcurrentConnect bounds how many MCP servers are started at the same
// time during initialization. Each server is a separate child process (often a
// Node or Python runtime), so connecting every configured server at once can
// spike memory and CPU on constrained devices. All enabled servers are still
// connected; only the startup burst is bounded.
const mcpMaxConcurrentConnect = 4

// loadEnvFile loads environment variables from a file in .env format
// Each line should be in the format: KEY=value
// Lines starting with # are comments
// Empty lines are ignored
func loadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open env file: %w", err)
	}
	defer file.Close()

	envVars := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format at line %d: %s", lineNum, line)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return nil, fmt.Errorf("invalid format at line %d: empty key", lineNum)
		}

		// Remove surrounding quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		envVars[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading env file: %w", err)
	}

	return envVars, nil
}

func allowedMCPParentEnv(key string) bool {
	// Keep the process usable without passing PicoClaw's unrelated credentials.
	switch key {
	case "PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "SHELL", "TMPDIR":
		return true
	}
	if strings.HasPrefix(key, "XDG_") {
		return true
	}
	if runtime.GOOS == "windows" {
		switch strings.ToUpper(key) {
		case "PATH", "HOME", "USER", "LANG", "LC_ALL", "TERM", "SHELL", "TMPDIR",
			"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "TEMP", "TMP",
			"USERPROFILE", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "HOMEDRIVE", "HOMEPATH":
			return true
		}
	}
	return false
}

func inheritedMCPEnv(environ []string, inheritAll bool) map[string]string {
	env := make(map[string]string)
	for _, entry := range environ {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			key := entry[:idx]
			if inheritAll || allowedMCPParentEnv(key) {
				env[key] = entry[idx+1:]
			}
		}
	}
	return env
}

// ServerConnection represents a connection to an MCP server
type ServerConnection struct {
	plugin      bool // Plugin calls are never automatically replayed after disconnect.
	cancel      context.CancelFunc
	Config      config.MCPServerConfig
	Session     *mcp.ClientSession
	Tools       []*mcp.Tool
	reconnectMu sync.Mutex
}

// Manager manages multiple MCP server connections
type Manager struct {
	servers       map[string]*ServerConnection
	statuses      map[string]ServerStatus
	runtimeEvents runtimeevents.Bus
	mu            sync.RWMutex
	closed        atomic.Bool    // set once by Close; read without the mutex to fail closed
	wg            sync.WaitGroup // tracks in-flight CallTool calls
	watchWG       sync.WaitGroup // tracks session disconnect observers
}

var connectServerFunc = connectServer

// ManagerOption configures an MCP manager.
type ManagerOption func(*Manager)

// WithRuntimeEvents injects the runtime event bus used for MCP observations.
func WithRuntimeEvents(eventBus runtimeevents.Bus) ManagerOption {
	return func(m *Manager) {
		m.runtimeEvents = eventBus
	}
}

// serverEventPayload describes MCP server connection events.
type serverEventPayload struct {
	Server    string `json:"server"`
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	Command   string `json:"command,omitempty"`
	Tool      string `json:"tool,omitempty"`
	ToolCount int    `json:"tool_count,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewManager creates a new MCP manager
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		servers:  make(map[string]*ServerConnection),
		statuses: make(map[string]ServerStatus),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// LoadFromMCPConfig loads MCP servers from MCP configuration and workspace path,
// taking the MCP section rather than the full Config.
func (m *Manager) LoadFromMCPConfig(
	ctx context.Context,
	mcpCfg config.MCPConfig,
	workspacePath string,
) error {
	for name, serverCfg := range mcpCfg.Servers {
		m.setServerStatus(name, serverCfg, initialServerState(mcpCfg.Enabled, serverCfg), nil, nil)
	}
	if !mcpCfg.Enabled {
		logger.InfoCF("mcp", "MCP integration is disabled", nil)
		return nil
	}

	if len(mcpCfg.Servers) == 0 {
		logger.InfoCF("mcp", "No MCP servers configured", nil)
		return nil
	}

	logger.InfoCF("mcp", "Initializing MCP servers",
		map[string]any{
			"count": len(mcpCfg.Servers),
		})

	var (
		wg      sync.WaitGroup
		success atomic.Int64
	)
	errs := make(chan error, len(mcpCfg.Servers))
	sem := make(chan struct{}, mcpMaxConcurrentConnect)
	enabledCount := 0

	for name, serverCfg := range mcpCfg.Servers {
		if !serverCfg.Enabled {
			logger.DebugCF("mcp", "Skipping disabled server",
				map[string]any{
					"server": name,
				})
			continue
		}

		enabledCount++
		wg.Add(1)
		go func(name string, serverCfg config.MCPServerConfig, workspace string) {
			defer wg.Done()

			// Bound the number of servers starting concurrently so a large
			// configuration does not spawn all child processes at once.
			sem <- struct{}{}
			defer func() { <-sem }()

			// Resolve relative envFile paths relative to workspace
			if serverCfg.EnvFile != "" && !filepath.IsAbs(serverCfg.EnvFile) {
				if workspace == "" {
					err := fmt.Errorf(
						"workspace path is empty while resolving relative envFile %q for server %s",
						serverCfg.EnvFile,
						name,
					)
					logger.ErrorCF("mcp", "Invalid MCP server configuration",
						map[string]any{
							"server":   name,
							"env_file": serverCfg.EnvFile,
							"error":    err.Error(),
						})
					m.setServerStatus(name, serverCfg, ConnectionError, err, nil)
					errs <- err
					return
				}
				serverCfg.EnvFile = filepath.Join(workspace, serverCfg.EnvFile)
			}

			if err := m.ConnectServer(ctx, name, serverCfg); err != nil {
				logger.ErrorCF("mcp", "Failed to connect to MCP server",
					map[string]any{
						"server": name,
						"error":  err.Error(),
					})
				errs <- fmt.Errorf("failed to connect to server %s: %w", name, err)
				return
			}
			success.Add(1)
		}(name, serverCfg, workspacePath)
	}

	wg.Wait()
	close(errs)

	// Collect errors
	var allErrors []error
	for err := range errs {
		allErrors = append(allErrors, err)
	}

	// Count only the servers connected by this call: pre-existing connections
	// from an earlier load must not mask a fully failed reload.
	connectedCount := int(success.Load())

	// If all enabled servers failed to connect, return aggregated error
	if enabledCount > 0 && connectedCount == 0 {
		logger.ErrorCF("mcp", "All MCP servers failed to connect",
			map[string]any{
				"failed": len(allErrors),
				"total":  enabledCount,
			})
		return errors.Join(allErrors...)
	}

	if len(allErrors) > 0 {
		logger.WarnCF("mcp", "Some MCP servers failed to connect",
			map[string]any{
				"failed":    len(allErrors),
				"connected": connectedCount,
				"total":     enabledCount,
			})
		// Don't fail completely if some servers successfully connected
	}

	logger.InfoCF("mcp", "MCP server initialization complete",
		map[string]any{
			"connected": connectedCount,
			"total":     enabledCount,
		})

	return nil
}

// ConnectServer connects to a single MCP server
func (m *Manager) ConnectServer(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
) error {
	m.setServerStatus(name, cfg, ConnectionConnecting, nil, nil)
	m.publishServerEvent(runtimeevents.KindMCPServerConnecting, name, cfg, 0, nil)
	conn, err := connectServerFunc(ctx, name, cfg)
	if err != nil {
		m.setServerStatus(name, cfg, ConnectionError, err, nil)
		m.publishServerEvent(runtimeevents.KindMCPServerFailed, name, cfg, 0, err)
		return err
	}

	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		if conn.Session != nil {
			_ = conn.Session.Close()
		}
		err := fmt.Errorf("manager is closed")
		m.setServerStatus(name, cfg, ConnectionError, err, nil)
		m.publishServerEvent(runtimeevents.KindMCPServerFailed, name, cfg, 0, err)
		return err
	}
	replaced := m.replaceConnection(name, conn)
	m.setServerStatusLocked(name, cfg, ConnectionConnected, nil, conn.Tools)
	m.watchWG.Add(1)
	m.mu.Unlock()

	// Closing the replaced session can block on the network (a streamable
	// session sends a DELETE on close), so it must never run under m.mu.
	closeReplacedConnection(replaced)

	// Publish observations after releasing the manager lock: the event bus must
	// never run while the connection registry is locked.
	for _, tool := range conn.Tools {
		toolName := ""
		if tool != nil {
			toolName = tool.Name
		}
		m.publishToolDiscovered(name, cfg, toolName)
	}
	m.publishServerEvent(runtimeevents.KindMCPServerConnected, name, cfg, len(conn.Tools), nil)
	go m.watchServerConnection(name, conn)
	return nil
}

// replaceConnection stores conn under name and returns the connection it
// replaced, if any. Callers must hold m.mu and must hand the returned
// connection to closeReplacedConnection after releasing the lock.
func (m *Manager) replaceConnection(name string, conn *ServerConnection) *ServerConnection {
	previous := m.servers[name]
	m.servers[name] = conn
	return previous
}

// closeReplacedConnection releases a connection that was swapped out of the
// registry. Session.Close can block on the network, so callers must not hold
// m.mu. A close error only matters for the connection being replaced; the
// current one is unaffected, so it is not reported.
func closeReplacedConnection(conn *ServerConnection) {
	if conn == nil {
		return
	}
	if conn.cancel != nil {
		conn.cancel()
	}
	if conn.Session != nil {
		_ = conn.Session.Close()
	}
}

func (m *Manager) watchServerConnection(name string, conn *ServerConnection) {
	defer m.watchWG.Done()
	if conn == nil || conn.Session == nil {
		return
	}
	err := conn.Session.Wait()
	m.mu.Lock()
	current, ok := m.servers[name]
	if !ok || current != conn {
		m.mu.Unlock()
		return
	}
	state := ConnectionDisconnected
	if err != nil && !m.closed.Load() {
		state = ConnectionError
	}
	m.setServerStatusLocked(name, conn.Config, state, err, conn.Tools)
	m.mu.Unlock()
	if err != nil && !m.closed.Load() {
		m.publishServerEvent(runtimeevents.KindMCPServerFailed, name, conn.Config, len(conn.Tools), err)
	}
}

func connectServer(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
) (*ServerConnection, error) {
	var handler auth.OAuthHandler
	if cfg.OAuth != nil {
		if err := validateOAuthConfig(cfg); err != nil {
			return nil, err
		}
		handler = newStoredOAuthHandler(name, cfg)
	}
	return connectServerWithOAuth(ctx, name, cfg, handler)
}

func connectServerWithOAuth(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
	handler auth.OAuthHandler,
) (*ServerConnection, error) {
	logger.InfoCF("mcp", "Connecting to MCP server",
		map[string]any{
			"server":     name,
			"command":    cfg.Command,
			"args_count": len(cfg.Args),
		})

	transportType := config.EffectiveMCPTransportType(cfg)
	if transportType == "" {
		return nil, fmt.Errorf("either URL or command must be provided")
	}

	var transport mcp.Transport
	switch transportType {
	case "sse", "http":
		var err error
		transport, err = buildHTTPTransport(name, cfg, transportType, handler, false)
		if err != nil {
			return nil, err
		}
	case "stdio":
		var err error
		transport, err = buildNativeStdioTransport(ctx, name, cfg)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf(
			"unsupported transport type %q (supported: stdio, sse, http)",
			transportType,
		)
	}

	dialed, err := dialServer(ctx, "picoclaw", transport, name)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	// Get server info
	initResult := dialed.Session.InitializeResult()
	logger.InfoCF("mcp", "Connected to MCP server",
		map[string]any{
			"server":        name,
			"serverName":    initResult.ServerInfo.Name,
			"serverVersion": initResult.ServerInfo.Version,
			"protocol":      initResult.ProtocolVersion,
		})

	return &ServerConnection{
		Config:  cfg,
		Session: dialed.Session,
		Tools:   dialed.Tools,
	}, nil
}

// GetServers returns all connected servers
func (m *Manager) GetServers() map[string]*ServerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*ServerConnection, len(m.servers))
	for k, v := range m.servers {
		result[k] = v
	}
	return result
}

// GetServer returns a specific server connection
func (m *Manager) GetServer(name string) (*ServerConnection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, ok := m.servers[name]
	return conn, ok
}

// CallTool calls a tool on a specific server
func (m *Manager) CallTool(
	ctx context.Context,
	serverName, toolName string,
	arguments map[string]any,
) (*mcp.CallToolResult, error) {
	// Check if closed before acquiring lock (fast path)
	if m.closed.Load() {
		return nil, fmt.Errorf("manager is closed")
	}

	m.mu.RLock()
	// Double-check after acquiring lock to prevent TOCTOU race
	if m.closed.Load() {
		m.mu.RUnlock()
		return nil, fmt.Errorf("manager is closed")
	}
	conn, ok := m.servers[serverName]
	if ok {
		m.wg.Add(1) // Add to WaitGroup while holding the lock
	}
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	defer m.wg.Done()

	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	}

	result, err := conn.Session.CallTool(ctx, params)
	if err != nil {
		if !conn.plugin && shouldReconnectCallError(err) {
			logger.WarnCF("mcp", "MCP server session was lost during tool call, reconnecting",
				map[string]any{
					"server": serverName,
					"tool":   toolName,
					"error":  err.Error(),
				})

			reconnectedConn, reconnectErr := m.reconnectServer(ctx, serverName, conn)
			if reconnectErr != nil {
				return nil, fmt.Errorf("failed to recover lost MCP session: %w", reconnectErr)
			}

			result, err = reconnectedConn.Session.CallTool(ctx, params)
			if err == nil {
				return result, nil
			}
		}

		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	return result, nil
}

// listServerTools lists the tools exposed by a session. Listing is
// best-effort: per-iteration errors are logged and skipped.
func listServerTools(
	ctx context.Context,
	name string,
	session *mcp.ClientSession,
	initResult *mcp.InitializeResult,
) []*mcp.Tool {
	var tools []*mcp.Tool
	if initResult.Capabilities.Tools == nil {
		return nil
	}

	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			logger.WarnCF("mcp", "Error listing tool",
				map[string]any{
					"server": name,
					"error":  err.Error(),
				})
			continue
		}
		tools = append(tools, tool)
	}

	logger.InfoCF("mcp", "Listed tools from MCP server",
		map[string]any{
			"server":    name,
			"toolCount": len(tools),
		})

	return tools
}

func shouldReconnectCallError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, mcp.ErrSessionMissing) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, mcp.ErrSessionMissing.Error()) {
		return true
	}
	// Servers answering a stale Mcp-Session-Id with HTTP 404 plus a JSON-RPC error
	// body make the SDK return "<rpc error>: rejected by transport: Not Found"
	// instead of ErrSessionMissing, and keep the dead session bound to the connection.
	return strings.HasSuffix(msg, "rejected by transport: "+strings.ToLower(http.StatusText(http.StatusNotFound)))
}

func (m *Manager) reconnectServer(
	ctx context.Context,
	serverName string,
	staleConn *ServerConnection,
) (*ServerConnection, error) {
	if staleConn == nil {
		return nil, fmt.Errorf("server %s not found", serverName)
	}

	staleConn.reconnectMu.Lock()
	defer staleConn.reconnectMu.Unlock()

	if m.closed.Load() {
		return nil, fmt.Errorf("manager is closed")
	}

	m.mu.RLock()
	currentConn, ok := m.servers[serverName]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	if currentConn != staleConn {
		return currentConn, nil
	}

	m.setServerStatus(serverName, staleConn.Config, ConnectionConnecting, nil, staleConn.Tools)
	m.publishServerEvent(runtimeevents.KindMCPServerConnecting, serverName, staleConn.Config, len(staleConn.Tools), nil)
	freshConn, err := connectServerFunc(ctx, serverName, staleConn.Config)
	if err != nil {
		m.setServerStatus(serverName, staleConn.Config, ConnectionError, err, staleConn.Tools)
		m.publishServerEvent(runtimeevents.KindMCPServerFailed, serverName, staleConn.Config, len(staleConn.Tools), err)
		return nil, err
	}

	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		_ = freshConn.Session.Close()
		return nil, fmt.Errorf("manager is closed")
	}

	currentConn, ok = m.servers[serverName]
	if !ok {
		m.mu.Unlock()
		_ = freshConn.Session.Close()
		return nil, fmt.Errorf("server %s not found", serverName)
	}

	if currentConn == staleConn {
		m.servers[serverName] = freshConn
		m.setServerStatusLocked(serverName, freshConn.Config, ConnectionConnected, nil, freshConn.Tools)
		m.watchWG.Add(1)
		staleToClose := staleConn
		m.mu.Unlock()
		m.publishServerEvent(
			runtimeevents.KindMCPServerConnected,
			serverName,
			freshConn.Config,
			len(freshConn.Tools),
			nil,
		)
		go m.watchServerConnection(serverName, freshConn)
		_ = staleToClose.Session.Close()
		return freshConn, nil
	}

	m.mu.Unlock()
	_ = freshConn.Session.Close()
	return currentConn, nil
}

// Close closes all server connections
func (m *Manager) Close() error {
	// Set closed=true while holding the same lock CallTool holds across its
	// closed check and wg.Add. This guarantees that once Swap returns, no
	// CallTool can still be between the check and the Add, so wg.Wait below
	// actually waits for every in-flight call. Setting closed without the lock
	// would leave a TOCTOU window where a call passes the check and Adds after
	// Wait already observed a zero counter.
	m.mu.Lock()
	if m.closed.Swap(true) {
		m.mu.Unlock()
		return nil // already closed
	}
	m.mu.Unlock()

	// Wait for all in-flight CallTool calls to finish before closing sessions.
	m.wg.Wait()

	m.mu.Lock()

	logger.InfoCF("mcp", "Closing all MCP server connections",
		map[string]any{
			"count": len(m.servers),
		})

	var errs []error
	for name, conn := range m.servers {
		if conn == nil {
			continue
		}
		if conn.cancel != nil {
			conn.cancel()
		}
		if conn.Session == nil {
			continue
		}
		if err := conn.Session.Close(); err != nil {
			// Plugin shutdown cancels process lifetime before joining sessions.
			// A terminated child is expected here, not a new component failure.
			var exitErr *exec.ExitError
			if conn.plugin &&
				(errors.As(err, &exitErr) || errors.Is(err, os.ErrClosed) || errors.Is(err, context.Canceled)) {
				continue
			}
			logger.ErrorCF("mcp", "Failed to close server connection",
				map[string]any{
					"server": name,
					"error":  err.Error(),
				})
			errs = append(errs, fmt.Errorf("server %s: %w", name, err))
		}
	}

	m.servers = make(map[string]*ServerConnection)
	for name, status := range m.statuses {
		if status.State != ConnectionDisabled {
			status.State = ConnectionDisconnected
			status.Error = ""
			m.statuses[name] = status
		}
	}
	m.mu.Unlock()
	m.watchWG.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("failed to close %d server(s): %w", len(errs), errors.Join(errs...))
	}

	return nil
}

// getAllTools returns all tools from all connected servers.
func (m *Manager) getAllTools() map[string][]*mcp.Tool {
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
