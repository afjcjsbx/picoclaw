package plugins

import (
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
)

// ResolveOAuthServer returns the same plugin MCP endpoint used at runtime, for
// the explicit `picoclaw mcp login <plugin>:<server>` command.
func ResolveOAuthServer(cfg config.PluginsConfig, workspace, pluginID, serverName string) (config.MCPServerConfig, error) {
	items, _ := discover(cfg, workspace)
	for _, item := range items {
		if item.id != pluginID {
			continue
		}
		if item.err != nil {
			return config.MCPServerConfig{}, item.err
		}
		oauth, enabled := item.entry.MCPOAuth[serverName]
		if !enabled {
			return config.MCPServerConfig{}, fmt.Errorf("plugin MCP server %q has no OAuth configuration", pluginID+":"+serverName)
		}
		var spec ServerSpec
		if raw, overridden := item.entry.MCPOverrides[serverName]; overridden {
			var err error
			spec, err = parseServerOverride(raw)
			if err != nil {
				return config.MCPServerConfig{}, err
			}
		} else {
			data, err := ReadPackageFile(item.root, "mcp.json")
			if err != nil {
				return config.MCPServerConfig{}, err
			}
			servers, _, err := ParseMCP(data)
			if err != nil {
				return config.MCPServerConfig{}, err
			}
			var ok bool
			spec, ok = servers[serverName]
			if !ok {
				return config.MCPServerConfig{}, fmt.Errorf("plugin MCP server %q not found", pluginID+":"+serverName)
			}
		}
		server, _, _, err := spec.RuntimeConfig(item.root, "")
		if err != nil {
			return config.MCPServerConfig{}, err
		}
		if _, bearerFile := item.entry.MCPBearerTokenFiles[serverName]; bearerFile {
			return config.MCPServerConfig{}, fmt.Errorf("plugin MCP OAuth cannot be combined with a bearer token file")
		}
		if err := resolveMCPHeaderEnv(&server, false); err != nil {
			return config.MCPServerConfig{}, err
		}
		server.OAuth = &oauth
		return server, nil
	}
	return config.MCPServerConfig{}, fmt.Errorf("plugin %q not found or disabled", pluginID)
}
