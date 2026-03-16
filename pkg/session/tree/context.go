package tree

import (
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// buildSessionContext walks the tree from the current leaf to the root,
// producing the message list for the LLM. Handles compaction, branch summaries,
// and custom messages.
func buildSessionContext(
	entries []*SessionEntry,
	leafID *string,
	byID map[string]*SessionEntry,
) SessionContext {
	if byID == nil {
		byID = make(map[string]*SessionEntry, len(entries))
		for _, e := range entries {
			byID[e.ID] = e
		}
	}

	// Explicitly nil leafID = no messages (navigated before first entry)
	if leafID == nil {
		return SessionContext{ThinkingLevel: "off"}
	}

	leaf := byID[*leafID]
	if leaf == nil {
		// Fallback to last entry
		if len(entries) > 0 {
			leaf = entries[len(entries)-1]
		}
	}
	if leaf == nil {
		return SessionContext{ThinkingLevel: "off"}
	}

	// Walk from leaf to root, collecting the path
	var path []*SessionEntry
	current := leaf
	for current != nil {
		path = append([]*SessionEntry{current}, path...)
		if current.ParentID == nil {
			break
		}
		current = byID[*current.ParentID]
	}

	// Extract settings and find compaction
	thinkingLevel := "off"
	var model *ModelInfo
	var compaction *SessionEntry

	for _, entry := range path {
		switch entry.Type {
		case "thinking_level_change":
			thinkingLevel = entry.ThinkingLevel
		case "model_change":
			model = &ModelInfo{Provider: entry.Provider, ModelID: entry.ModelID}
		case "message":
			if entry.Message != nil && entry.Message.Role == "assistant" && entry.Message.APIProvider != "" {
				model = &ModelInfo{Provider: entry.Message.APIProvider, ModelID: entry.Message.Model}
			}
		case "compaction":
			compaction = entry
		}
	}

	// Build messages
	var messages []AgentMessage

	appendMsg := func(entry *SessionEntry) {
		switch entry.Type {
		case "message":
			if entry.Message != nil {
				messages = append(messages, *entry.Message)
			}
		case "custom_message":
			messages = append(messages, AgentMessage{
				Role:       "custom",
				CustomType: entry.CustomType,
				Content:    entry.Content,
				Display:    entry.Display,
				Timestamp:  parseTimestamp(entry.Timestamp),
			})
		case "branch_summary":
			if entry.Summary != "" {
				messages = append(messages, AgentMessage{
					Role:      "branchSummary",
					Summary:   entry.Summary,
					FromID:    entry.FromID,
					Timestamp: parseTimestamp(entry.Timestamp),
				})
			}
		}
	}

	if compaction != nil {
		// 1. Emit compaction summary first
		messages = append(messages, AgentMessage{
			Role:         "compactionSummary",
			Summary:      compaction.Summary,
			TokensBefore: compaction.TokensBefore,
			Timestamp:    parseTimestamp(compaction.Timestamp),
		})

		// Find compaction index in path
		compactionIdx := -1
		for i, e := range path {
			if e.Type == "compaction" && e.ID == compaction.ID {
				compactionIdx = i
				break
			}
		}

		// 2. Emit kept messages (before compaction, starting from firstKeptEntryId)
		foundFirstKept := false
		for i := 0; i < compactionIdx; i++ {
			entry := path[i]
			if entry.ID == compaction.FirstKeptEntryID {
				foundFirstKept = true
			}
			if foundFirstKept {
				appendMsg(entry)
			}
		}

		// 3. Emit messages after compaction
		for i := compactionIdx + 1; i < len(path); i++ {
			appendMsg(path[i])
		}
	} else {
		// No compaction - emit all messages
		for _, entry := range path {
			appendMsg(entry)
		}
	}

	// Log the traversed path for debugging
	if len(path) > 0 {
		var pathDesc []string
		for _, e := range path {
			desc := e.ID[:4] + ":" + e.Type
			if e.Type == "message" && e.Message != nil {
				desc += "(" + e.Message.Role + ")"
			}
			pathDesc = append(pathDesc, desc)
		}
		hasCompaction := "no"
		if compaction != nil {
			hasCompaction = fmt.Sprintf("yes (keep from %s)", compaction.FirstKeptEntryID[:4])
		}
		logger.DebugCF("session", "Context path: "+strings.Join(pathDesc, " -> "), map[string]any{
			"path_length": len(path),
			"messages":    len(messages),
			"compaction":  hasCompaction,
		})
	}

	return SessionContext{
		Messages:      messages,
		ThinkingLevel: thinkingLevel,
		Model:         model,
	}
}

// parseTimestamp parses an ISO 8601 timestamp to Unix milliseconds.
// Returns 0 on parse error.
func parseTimestamp(ts string) int64 {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return 0
		}
	}
	return t.UnixMilli()
}
