package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/fileutil"
)

// Memory targets accepted by MemoryTool.
const (
	MemoryTargetMemory = "memory" // memory/MEMORY.md: durable facts the agent learned
	MemoryTargetUser   = "user"   // USER.md: who the user is and how they like to work
)

type memoryFile struct {
	path  string
	limit int
}

// MemoryTool edits the two bounded memory files. It never truncates or drops
// content by itself: a write that would exceed the limit fails and tells the
// agent how many characters to free, so the agent decides what to consolidate.
//
// Files change on disk at once, but the system prompt carries a snapshot taken
// at session start, so a write is visible to the model from the next session.
type MemoryTool struct {
	mu    sync.Mutex
	files map[string]memoryFile
}

func NewMemoryTool(workspace string, memoryLimit, userLimit int) *MemoryTool {
	memoryPath, userPath := memoryFilePaths(workspace)
	return &MemoryTool{files: map[string]memoryFile{
		MemoryTargetMemory: {memoryPath, memoryLimit},
		MemoryTargetUser:   {userPath, userLimit},
	}}
}

func memoryFilePaths(workspace string) (memoryPath, userPath string) {
	return filepath.Join(workspace, "memory", "MEMORY.md"), filepath.Join(workspace, "USER.md")
}

// MemoryFilePaths returns the two files the memory tool manages, for write
// tools that must leave them to it.
func MemoryFilePaths(workspace string) []string {
	memoryPath, userPath := memoryFilePaths(workspace)
	return []string{memoryPath, userPath}
}

func (t *MemoryTool) Name() string { return "memory" }

func (t *MemoryTool) Description() string {
	return "Save durable facts to bounded persistent memory that survives across sessions.\n\n" +
		"Targets: `memory` = facts about the environment, projects, decisions and lessons learned (MEMORY.md); " +
		"`user` = who the user is: name, preferences, communication style (USER.md).\n\n" +
		"Actions: `add` appends `content` as a new entry; `replace` swaps the unique text `old_text` for `content` " +
		"(use it to shorten or fix an entry); `remove` deletes the line(s) holding the unique text `old_text`.\n\n" +
		"Keep each fact compact (1-2 lines). Never save task progress, logs or investigation details here: " +
		"those belong in the daily note memory/YYYYMM/YYYYMMDD.md.\n\n" +
		"Both files have a hard character limit and nothing is ever dropped silently. If a write would exceed it, " +
		"the call returns success=false with the characters to free and the current entries: consolidate, shorten " +
		"(replace) or delete (remove) entries, then retry.\n\n" +
		"Writes reach the file immediately but only show up in your system prompt from the next session: " +
		"do not re-save a fact to \"check\" it."
}

func (t *MemoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"add", "replace", "remove"}},
			"target": map[string]any{
				"type": "string", "enum": []string{MemoryTargetMemory, MemoryTargetUser},
				"description": "memory = MEMORY.md, user = USER.md.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Entry text. Required for add and replace.",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Text that appears exactly once in the file. Required for replace and remove.",
			},
		},
		"required": []string{"action", "target"},
	}
}

func (t *MemoryTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if err := ctx.Err(); err != nil {
		return ErrorResult(err.Error())
	}
	action, _ := args["action"].(string)
	target, _ := args["target"].(string)
	content, _ := args["content"].(string)
	oldText, _ := args["old_text"].(string)
	content = strings.TrimSpace(content)

	file, ok := t.files[target]
	if !ok {
		return ErrorResult("target must be memory or user")
	}
	needContent := action == "add" || action == "replace"
	needOld := action == "replace" || action == "remove"
	switch {
	case action != "add" && action != "replace" && action != "remove":
		return ErrorResult("action must be add, replace or remove")
	case needContent && content == "":
		return ErrorResult(action + " requires non-empty content")
	case needOld && oldText == "":
		return ErrorResult(action + " requires old_text")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	data, err := os.ReadFile(file.path)
	if err != nil && !os.IsNotExist(err) {
		return ErrorResult("read " + target + ": " + err.Error())
	}
	cur := strings.TrimRight(string(data), " \t\r\n")
	next := cur

	switch action {
	case "add":
		for _, line := range strings.Split(cur, "\n") {
			if strings.TrimSpace(line) == content {
				return memorySuccess(target, "Already saved.", cur, file.limit)
			}
		}
		next = strings.TrimLeft(cur+"\n"+content, "\n")
	case "replace", "remove":
		switch n := strings.Count(cur, oldText); {
		case n == 0:
			return ErrorResult("old_text not found in " + target)
		case n > 1:
			return ErrorResult(fmt.Sprintf("old_text matches %d places in %s; use a longer, unique snippet", n, target))
		}
		i := strings.Index(cur, oldText)
		if action == "replace" {
			next = cur[:i] + content + cur[i+len(oldText):]
			break
		}
		// remove: drop whole lines so no half-entry is left behind.
		start := strings.LastIndex(cur[:i], "\n") + 1
		end := len(cur)
		if j := strings.Index(cur[i+len(oldText):], "\n"); j >= 0 {
			end = i + len(oldText) + j + 1
		}
		next = strings.TrimRight(cur[:start]+cur[end:], " \t\r\n")
	}

	// Growth past the limit is refused; a shrinking edit is always allowed so an
	// oversized legacy file can be cut down step by step.
	if used, curUsed := utf8.RuneCountInString(next), utf8.RuneCountInString(cur); used > file.limit && used > curUsed {
		return memoryOverLimit(target, cur, used, file.limit)
	}
	if next != "" {
		next += "\n"
	}
	if err := fileutil.WriteFileAtomic(file.path, []byte(next), 0o600); err != nil {
		return ErrorResult("write " + target + ": " + err.Error())
	}
	return memorySuccess(target, "Saved. It appears in your system prompt from the next session.", next, file.limit)
}

func memoryUsage(text string, limit int) string {
	return fmt.Sprintf("%d/%d chars", utf8.RuneCountInString(strings.TrimSpace(text)), limit)
}

func memoryEntries(text string) []string {
	var entries []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			entries = append(entries, line)
		}
	}
	return entries
}

func memorySuccess(target, msg, text string, limit int) *ToolResult {
	out, _ := json.Marshal(map[string]any{
		"success": true, "target": target, "message": msg, "usage": memoryUsage(text, limit),
	})
	return SilentResult(string(out))
}

func memoryOverLimit(target, cur string, used, limit int) *ToolResult {
	free := used - limit
	out, _ := json.Marshal(map[string]any{
		"success": false,
		"target":  target,
		"error": fmt.Sprintf(
			"%s would be %d/%d chars: free at least %d chars by shortening (replace) or removing entries, then retry",
			target, used, limit, free,
		),
		"usage":           memoryUsage(cur, limit),
		"chars_to_free":   free,
		"current_entries": memoryEntries(cur),
	})
	return ErrorResult(string(out))
}
