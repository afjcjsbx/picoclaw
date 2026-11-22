// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/providers"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
	// prunedToolResult replaces bulky tool outputs outside the protected tail
	// when they could not be archived.
	prunedToolResult = "[Old tool output cleared to save context space]"
	// archivedToolResultPrefix starts the stub that replaces an archived tool
	// output; it also marks a result as already evicted.
	archivedToolResultPrefix = "[Old tool output archived at "
	// duplicateToolResultPrefix starts the stub that replaces an older copy of
	// an identical tool result; it also marks the row as already pruned.
	duplicateToolResultPrefix = "[Duplicate tool output - "
	// duplicateToolResultShort is the fallback when the path-bearing duplicate
	// stub would not be shorter than the output it replaces.
	duplicateToolResultShort = "[Duplicate tool output - same content as a newer result]"
	protectedTail            = 4
	minToolResultChars       = 200
	// maxToolCallArgsChars bounds the tool-call arguments kept verbatim in
	// history. A larger argument string is archived and replaced by a compact,
	// still-valid JSON view. Historical calls are context, not execution.
	maxToolCallArgsChars = 4096
	// maxToolArgValueChars bounds one string value in the compact JSON view.
	maxToolArgValueChars = 400
	// archivedArgsKey marks a compacted argument object and points at the
	// archive that still holds the original.
	archivedArgsKey = "_picoclaw_archived"
)

// isEvictedToolResult reports whether content is a stub left by pruning.
func isEvictedToolResult(content string) bool {
	return content == prunedToolResult ||
		strings.HasPrefix(content, archivedToolResultPrefix) ||
		strings.HasPrefix(content, duplicateToolResultPrefix)
}

// resultLen is len(content) without trailing budget notices, so a notice alone
// never makes a short result bulky enough to be pruned.
func resultLen(content string) int {
	n := len(content)
	for _, notice := range [...]string{budgetNoticePrefix, runBudgetNotice} {
		if i := strings.LastIndex(content, "\n\n"+notice); i >= 0 {
			n = min(n, i)
		}
	}
	return n
}

// pruneOldToolResults evicts tool outputs longer than minToolResultChars that
// sit before the last protectedTail messages, in place: each is archived under
// workspace and replaced by a per-tool stub pointing at the file. Media-carrying
// results are kept. It reports whether anything changed.
func pruneOldToolResults(history []providers.Message, workspace string) bool {
	return pruneToolResultsBefore(history, len(history)-protectedTail, workspace)
}

// pruneToolResultsBefore is pruneOldToolResults for history[:end]. It also
// collapses older identical outputs into a back-reference stub and compacts
// oversized tool-call arguments, so pruning is lossless: the model can still
// recover the original text from the archive.
func pruneToolResultsBefore(history []providers.Message, end int, workspace string) bool {
	if len(history) == 0 {
		return false
	}
	if end > len(history) {
		end = len(history)
	}
	if end < 0 {
		end = 0
	}

	// Only an archived copy is a durable dedupe target. A copy still in the
	// protected tail has no archive of its own and may be summarized away later,
	// leaving an older back-reference with nothing to point at.
	dedupeTargets := make(map[[sha256.Size]byte]dedupeTarget)

	pruned := false
	for i := end - 1; i >= 0; i-- {
		msg := &history[i]
		if !isPruneCandidate(*msg) {
			continue
		}
		call := toolCallInfoForResult(history, i)
		sum := sha256.Sum256([]byte(msg.Content))
		if target, ok := dedupeTargets[sum]; ok {
			stub := duplicateToolResultStub(target, call.name)
			if len(stub) >= resultLen(msg.Content) {
				stub = duplicateToolResultShort
			}
			msg.Content = stub
			pruned = true
			continue
		}

		stub, archivePath := archiveToolResult(workspace, call.name, call.args, msg.Content)
		msg.Content = stub
		dedupeTargets[sum] = dedupeTarget{
			callID:      msg.ToolCallID,
			toolName:    call.name,
			archivePath: archivePath,
		}
		pruned = true
	}

	if truncateToolCallArguments(history, end, workspace) {
		pruned = true
	}
	return pruned
}

func isPruneCandidate(msg providers.Message) bool {
	return msg.Role == "tool" &&
		resultLen(msg.Content) > minToolResultChars &&
		len(msg.Media) == 0 &&
		len(msg.Attachments) == 0 &&
		!isEvictedToolResult(msg.Content)
}

type toolCallInfo struct {
	name string
	args string
}

// toolCallInfoForResult finds the assistant call that owns the tool result at
// index. The search is positional, not a global call-id map: some providers
// reuse ids such as "call_0" across turns, and a map would attribute an old
// result to the newest call with the same id.
func toolCallInfoForResult(history []providers.Message, index int) toolCallInfo {
	if index <= 0 || index >= len(history) {
		return toolCallInfo{}
	}
	callID := history[index].ToolCallID
	if callID == "" {
		return toolCallInfo{}
	}
	for i := index - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role == "user" {
			break
		}
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID == callID {
				return toolCallInfoFromCall(tc)
			}
		}
	}
	return toolCallInfo{}
}

