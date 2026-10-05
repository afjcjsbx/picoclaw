package plugins

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/mcp"
	"github.com/sipeed/picoclaw/pkg/tools"
	integrationtools "github.com/sipeed/picoclaw/pkg/tools/integration"
)

// PluginTool preserves PicoClaw's MCP result/media conversion and adds stable
// identity, cancellation and a per-call deadline. No automatic retries occur.
type PluginTool struct {
	*integrationtools.MCPTool
	ID         string
	Server     string
	Deferred   *bool
	definition *sdk.Tool
	manager    *mcp.Manager
	lifetime   context.Context
	timeout    time.Duration
	name       string
}

func capabilityName(id, server, tool string) string {
	raw := id + "\x00" + server + "\x00" + tool
	base := "mcp_plugin_" + id + "_" + server + "_" + tool
	base = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, base)
	if len(base) > 47 {
		base = base[:47]
	}
	hash := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%s_%x", base, hash[:8])
}

func newPluginTool(
	id, server string,
	definition *sdk.Tool,
	manager *mcp.Manager,
	lifetime context.Context,
	timeout time.Duration,
) *PluginTool {
	return &PluginTool{
		MCPTool:    integrationtools.NewMCPTool(manager, server, definition),
		ID:         id,
		Server:     server,
		definition: definition,
		manager:    manager,
		lifetime:   lifetime,
		timeout:    timeout,
		name:       capabilityName(id, server, definition.Name),
	}
}

func (t *PluginTool) Name() string { return t.name }

func (t *PluginTool) RemoteName() string { return t.definition.Name }

// RedactCredentials removes this server's configured HTTP credentials from
// content before automatic plugin hooks send it back to the server.
func (t *PluginTool) RedactCredentials(content string) string {
	conn, ok := t.manager.GetServer(t.Server)
	if !ok {
		return content
	}
	for name, value := range conn.Config.Headers {
		lower := strings.ToLower(name)
		if !strings.Contains(lower, "auth") && !strings.Contains(lower, "key") && !strings.Contains(lower, "token") &&
			!strings.Contains(lower, "secret") {
			continue
		}
		if len(value) > 8 {
			content = strings.ReplaceAll(content, value, "[REDACTED]")
		}
		if strings.HasPrefix(strings.ToLower(value), "bearer ") && len(value) > len("Bearer ")+8 {
			content = strings.ReplaceAll(content, value[len("Bearer "):], "[REDACTED]")
		}
	}
	return content
}

func (t *PluginTool) Description() string { return "[Plugin:" + t.ID + "] " + t.MCPTool.Description() }

func (t *PluginTool) ForAgent(workspace string, maxInline int, events runtimeevents.Bus) *PluginTool {
	clone := newPluginTool(t.ID, t.Server, t.definition, t.manager, t.lifetime, t.timeout)
	clone.Deferred = t.Deferred
	clone.SetWorkspace(workspace)
	clone.SetMaxInlineTextRunes(maxInline)
	clone.SetEventPublisher(events)
	return clone
}

func (t *PluginTool) Execute(ctx context.Context, args map[string]any) (result *tools.ToolResult) {
	defer func() {
		if p := recover(); p != nil {
			result = tools.ErrorResult(fmt.Sprintf("plugin tool panic: %v", p))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	stop := context.AfterFunc(t.lifetime, cancel)
	defer stop()
	if t.lifetime.Err() != nil {
		return tools.ErrorResult("plugin is closed")
	}
	return t.MCPTool.Execute(ctx, args)
}

// ResourceTool lets agents read bundled skill references without granting broad
// filesystem permissions to plugin roots outside the workspace.
type ResourceTool struct {
	ID, Root string
	Lifetime context.Context
}

func (t *ResourceTool) Name() string {
	// A distinct prefix keeps host resource access separate from every MCP
	// server/tool tuple, including a server literally named "package".
	return strings.TrimPrefix(capabilityName(t.ID, "package", "read_resource"), "mcp_")
}

func (t *ResourceTool) Description() string {
	return "Read a UTF-8 resource bundled in plugin " + t.ID + "; provide a path relative to its root (for example skills/greet/references/help.md)."
}

func (t *ResourceTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
		"required":   []string{"path"},
	}
}

func (t *ResourceTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t.Lifetime != nil && t.Lifetime.Err() != nil {
		return tools.ErrorResult("plugin is closed")
	}
	if err := ctx.Err(); err != nil {
		return tools.ErrorResult(err.Error())
	}
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return tools.ErrorResult("path is required")
	}
	content, err := ReadPackageFile(t.Root, path)
	if err != nil {
		return tools.ErrorResult(err.Error())
	}
	if !utf8.Valid(content) {
		return tools.ErrorResult("resource is not UTF-8 text")
	}
	return tools.NewToolResult(string(content))
}
