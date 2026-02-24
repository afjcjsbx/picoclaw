package tools

import (
	"context"
)

type ReadFileTool struct {
	fs fileSystem
}

func NewReadFileTool(workspace string, restrict bool) *ReadFileTool {
	var fs fileSystem
	if restrict {
		fs = &sandboxFs{workspace: workspace}
	} else {
		fs = &hostFs{}
	}
	return &ReadFileTool{fs: fs}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read the contents of a file"
}

func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, err := t.fs.ReadFile(path)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return NewToolResult(string(content))
}