func toolCallInfoFromCall(tc providers.ToolCall) toolCallInfo {
	info := toolCallInfo{}
	if tc.Function != nil {
		info.name = tc.Function.Name
		info.args = tc.Function.Arguments
	}
	if info.name == "" {
		info.name = tc.Name
	}
	if info.args == "" && len(tc.Arguments) > 0 {
		if encoded, err := json.Marshal(tc.Arguments); err == nil {
			info.args = string(encoded)
		}
	}
	return info
}

type dedupeTarget struct {
	callID      string
	toolName    string
	archivePath string
}

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

func duplicateToolResultStub(target dedupeTarget, fallbackName string) string {
	if target.archivePath == "" {
		return prunedToolResult
	}
	name := target.toolName
	if name == "" {
		name = fallbackName
	}
	label := "tool"
	if name != "" {
		label = name
	}
	call := ""
	if target.callID != "" {
		call = " (call " + target.callID + ")"
	}
	return fmt.Sprintf(
		"%ssame content as the archived %s result%s at %s; read_file it, or re-run the tool]",
		duplicateToolResultPrefix,
		label,
		call,
		target.archivePath,
	)
}

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
		pattern := toolArgString(toolArgs, "pattern")
		path := toolArgString(toolArgs, "path")
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
	command := toolArgString(toolArgs, "command")
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
	path := toolArgString(toolArgs, "path")
	if path == "" {
		return fmt.Sprintf("%s, %s", name, size)
	}
	return fmt.Sprintf("%s %s, %s", name, compactPreview(path, 100), size)
}

func argToolResultDetails(name, toolArgs, key, size string) string {
	value := toolArgString(toolArgs, key)
	if value == "" {
		return fmt.Sprintf("%s, %s", name, size)
	}
	return fmt.Sprintf("%s %q, %s", name, compactPreview(value, 90), size)
}

func toolArgString(raw string, keys ...string) string {
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
	if raw == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err == nil {
		for _, key := range []string{"command", "path", "url", "query", "pattern", "name"} {
			if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
				return compactPreview(value, 100)
			}
		}
	}
	return ""
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

// truncateToolCallArguments archives and compacts oversized historical tool-call
// arguments outside the protected region. The replacement stays valid JSON and
// points at the archive, so providers can still parse the call and the model can
// recover the exact arguments with read_file.
func truncateToolCallArguments(history []providers.Message, end int, workspace string) bool {
	if workspace == "" || end <= 0 {
		return false
	}
	if end > len(history) {
		end = len(history)
	}
	// Never rewrite the assistant row that owns a tool round whose result sits
	// at the prune boundary: the model may still need to read that round.
	for end > 0 && end < len(history) && history[end].Role == "tool" {
		end--
	}

	changed := false
	for i := 0; i < end; i++ {
		msg := &history[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			if tc.Function == nil {
				continue
			}
			args := tc.Function.Arguments
			if len(args) <= maxToolCallArgsChars || strings.Contains(args, archivedArgsKey) {
				continue
			}
			compact, ok := archiveAndCompactToolArgs(workspace, args)
			if !ok {
				continue
			}
			tc.Function.Arguments = compact
			var parsed map[string]any
			if err := json.Unmarshal([]byte(compact), &parsed); err == nil {
				tc.Arguments = parsed
			} else {
				tc.Arguments = nil
			}
			changed = true
		}
	}
	return changed
}

func archiveAndCompactToolArgs(workspace, args string) (string, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil || parsed == nil {
		return "", false
	}
	archivePath, err := toolshared.PersistTextArtifact(
		filepath.Join(workspace, ".artifacts", "evicted"), "tool_args_*.json", args)
	if err != nil {
		return "", false
	}

	compact := compactJSONObject(parsed)
	compact[archivedArgsKey] = archivePath
	encoded, err := json.Marshal(compact)
	if err != nil || len(encoded) >= len(args) {
		// A minimal pointer object still preserves the full arguments on disk.
		minimal, minimalErr := json.Marshal(map[string]any{
			archivedArgsKey:            archivePath,
			"_picoclaw_original_chars": len(args),
		})
		if minimalErr != nil || len(minimal) >= len(args) {
			_ = os.Remove(archivePath)
			return "", false
		}
		encoded = minimal
	}
	return string(encoded), true
}

func compactJSONObject(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = compactJSONItem(item)
	}
	return out
}

func compactJSONItem(value any) any {
	switch typed := value.(type) {
	case string:
		if utf8.RuneCountInString(typed) > maxToolArgValueChars {
			return toolshared.RunePrefix(typed, maxToolArgValueChars) + "..."
		}
		return typed
	case map[string]any:
		return compactJSONObject(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = compactJSONItem(item)
		}
		return out
	default:
		return value
	}
}
