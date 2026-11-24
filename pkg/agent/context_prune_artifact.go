// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// archiveToolResult replaces a tool output with a stub that points at the full
// text. It first reuses an artifact the tool already spilled (exec's
// artifactPath or the runtime's "Local artifact paths" note), so a bounded
// preview does not hide the full payload. Only when no readable spill exists
// does it write the inline content to .artifacts/evicted. An empty workspace, a
// write failure, or a stub no shorter than the output (a deep workspace path)
// falls back to prunedToolResult with no path.
func archiveToolResult(workspace, toolName, toolArgs, content string) (string, string) {
	if path := existingArtifactPath(toolName, content); path != "" {
		stub := archivedToolResultStub(path, toolName, toolArgs, content)
		if len(stub) < len(content) {
			return stub, path
		}
	}
	if workspace == "" {
		return prunedToolResult, ""
	}
	path, err := toolshared.PersistTextArtifact(
		filepath.Join(workspace, ".artifacts", "evicted"), "tool_result_*.txt", content)
	if err != nil {
		return prunedToolResult, ""
	}
	stub := archivedToolResultStub(path, toolName, toolArgs, content)
	if len(stub) >= len(content) {
		_ = os.Remove(path)
		return prunedToolResult, ""
	}
	return stub, path
}

// artifactNoteMarker starts the note ToolResult.ContentForLLM appends to a
// result with spilled artifacts.
const artifactNoteMarker = "Local artifact paths: "

// existingArtifactPath returns a readable file the tool already spilled. The
// exec header is trusted only for exec results; the runtime artifact note is
// parsed from the exact suffix the runtime appends, so a note-like string in
// the tool-controlled body cannot forge a pointer.
func existingArtifactPath(toolName, content string) string {
	if strings.EqualFold(toolName, "exec") {
		if meta, ok := parseExecResultMeta(content); ok && isRegularFile(meta.ArtifactPath) {
			return meta.ArtifactPath
		}
	}
	return runtimeArtifactPath(content)
}

// runtimeArtifactPath extracts the first readable artifact path from the note
// ContentForLLM appends at the very end of a result:
//
//	\nLocal artifact paths: [file:...] [file:...]\n<ArtifactPathsLLMNote>
//
// Requiring that suffix keeps a tool-controlled body from forging a note.
func runtimeArtifactPath(content string) string {
	content = trimTrailingBudgetNotices(content)
	suffix := "\n" + toolshared.ArtifactPathsLLMNote
	if !strings.HasSuffix(content, suffix) {
		return ""
	}
	body := strings.TrimSuffix(content, suffix)
	note := body
	if idx := strings.LastIndex(body, "\n"+artifactNoteMarker); idx >= 0 {
		note = body[idx+1:]
	} else if !strings.HasPrefix(body, artifactNoteMarker) {
		return ""
	}
	if strings.Contains(note, "\n") {
		return ""
	}
	for {
		start := strings.Index(note, "[file:")
		if start < 0 {
			return ""
		}
		rest := note[start+len("[file:"):]
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return ""
		}
		path := rest[:end]
		if isRegularFile(path) {
			return path
		}
		note = rest[end+1:]
	}
}

// trimTrailingBudgetNotices removes the notices the tool batch appends after
// the runtime artifact note. They are part of the persisted result, so the note
// is still the last meaningful line but no longer the literal suffix.
func trimTrailingBudgetNotices(content string) string {
	for {
		if suffix := "\n\n" + runBudgetNotice; strings.HasSuffix(content, suffix) {
			content = strings.TrimSuffix(content, suffix)
			continue
		}
		idx := strings.LastIndex(content, "\n\n"+budgetNoticePrefix)
		if idx < 0 {
			return content
		}
		tail := content[idx+2:]
		if strings.Contains(tail, "\n\n") || !strings.HasPrefix(tail, budgetNoticePrefix) {
			return content
		}
		content = content[:idx]
	}
}

func isRegularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func archivedToolResultStub(path, toolName, toolArgs, content string) string {
	details := toolResultDetails(toolName, toolArgs, content)
	if details == "" {
		return archivedToolResultPrefix + path + "; read_file it, or re-run the tool]"
	}
	return archivedToolResultPrefix + path + "; " + details + "; read_file it, or re-run the tool]"
}

type execResultMeta struct {
	ExitCode     *int   `json:"exitCode"`
	TimedOut     bool   `json:"timedOut"`
	Error        string `json:"error"`
	ArtifactPath string `json:"artifactPath"`
}

const execResultMetaPrefix = "[exec] "

func parseExecResultMeta(content string) (execResultMeta, bool) {
	if !strings.HasPrefix(content, execResultMetaPrefix) {
		return execResultMeta{}, false
	}
	header, _, _ := strings.Cut(content[len(execResultMetaPrefix):], "\n")
	var meta execResultMeta
	if err := json.Unmarshal([]byte(header), &meta); err != nil {
		return execResultMeta{}, false
	}
	return meta, true
}
