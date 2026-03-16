package tree

import (
	"encoding/json"

	"github.com/sipeed/picoclaw/pkg/providers"
)

const CurrentSessionVersion = 3

// SessionHeader is the first line in a JSONL session file.
// It does not have id/parentId like regular entries.
type SessionHeader struct {
	Type          string `json:"type"`                    // always "session"
	Version       int    `json:"version,omitempty"`       // session format version
	ID            string `json:"id"`                      // UUID
	Timestamp     string `json:"timestamp"`               // ISO 8601
	Cwd           string `json:"cwd"`                     // working directory
	ParentSession string `json:"parentSession,omitempty"` // path to parent session file
}

// SessionEntryBase is the common base for all session entries.
type SessionEntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`        // 8 hex chars
	ParentID  *string `json:"parentId"`  // null for root entries
	Timestamp string  `json:"timestamp"` // ISO 8601
}

// SessionMessageEntry holds a conversation message (type: "message").
type SessionMessageEntry struct {
	SessionEntryBase
	Message AgentMessage `json:"message"`
}

// ThinkingLevelChangeEntry records a thinking level change (type: "thinking_level_change").
type ThinkingLevelChangeEntry struct {
	SessionEntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

// ModelChangeEntry records a model change (type: "model_change").
type ModelChangeEntry struct {
	SessionEntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// CompactionEntry records a context compaction (type: "compaction").
type CompactionEntry struct {
	SessionEntryBase
	Summary          string          `json:"summary"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
	TokensBefore     int             `json:"tokensBefore"`
	Details          json.RawMessage `json:"details,omitempty"`
	FromHook         bool            `json:"fromHook,omitempty"`
}

// BranchSummaryEntry records a branch summary (type: "branch_summary").
type BranchSummaryEntry struct {
	SessionEntryBase
	FromID   string          `json:"fromId"`
	Summary  string          `json:"summary"`
	Details  json.RawMessage `json:"details,omitempty"`
	FromHook bool            `json:"fromHook,omitempty"`
}

// CustomEntry stores extension-specific data (type: "custom").
// Does NOT participate in LLM context.
type CustomEntry struct {
	SessionEntryBase
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// CustomMessageEntry stores extension messages that DO participate in LLM context (type: "custom_message").
type CustomMessageEntry struct {
	SessionEntryBase
	CustomType string          `json:"customType"`
	Content    string          `json:"content"`
	Details    json.RawMessage `json:"details,omitempty"`
	Display    bool            `json:"display"`
}

// LabelEntry is a user-defined bookmark on an entry (type: "label").
type LabelEntry struct {
	SessionEntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label"` // nil to clear
}

// SessionInfoEntry stores session metadata (type: "session_info").
type SessionInfoEntry struct {
	SessionEntryBase
	Name string `json:"name,omitempty"`
}

// SessionEntry is the union of all entry types. We store the raw JSON and
// decode on demand via the Type field.
type SessionEntry struct {
	// Common fields present in all entries.
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`

	// Message entry (type: "message")
	Message *AgentMessage `json:"message,omitempty"`

	// ThinkingLevelChange (type: "thinking_level_change")
	ThinkingLevel string `json:"thinkingLevel,omitempty"`

	// ModelChange (type: "model_change")
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`

	// Compaction (type: "compaction")
	Summary          string          `json:"summary,omitempty"`
	FirstKeptEntryID string          `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int             `json:"tokensBefore,omitempty"`
	Details          json.RawMessage `json:"details,omitempty"`
	FromHook         bool            `json:"fromHook,omitempty"`

	// BranchSummary (type: "branch_summary")
	FromID string `json:"fromId,omitempty"`
	// Summary and Details are shared with CompactionEntry

	// Custom / CustomMessage (type: "custom" / "custom_message")
	CustomType string          `json:"customType,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Content    string          `json:"content,omitempty"`
	Display    bool            `json:"display,omitempty"`

	// Label (type: "label")
	TargetID string  `json:"targetId,omitempty"`
	Label    *string `json:"label,omitempty"`

	// SessionInfo (type: "session_info")
	Name string `json:"name,omitempty"`

	// Raw preserves the original JSON line for lossless re-serialization.
	Raw json.RawMessage `json:"-"`
}

// FileEntry is either a SessionHeader or a SessionEntry (raw JSON line).
type FileEntry struct {
	Type string `json:"type"`
	// The rest is decoded into Header or Entry depending on Type.
	Header *SessionHeader
	Entry  *SessionEntry
	Raw    json.RawMessage
}

// AgentMessage maps picoclaw's providers.Message plus extended roles.
type AgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`

	// Provider/model info (for assistant messages)
	APIProvider string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`

	// Tool call fields
	ToolCalls  []providers.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`

	// Tool result fields
	ToolName string `json:"toolName,omitempty"`
	IsError  bool   `json:"isError,omitempty"`

	// Media
	Media []string `json:"media,omitempty"`

	// Extended message types
	CustomType string `json:"customType,omitempty"`
	Summary    string `json:"summary,omitempty"`
	FromID     string `json:"fromId,omitempty"`

	// Compaction summary fields
	TokensBefore int `json:"tokensBefore,omitempty"`

	// Display control (for custom messages)
	Display bool `json:"display,omitempty"`

	// Timestamp
	Timestamp int64 `json:"timestamp,omitempty"`
}

// SessionTreeNode represents a node in the session tree.
type SessionTreeNode struct {
	Entry    *SessionEntry
	Children []*SessionTreeNode
	Label    string
}

// SessionContext is the output of BuildSessionContext.
type SessionContext struct {
	Messages      []AgentMessage
	ThinkingLevel string
	Model         *ModelInfo
}

// ModelInfo holds provider and model ID.
type ModelInfo struct {
	Provider string
	ModelID  string
}

// SessionInfo provides metadata about a session file.
type SessionInfo struct {
	Path              string
	ID                string
	Cwd               string
	Name              string
	ParentSessionPath string
	Created           string
	Modified          string
	MessageCount      int
	FirstMessage      string
}

// parentIDStr returns the parentId as a string, or "" if nil.
func (e *SessionEntry) parentIDStr() string {
	if e.ParentID == nil {
		return ""
	}
	return *e.ParentID
}
