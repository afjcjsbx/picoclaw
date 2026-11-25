// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// toolResultDetails renders the per-tool part of an archive stub: the command,
// path, URL or query that produced the output, plus its size. It deliberately
// uses only the tool call and the first bytes of the result, never a second copy
// of the evicted text.
func toolResultDetails(toolName, toolArgs, content string) string {
	name := strings.ToLower(strings.TrimSpace(toolName))
	size := formatByteSize(len(content))
	switch name {
	case "exec":
		return execToolResultDetails(toolArgs, content, size)
	case "read_file", "write_file", "edit_file", "append_file", "list_dir":
		return pathToolResultDetails(name, toolArgs, size)
	case "web_fetch":
		return argToolResultDetails("web_fetch", toolArgs, "url", size)
	case "web_search":
		return argToolResultDetails("web_search", toolArgs, "query", size)
	case "search_files":
		pattern := firstToolArg(toolArgs, "pattern")
		path := firstToolArg(toolArgs, "path")
		switch {
		case pattern != "" && path != "":
			return fmt.Sprintf(
				"search_files %q in %s, %s",
				compactPreview(pattern, 60),
				compactPreview(path, 80),
				size,
			)
		case pattern != "":
			return fmt.Sprintf("search_files %q, %s", compactPreview(pattern, 80), size)
		default:
			return fmt.Sprintf("search_files, %s", size)
		}
	}
	if name == "" {
		return size
	}
	if args := toolArgPreview(toolArgs); args != "" {
		return fmt.Sprintf("%s %s, %s", name, args, size)
	}
	return fmt.Sprintf("%s, %s", name, size)
}

func execToolResultDetails(toolArgs, content, size string) string {
	command := firstToolArg(toolArgs, "command")
	parts := []string{"exec"}
	if command != "" {
		parts = append(parts, fmt.Sprintf("%q", compactPreview(command, 80)))
	}
	if meta, ok := parseExecResultMeta(content); ok {
		if meta.ExitCode != nil {
			parts = append(parts, fmt.Sprintf("exit=%d", *meta.ExitCode))
		}
		if meta.TimedOut {
			parts = append(parts, "timed out")
		}
		if meta.Error != "" {
			parts = append(parts, "error="+compactPreview(meta.Error, 60))
		}
	}
	parts = append(parts, size)
	return strings.Join(parts, " ")
}

func pathToolResultDetails(name, toolArgs, size string) string {
	return singleArgToolResultDetails(name, toolArgs, "path", size, 100, false)
}

func argToolResultDetails(name, toolArgs, key, size string) string {
	return singleArgToolResultDetails(name, toolArgs, key, size, 90, true)
}

func singleArgToolResultDetails(name, toolArgs, key, size string, maxRunes int, quoted bool) string {
	value := firstToolArg(toolArgs, key)
	if value == "" {
		return fmt.Sprintf("%s, %s", name, size)
	}
	value = compactPreview(value, maxRunes)
	if quoted {
		return fmt.Sprintf("%s %q, %s", name, value, size)
	}
	return fmt.Sprintf("%s %s, %s", name, value, size)
}

// firstToolArg returns the first non-empty string argument among keys.
func firstToolArg(raw string, keys ...string) string {
	if raw == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return ""
	}
	for _, key := range keys {
		value, ok := args[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func toolArgPreview(raw string) string {
	return compactPreview(firstToolArg(raw, "command", "path", "url", "query", "pattern", "name"), 100)
}

func compactPreview(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	return toolshared.RunePrefix(text, maxRunes) + "..."
}

func formatByteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
