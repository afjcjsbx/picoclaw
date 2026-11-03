package fstools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// EditFileTool edits a file by replacing old_text with new_text, or by
// applying a V4A patch. The old_text must exist exactly in the file.
type EditFileTool struct {
	fs        fileSystem
	workspace string
}

// NewEditFileTool creates a new EditFileTool with optional directory restriction.
func NewEditFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *EditFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &EditFileTool{fs: buildFs(workspace, restrict, patterns), workspace: workspace}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Description() string {
	return "Edit a file by replacing old_text with new_text, or by applying a V4A patch through patch. " +
		"The old_text must exist exactly in the file. Prefer patch for multi-hunk edits: it accepts " +
		"'*** Begin Patch' / '*** End Patch' with an optional '*** Update File: <path>' header, and hunk lines " +
		"starting with ' ' (context), '-' (remove), or '+' (add). Hunks may be given in any order and each needs at least one context or removed line; a hunk with no context appends at end of file when it ends with '*** End of File'. Provide either patch or old_text/new_text, not both. " +
		"Standard JSON escaping applies: \\n for newline and \\\\n for literal backslash-n."
}

func (t *EditFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to edit",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace. Required when patch is not provided. Standard JSON escaping applies: \\n for newline and \\\\n for literal backslash-n.",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "The text to replace with. Required when patch is not provided. Standard JSON escaping applies: \\n for newline and \\\\n for literal backslash-n.",
			},
			"patch": map[string]any{
				"type": "string",
				"description": "A V4A patch for this file: optional \"*** Begin Patch\" / \"*** End Patch\" wrapper, " +
					"an optional \"*** Update File: <path>\" header, then hunks whose lines start with ' ' (context), " +
					"'-' (remove), or '+' (add). '@@' starts a hunk and \"*** End of File\" anchors the last hunk to EOF, " +
					"or appends a hunk without context at EOF. Each hunk needs a context or removed line, unless it ends with \"*** End of File\" to append; " +
					"the context must match exactly once, and hunks may be given in any order. A line containing a single space is an empty context line; blank lines before a closing directive are ignored. " +
					"Use instead of old_text/new_text.",
			},
		},
		"required": []string{"path"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	patchRaw, hasPatch := args["patch"]
	if hasPatch {
		patchText, isString := patchRaw.(string)
		if !isString {
			return ErrorResult("patch must be a string")
		}
		if strings.TrimSpace(patchText) == "" {
			return ErrorResult("patch is empty")
		}
		if _, hasOld := args["old_text"]; hasOld {
			return ErrorResult("provide either patch or old_text/new_text, not both")
		}
		if _, hasNew := args["new_text"]; hasNew {
			return ErrorResult("provide either patch or old_text/new_text, not both")
		}

		beforeContent, afterContent, err := editFileWithPatch(t.fs, path, patchText)
		if err != nil {
			return ErrorResult(fmt.Sprintf("patch failed: %v", err))
		}
		return DiffResult(path, beforeContent, afterContent)
	}

	oldText, ok := args["old_text"].(string)
	if !ok {
		return ErrorResult("old_text is required")
	}

	newText, ok := args["new_text"].(string)
	if !ok {
		return ErrorResult("new_text is required")
	}

	beforeContent, afterContent, err := editFile(t.fs, path, oldText, newText)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return DiffResult(path, beforeContent, afterContent)
}

type AppendFileTool struct {
	fs        fileSystem
	workspace string
}

func NewAppendFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *AppendFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &AppendFileTool{fs: buildFs(workspace, restrict, patterns), workspace: workspace}
}

func (t *AppendFileTool) Name() string {
	return "append_file"
}

func (t *AppendFileTool) Description() string {
	return "Append content to the end of a file. Standard JSON escaping applies: \\n for newline and \\\\n for literal backslash-n."
}

func (t *AppendFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to append to",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to append. Standard JSON escaping applies: \\n for newline and \\\\n for literal backslash-n.",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *AppendFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	if err := appendFile(t.fs, path, content); err != nil {
		return ErrorResult(err.Error())
	}
	return SilentResult(fmt.Sprintf("Appended to %s", path))
}

// editFileWithPatch reads the file via sysFs, applies a V4A patch, and writes
// back only when the content changed.
func editFileWithPatch(sysFs fileSystem, path, patch string) ([]byte, []byte, error) {
	content, err := sysFs.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	newContent, err := applyV4APatch(content, patch, path)
	if err != nil {
		return nil, nil, err
	}
	if bytes.Equal(content, newContent) {
		return content, newContent, nil
	}

	if err := sysFs.WriteFile(path, newContent); err != nil {
		return nil, nil, err
	}
	return content, newContent, nil
}

// editFile reads the file via sysFs, performs the replacement, and writes back.
// It uses a fileSystem interface, allowing the same logic for both restricted and unrestricted modes.
func editFile(sysFs fileSystem, path, oldText, newText string) ([]byte, []byte, error) {
	content, err := sysFs.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	newContent, err := replaceEditContent(content, oldText, newText)
	if err != nil {
		return nil, nil, err
	}

	if err := sysFs.WriteFile(path, newContent); err != nil {
		return nil, nil, err
	}

	return content, newContent, nil
}

// appendFile appends content through the filesystem's append implementation
// when available, falling back to read+write for filesystems that cannot
// append in place.
func appendFile(sysFs fileSystem, path, appendContent string) error {
	if appender, ok := sysFs.(fileAppender); ok {
		return appender.AppendFile(path, []byte(appendContent))
	}

	content, err := sysFs.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return sysFs.WriteFile(path, append(content, []byte(appendContent)...))
}

// replaceEditContent handles the core logic of finding and replacing a single occurrence of oldText.
func replaceEditContent(content []byte, oldText, newText string) ([]byte, error) {
	contentStr := string(content)

	if !strings.Contains(contentStr, oldText) {
		return nil, fmt.Errorf("old_text not found in file. Make sure it matches exactly")
	}

	count := strings.Count(contentStr, oldText)
	if count > 1 {
		return nil, fmt.Errorf("old_text appears %d times. Please provide more context to make it unique", count)
	}

	newContent := strings.Replace(contentStr, oldText, newText, 1)
	return []byte(newContent), nil
}
