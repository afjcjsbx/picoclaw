package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

// PluginRuntimeOptions separates process lifetime from handshake timeout.
// Environment is an exact host-prepared environment, never ambient inheritance.
type PluginRuntimeOptions struct {
	Stderr      io.Writer
	Lifetime    context.Context
	OAuthName   string
	Directory   string
	Environment []string
	Timeout     time.Duration
}

// ConnectPluginServer uses portable transport meanings, without changing the
// historical meaning of "sse" in PicoClaw's native MCP configuration.
func (m *Manager) ConnectPluginServer(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
	opts PluginRuntimeOptions,
) error {
	if opts.Lifetime == nil {
		return fmt.Errorf("plugin lifetime context is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	// The session outlives the handshake: SSE binds its long-lived GET request
	// to Connect's context, so it is derived from opts.Lifetime, not from the
	// timeout. The timer cancels only a stuck initialization and is stopped on
	// success; ctx (caller gave up) always cancels. Any failure path releases
	// the session context through the deferred cancel.
	sessionCtx, cancel := context.WithCancel(opts.Lifetime)
	timer := time.AfterFunc(opts.Timeout, cancel)
	stop := context.AfterFunc(ctx, cancel)
	connected := false
	defer func() {
		timer.Stop()
		stop()
		if !connected {
			cancel()
		}
	}()
	if cfg.OAuth != nil {
		if err := validateOAuthConfig(cfg); err != nil {
			return err
		}
	}
	var transport sdk.Transport
	transportType := config.NormalizeMCPTransportType(cfg.Type)
	switch transportType {
	case "stdio":
		cmd := exec.CommandContext(sessionCtx, cfg.Command, cfg.Args...)
		cmd.Dir = opts.Directory
		cmd.Stderr = opts.Stderr
		// The host supplies the exact child environment: no ambient
		// inheritance and no cfg.Env merge, unlike native stdio servers.
		// Commands are deliberately not home-expanded either: packages must
		// use absolute paths (docs/guides/plugins.md).
		cmd.Env = append([]string{}, opts.Environment...)
		transport = &isolatedCommandTransport{Command: cmd, TerminateDuration: time.Second}
	case "http", "sse":
		var oauthHandler auth.OAuthHandler
		if cfg.OAuth != nil {
			oauthName := opts.OAuthName
			if oauthName == "" {
				oauthName = name
			}
			oauthHandler = newStoredOAuthHandler(oauthName, cfg)
		}
		var err error
		transport, err = buildHTTPTransport(name, cfg, transportType, oauthHandler, pluginHTTPTransport)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported plugin MCP transport %q", cfg.Type)
	}

	dialed, err := dialServer(sessionCtx, "picoclaw-plugins", transport, name)
	if err != nil {
		return err
	}
	session := dialed.Session
	serverTools := dialed.Tools
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		_ = session.Close()
		return fmt.Errorf("manager is closed")
	}
	if _, exists := m.servers[name]; exists {
		_ = session.Close()
		return fmt.Errorf("duplicate MCP server %q", name)
	}
	if !timer.Stop() || sessionCtx.Err() != nil {
		_ = session.Close()
		return fmt.Errorf("plugin initialization canceled or timed out")
	}
	m.servers[name] = &ServerConnection{
		Config:  cfg,
		Session: session,
		Tools:   serverTools,
		plugin:  true,
		cancel:  cancel,
	}
	connected = true
	return nil
}
