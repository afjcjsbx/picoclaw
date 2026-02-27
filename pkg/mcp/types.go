package mcp

import "encoding/json"

// JSONRPCMessage represents the basic payload for the MCP protocol
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *string         `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPToolDefinition represents a tool as returned by the MCP server
type MCPToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"` // JSON Schema per gli argomenti
}

type ListToolsResult struct {
	Tools []MCPToolDefinition `json:"tools"`
}

type CallToolRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type CallToolResult struct {
	Content []MCPContentBlock `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

type MCPContentBlock struct {
	Type string `json:"type"` // "text" o "image"
	Text string `json:"text,omitempty"`
}
