// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT

package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
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
