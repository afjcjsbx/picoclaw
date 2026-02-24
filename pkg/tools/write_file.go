package tools

import (
	"context"
	"fmt"
)

type WriteFileTool struct {
	fs fileSystem
}

func NewWriteFileTool(workspace string, restrict bool) *WriteFileTool {
	var fs fileSystem
	if restrict {
		fs = &sandboxFs{workspace: workspace}
	} else {
		fs = &hostFs{}
	}
	return &WriteFileTool{fs: fs}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Write content to a file"
}

func (t *WriteFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	if err := t.fs.WriteFile(path, []byte(content)); err != nil {
		return ErrorResult(err.Error())
	}

	return SilentResult(fmt.Sprintf("File written: %s", path))
}
