package mcp

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal/cliui"
	"github.com/sipeed/picoclaw/pkg/config"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

type toolDetail struct {
	Name        string
	Description string
	Parameters  []paramDetail
}

type paramDetail struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

var serverShowProbe = defaultServerShowProbe

func defaultServerShowProbe(
	ctx context.Context,
	name string,
	server config.MCPServerConfig,
	workspacePath string,
) ([]toolDetail, error) {
	return probeServer(ctx, name, server, workspacePath, func(conn *picomcp.ServerConnection) []toolDetail {
		details := make([]toolDetail, 0, len(conn.Tools))
		for _, tool := range conn.Tools {
			details = append(details, toolDetail{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  extractParameters(tool.InputSchema),
			})
		}
		return details
	})
}

func extractParameters(schema any) []paramDetail {
	properties := toolshared.SchemaProperties(schema)
	if len(properties) == 0 {
		return nil
	}
	params := make([]paramDetail, 0, len(properties))
	for _, property := range properties {
		params = append(params, paramDetail{
			Name:        property.Name,
			Type:        property.Type,
			Description: property.Description,
			Required:    property.Required,
		})
	}
	return params
}

func newShowCommand() *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show details and tools for a configured MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			name := args[0]
			server, exists := cfg.Tools.MCP.Servers[name]
			if !exists {
				return fmt.Errorf("MCP server %q not found", name)
			}

			serverInfo := buildServerInfo(name, server, cfg.Tools.MCP.Discovery.Enabled)

			if !server.Enabled {
				cliui.PrintMCPShow(cmd.OutOrStdout(), serverInfo, nil, true)
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			details, err := serverShowProbe(ctx, name, server, cfg.WorkspacePath())
			if err != nil {
				return fmt.Errorf("failed to connect to MCP server %q: %w", name, err)
			}

			tools := make([]cliui.MCPShowTool, 0, len(details))
			for _, d := range details {
				params := make([]cliui.MCPShowParam, 0, len(d.Parameters))
				for _, p := range d.Parameters {
					params = append(params, cliui.MCPShowParam{
						Name:        p.Name,
						Type:        p.Type,
						Description: p.Description,
						Required:    p.Required,
					})
				}
				tools = append(tools, cliui.MCPShowTool{
					Name:        d.Name,
					Description: d.Description,
					Parameters:  params,
				})
			}

			cliui.PrintMCPShow(cmd.OutOrStdout(), serverInfo, tools, false)
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Second, "Connection timeout")

	return cmd
}

func buildServerInfo(name string, server config.MCPServerConfig, discoveryEnabled bool) cliui.MCPShowServer {
	info := cliui.MCPShowServer{
		Name:              name,
		Type:              inferTransportType(server),
		Target:            renderServerTarget(server),
		Enabled:           server.Enabled,
		EffectiveDeferred: server.EffectiveDeferred(discoveryEnabled),
		DeferredExplicit:  server.Deferred != nil,
		EnvFile:           server.EnvFile,
	}
	if len(server.Env) > 0 {
		keys := make([]string, 0, len(server.Env))
		for k := range server.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		info.EnvKeys = keys
	}
	if len(server.Headers) > 0 {
		keys := make([]string, 0, len(server.Headers))
		for k := range server.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		info.Headers = keys
	}
	return info
}
