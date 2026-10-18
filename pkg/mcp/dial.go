package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// dialedServer is a completed MCP handshake: the live session plus the tools
// the server advertised during initialization.
type dialedServer struct {
	Session *mcp.ClientSession
	Tools   []*mcp.Tool
}

// dialServer performs the handshake shared by native and plugin servers:
// create the client, connect the transport and list the server tools.
func dialServer(
	ctx context.Context,
	clientName string,
	transport mcp.Transport,
	serverName string,
) (dialedServer, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return dialedServer{}, err
	}
	tools := listServerTools(ctx, serverName, session, session.InitializeResult())
	return dialedServer{Session: session, Tools: tools}, nil
}

// buildHTTPTransport builds the SSE/HTTP transport for one server.
// transportType must already be normalized to "http" or "sse". legacySSE keeps
// the historical plugin meaning of "sse": the legacy SSE client transport when
// no OAuth handler participates; native configs use the streamable transport
// with standalone SSE in both cases.
func buildHTTPTransport(
	name string,
	cfg config.MCPServerConfig,
	transportType string,
	handler auth.OAuthHandler,
	legacySSE bool,
) (mcp.Transport, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("URL is required for SSE/HTTP transport")
	}
	origin, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP URL: %w", err)
	}

	// http: request-response mode, since the optional GET listener is not
	// supported by every hosted server.
	// sse: keep the standalone GET stream for server-initiated notifications.
	disableStandaloneSSE := transportType != "sse"
	logger.DebugCF("mcp", "Using SSE/HTTP transport",
		map[string]any{
			"server":               name,
			"url":                  cfg.URL,
			"disableStandaloneSSE": disableStandaloneSSE,
		})
	if len(cfg.Headers) > 0 {
		logger.DebugCF("mcp", "Added custom HTTP headers",
			map[string]any{
				"server":       name,
				"header_count": len(cfg.Headers),
			})
	}

	// Every request is bound to the configured origin and redirects may not
	// leave it, so configured headers (for example Authorization) are never
	// replayed to another host.
	var base http.RoundTripper = http.DefaultTransport
	checkRedirect := sameOriginRedirectPolicy(origin)
	if handler != nil {
		base = oauthHTTPTransport{}
		checkRedirect = rejectOAuthRedirect
	}
	client := &http.Client{
		Transport:     &originBoundTransport{base: base, origin: origin, headers: cfg.Headers},
		CheckRedirect: checkRedirect,
	}

	if legacySSE && transportType == "sse" && handler == nil {
		return &mcp.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: client}, nil
	}
	return &mcp.StreamableClientTransport{
		Endpoint:             cfg.URL,
		HTTPClient:           client,
		OAuthHandler:         handler,
		DisableStandaloneSSE: disableStandaloneSSE,
	}, nil
}

// buildNativeStdioTransport builds the stdio transport for a native MCP
// server, applying the configured environment policy: a filtered inherited
// environment, then the env file, then the explicit env entries.
func buildNativeStdioTransport(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
) (mcp.Transport, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("command is required for stdio transport")
	}
	logger.DebugCF("mcp", "Using stdio transport",
		map[string]any{
			"server":  name,
			"command": cfg.Command,
		})

	cmd := exec.CommandContext(ctx, fileutil.ExpandHome(cfg.Command), cfg.Args...)

	// Use a map to ensure config variables override file variables.
	// Inherit only basic process variables unless this server explicitly opts in.
	envMap := inheritedMCPEnv(cmd.Environ(), cfg.InheritEnv)

	// Load environment variables from file if specified
	if cfg.EnvFile != "" {
		envVars, err := loadEnvFile(cfg.EnvFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load env file %s: %w", cfg.EnvFile, err)
		}
		for k, v := range envVars {
			envMap[k] = v
		}
		logger.DebugCF("mcp", "Loaded environment variables from file",
			map[string]any{
				"server":    name,
				"envFile":   cfg.EnvFile,
				"var_count": len(envVars),
			})
	}

	// Environment variables from config override those from file
	for k, v := range cfg.Env {
		envMap[k] = v
	}

	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	return &isolatedCommandTransport{Command: cmd}, nil
}
