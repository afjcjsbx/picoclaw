package mcp

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type MCPToolAdapter struct {
	client       Client
	originalName string
	definition   MCPToolDefinition
}

var _ tools.DeferredTool = (*MCPToolAdapter)(nil)

// IsDeferred tells PicoClaw to never load this tool in the initial context,
// but to make it available only through the tool_search_tool.
func (a *MCPToolAdapter) IsDeferred() bool {
	return true
}

func NewMCPToolAdapter(client Client, def MCPToolDefinition, originalName string) *MCPToolAdapter {
	return &MCPToolAdapter{
		client:       client,
		originalName: originalName,
		definition:   def,
	}
}

// Name returns the name of the MCP tool
func (a *MCPToolAdapter) Name() string {
	return a.definition.Name
}

// Description MCP tool description returns
func (a *MCPToolAdapter) Description() string {
	return a.definition.Description
}

func (a *MCPToolAdapter) Parameters() map[string]any {
	return a.definition.InputSchema
}

func (a *MCPToolAdapter) Schema() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        a.definition.Name,
			"description": a.definition.Description,
			"parameters":  a.definition.InputSchema,
		},
	}
}

// Execute sends the call to the remote MCP server
func (a *MCPToolAdapter) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	logger.DebugCF("mcp_tool", "Executing MCP tool", map[string]any{
		"llm_tool_name": a.Name(),
		"mcp_tool_name": a.originalName,
		"args":          args,
	})

	result, err := a.client.CallTool(ctx, a.originalName, args)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("mcp call failed: %v", err)).WithError(err)
	}

	if result.IsError {
		errMsg := extractTextContent(result.Content)
		return tools.ErrorResult(errMsg)
	}

	successText := extractTextContent(result.Content)

	return tools.NewToolResult(successText)
}

func extractTextContent(blocks []MCPContentBlock) string {
	var text string
	for _, block := range blocks {
		if block.Type == "text" {
			text += block.Text + "\n"
		}
	}
	return text
}
