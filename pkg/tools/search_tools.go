package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// PromotedToolTTL defines for how many iterations of the LLM a discovered tool
// remains unlocked and available in the as native tool before becoming invisible again.
const PromotedToolTTL = 10

type RegexSearchTool struct {
	registry *ToolRegistry
}

func NewRegexSearchTool(r *ToolRegistry) *RegexSearchTool {
	return &RegexSearchTool{registry: r}
}

func (t *RegexSearchTool) Name() string {
	return "tool_search_tool_regex"
}

func (t *RegexSearchTool) Description() string {
	return "Search available tools on-demand using a regex pattern. Returns JSON schemas of discovered tools."
}

func (t *RegexSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to match tool name or description",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *RegexSearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	pattern, _ := args["pattern"].(string)
	res, err := t.registry.SearchRegex(pattern)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if len(res) == 0 {
		return SilentResult("No tools found matching the pattern.")
	}

	// promote tools found for the next iterations (ttl)
	for _, r := range res {
		t.registry.PromoteTool(r.Name, PromotedToolTTL)
	}

	b, _ := json.MarshalIndent(res, "", "  ")
	msg := fmt.Sprintf(
		"Found %d tools:\n%s\n\nsuccess: These tools have been temporarily UNLOCKED as native tools! In your next response, you can call them directly just like any normal tool, without needing 'call_discovered_tool'.",
		len(res),
		string(b),
	)
	return SilentResult(msg)
}

type BM25SearchTool struct {
	registry *ToolRegistry
}

func NewBM25SearchTool(r *ToolRegistry) *BM25SearchTool {
	return &BM25SearchTool{registry: r}
}

func (t *BM25SearchTool) Name() string {
	return "tool_search_tool_bm25"
}

func (t *BM25SearchTool) Description() string {
	// return "Search available tools on-demand using natural language query. Returns JSON schemas of discovered tools."
	return "CRITICAL TOOL: You have a massive catalog of hidden tools. If you lack a specific tool to fulfill the user's request, you MUST use this tool to search the catalog using natural language (e.g. query='github repos', 'weather forecast', 'database'). It unlocks the tools so you can use them normally."
}

func (t *BM25SearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
		},
		"required": []string{"query"},
	}
}

func (t *BM25SearchTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	query, _ := args["query"].(string)
	res := t.registry.SearchBM25(query)
	if len(res) == 0 {
		return SilentResult("No tools found matching the query.")
	}

	// promote  tools found for the next iterations (ttl)
	for _, r := range res {
		t.registry.PromoteTool(r.Name, PromotedToolTTL)
	}

	b, _ := json.MarshalIndent(res, "", "  ")
	msg := fmt.Sprintf(
		"Found %d tools:\n%s\n\nSUCCESS: These tools have been temporarily UNLOCKED as native tools! In your next response, you can call them directly just like any normal tool, without needing 'call_discovered_tool'.",
		len(res),
		string(b),
	)

	return SilentResult(msg)
}

type CallDiscoveredTool struct {
	registry *ToolRegistry
}

func NewCallDiscoveredTool(r *ToolRegistry) *CallDiscoveredTool {
	return &CallDiscoveredTool{registry: r}
}

func (t *CallDiscoveredTool) Name() string {
	return "call_discovered_tool"
}

func (t *CallDiscoveredTool) Description() string {
	return "Fallback tool. Execute a tool found via tool_search_tool by passing arguments as a JSON string."
}

func (t *CallDiscoveredTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_name": map[string]any{
				"type": "string",
			},
			"arguments_json": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"tool_name", "arguments_json"},
	}
}

func (t *CallDiscoveredTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	name, _ := args["tool_name"].(string)
	argsJSONStr, _ := args["arguments_json"].(string)

	var parsedArgs map[string]any
	if err := json.Unmarshal([]byte(argsJSONStr), &parsedArgs); err != nil {
		return ErrorResult("invalid arguments_json format: " + err.Error())
	}

	// renew TTL when tool is used
	t.registry.PromoteTool(name, PromotedToolTTL)

	return t.registry.Execute(ctx, name, parsedArgs)
}
