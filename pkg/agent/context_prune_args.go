// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/providers"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

const (
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

// truncateToolCallArgumentsInRange archives oversized arguments on assistant
// messages in history[start:end]. The replacement stays valid JSON and points
// at the archive, so providers can still parse the call and the model can
// recover the exact arguments with read_file.
func truncateToolCallArgumentsInRange(
	history []providers.Message,
	start, end int,
	workspace string,
) bool {
	if workspace == "" || start < 0 {
		return false
	}
	if start > len(history) {
		start = len(history)
	}
	if end > len(history) {
		end = len(history)
	}
	if start >= end {
		return false
	}
	// Never rewrite the assistant row that owns a tool round whose result sits
	// at the prune boundary: the model may still need to read that round.
	for end > start && end < len(history) && history[end].Role == "tool" {
		end--
	}

	changed := false
	for i := start; i < end; i++ {
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
