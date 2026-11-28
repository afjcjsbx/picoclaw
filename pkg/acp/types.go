// Package acp implements the Agent Client Protocol surface used by PicoClaw.
package acp

import "encoding/json"

// ProtocolVersion is the ACP schema version implemented by this package.
const ProtocolVersion = 1

const (
	// StopReasonCancelled is the ACP wire value for a canceled prompt turn.
	StopReasonCancelled = "cancelled" //nolint:misspell // ACP wire value.
	// PermissionOutcomeCancelled is the ACP wire value for a canceled permission request.
	PermissionOutcomeCancelled = "cancelled" //nolint:misspell // ACP wire value.
)

// Implementation identifies an ACP peer.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeRequest is sent by the client when an ACP connection is created.
type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// InitializeResponse negotiates the protocol version and agent capabilities.
type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
}

// ClientCapabilities describes optional ACP features understood by the client.
type ClientCapabilities struct {
	FS          *FileSystemCapabilities    `json:"fs,omitempty"`
	Terminal    bool                       `json:"terminal,omitempty"`
	Session     *ClientSessionCapabilities `json:"session,omitempty"`
	Subagents   *SubagentCapabilities      `json:"subagents,omitempty"`
	Plan        *PlanCapabilities          `json:"plan,omitempty"`
	Auth        AuthCapabilities           `json:"auth,omitempty"`
	Elicitation json.RawMessage            `json:"elicitation,omitempty"`
}

// FileSystemCapabilities advertises client-side file operations.
type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// ClientSessionCapabilities advertises optional session update variants.
type ClientSessionCapabilities struct {
	Notices       *NoticeCapabilities               `json:"notices,omitempty"`
	Compaction    *CompactionCapabilities           `json:"compaction,omitempty"`
	ConfigOptions *SessionConfigOptionsCapabilities `json:"configOptions,omitempty"`
}

// NoticeCapabilities indicates that the client accepts notice updates.
type NoticeCapabilities struct{}

// CompactionCapabilities indicates that the client accepts compaction updates.
type CompactionCapabilities struct{}

// SessionConfigOptionsCapabilities advertises optional config option types.
type SessionConfigOptionsCapabilities struct {
	Boolean *BooleanConfigOptionCapabilities `json:"boolean,omitempty"`
}

// BooleanConfigOptionCapabilities indicates support for boolean config options.
type BooleanConfigOptionCapabilities struct{}

// SubagentCapabilities indicates support for child-session updates.
type SubagentCapabilities struct{}

// PlanCapabilities indicates support for identified plan updates.
type PlanCapabilities struct{}

// AuthCapabilities describes client-side authentication capabilities.
type AuthCapabilities struct {
	Terminal bool `json:"terminal,omitempty"`
}

// AgentCapabilities describes the ACP features implemented by PicoClaw.
type AgentCapabilities struct {
	LoadSession         bool                   `json:"loadSession"`
	PromptCapabilities  PromptCapabilities     `json:"promptCapabilities"`
	MCPCapabilities     MCPCapabilities        `json:"mcpCapabilities"`
	SessionCapabilities SessionCapabilities    `json:"sessionCapabilities"`
	Auth                *AgentAuthCapabilities `json:"auth,omitempty"`
}

// PromptCapabilities describes content accepted in session prompts.
type PromptCapabilities struct {
	Image           bool `json:"image,omitempty"`
	Audio           bool `json:"audio,omitempty"`
	EmbeddedContext bool `json:"embeddedContext,omitempty"`
}

// MCPCapabilities describes MCP transports accepted from ACP clients.
type MCPCapabilities struct {
	HTTP bool `json:"http,omitempty"`
	SSE  bool `json:"sse,omitempty"`
}

// SessionCapabilities advertises optional session lifecycle methods.
type SessionCapabilities struct {
	List                  *SessionListCapabilities                  `json:"list,omitempty"`
	Delete                *SessionDeleteCapabilities                `json:"delete,omitempty"`
	AdditionalDirectories *SessionAdditionalDirectoriesCapabilities `json:"additionalDirectories,omitempty"`
	SystemPrompt          *SessionSystemPromptCapabilities          `json:"systemPrompt,omitempty"`
	Resume                *SessionResumeCapabilities                `json:"resume,omitempty"`
	Close                 *SessionCloseCapabilities                 `json:"close,omitempty"`
}

// SessionListCapabilities indicates support for session/list.
type SessionListCapabilities struct{}

// SessionDeleteCapabilities indicates support for session/delete.
type SessionDeleteCapabilities struct{}

// SessionAdditionalDirectoriesCapabilities indicates support for extra roots.
type SessionAdditionalDirectoriesCapabilities struct{}

// SessionSystemPromptCapabilities indicates support for client system prompts.
type SessionSystemPromptCapabilities struct{}

// SessionResumeCapabilities indicates support for session/resume.
type SessionResumeCapabilities struct{}

// SessionCloseCapabilities indicates support for session/close.
type SessionCloseCapabilities struct{}

// AgentAuthCapabilities describes agent-side authentication features.
type AgentAuthCapabilities struct{}

// AuthMethod describes an ACP authentication method. PicoClaw currently uses
// credentials configured outside the ACP process and advertises no methods.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// NewSessionRequest creates an ACP conversation.
type NewSessionRequest struct {
	CWD                   string        `json:"cwd"`
	AdditionalDirectories []string      `json:"additionalDirectories,omitempty"`
	MCPServers            []MCPServer   `json:"mcpServers"`
	SystemPrompt          *SystemPrompt `json:"systemPrompt,omitempty"`
}

// NewSessionResponse returns the new session and its initial configuration.
type NewSessionResponse struct {
	SessionID     string                `json:"sessionId"`
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

// LoadSessionRequest restores a persisted ACP conversation.
type LoadSessionRequest struct {
	MCPServers            []MCPServer   `json:"mcpServers"`
	CWD                   string        `json:"cwd"`
	AdditionalDirectories []string      `json:"additionalDirectories,omitempty"`
	SessionID             string        `json:"sessionId"`
	SystemPrompt          *SystemPrompt `json:"systemPrompt,omitempty"`
}

// LoadSessionResponse returns the restored session configuration.
type LoadSessionResponse struct {
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

// ResumeSessionRequest restores a session without replaying its history.
type ResumeSessionRequest struct {
	MCPServers            []MCPServer   `json:"mcpServers,omitempty"`
	CWD                   string        `json:"cwd"`
	AdditionalDirectories []string      `json:"additionalDirectories,omitempty"`
	SessionID             string        `json:"sessionId"`
	SystemPrompt          *SystemPrompt `json:"systemPrompt,omitempty"`
}

// ResumeSessionResponse returns the resumed session configuration.
type ResumeSessionResponse struct {
	Modes         *SessionModeState     `json:"modes,omitempty"`
	ConfigOptions []SessionConfigOption `json:"configOptions,omitempty"`
}

// SystemPrompt carries optional client-provided system instructions.
type SystemPrompt struct {
	Mode    string         `json:"mode"`
	Content []ContentBlock `json:"content"`
}

// MCPServer is the wire representation of an ACP MCP server. An empty Type
// means the stdio transport, whose ACP schema has no type discriminator.
type MCPServer struct {
	Type    string        `json:"type,omitempty"`
	Name    string        `json:"name"`
	Command string        `json:"command,omitempty"`
	Args    []string      `json:"args,omitempty"`
	Env     []EnvVariable `json:"env,omitempty"`
	URL     string        `json:"url,omitempty"`
	Headers []HTTPHeader  `json:"headers,omitempty"`
}

// EnvVariable is an environment value passed to a stdio MCP server.
type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HTTPHeader is an HTTP header passed to an MCP server.
type HTTPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PromptRequest submits user content to an ACP session.
type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

// PromptResponse reports why a prompt turn stopped.
type PromptResponse struct {
	StopReason string `json:"stopReason"`
}

// CancelNotification cancels the active prompt turn for a session.
type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// ContentBlock is the flattened union of ACP text, image, audio, resource-link,
// and embedded-resource content blocks.
type ContentBlock struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	Data     string            `json:"data,omitempty"`
	MIMEType string            `json:"mimeType,omitempty"`
	URI      string            `json:"uri,omitempty"`
	Name     string            `json:"name,omitempty"`
	Resource *EmbeddedResource `json:"resource,omitempty"`
}

// EmbeddedResource is text or base64 resource content embedded in a prompt.
type EmbeddedResource struct {
	URI      string `json:"uri"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// SessionNotification carries one session update to the client.
type SessionNotification struct {
	SessionID string         `json:"sessionId"`
	Update    map[string]any `json:"update"`
}

// ToolCallUpdate describes a tool call or a patch to an existing tool call.
type ToolCallUpdate struct {
	ToolCallID string             `json:"toolCallId"`
	Title      string             `json:"title,omitempty"`
	Name       string             `json:"name,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Content    []ToolCallContent  `json:"content,omitempty"`
	Locations  []ToolCallLocation `json:"locations,omitempty"`
	RawInput   any                `json:"rawInput,omitempty"`
	RawOutput  any                `json:"rawOutput,omitempty"`
}

// ToolCallContent is regular content, a diff, a terminal reference, or a
// session reference attached to a tool call.
type ToolCallContent struct {
	Type      string        `json:"type"`
	Content   *ContentBlock `json:"content,omitempty"`
	Path      string        `json:"path,omitempty"`
	OldText   string        `json:"oldText,omitempty"`
	NewText   string        `json:"newText,omitempty"`
	SessionID string        `json:"sessionId,omitempty"`
}

// ToolCallLocation identifies a file affected by a tool call.
type ToolCallLocation struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
}

// RequestPermissionRequest asks the client to authorize a tool call.
type RequestPermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallUpdate     `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// RequestPermissionResponse carries the client's permission decision.
type RequestPermissionResponse struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOption is one choice presented for a permission request.
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// PermissionOutcome is either a selected option or cancellation.
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// SessionConfigOption is a session-level selector. PicoClaw currently emits
// select options; the type also accepts boolean options from newer clients.
type SessionConfigOption struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Description  string                      `json:"description,omitempty"`
	Category     string                      `json:"category,omitempty"`
	Type         string                      `json:"type"`
	CurrentValue any                         `json:"currentValue"`
	Options      []SessionConfigSelectOption `json:"options,omitempty"`
}

// SessionConfigSelectOption is one value in a session config selector.
type SessionConfigSelectOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SetSessionConfigOptionRequest changes a session configuration value.
type SetSessionConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Type      string `json:"type,omitempty"`
	Value     any    `json:"value"`
}

// SetSessionConfigOptionResponse returns the complete configuration state.
type SetSessionConfigOptionResponse struct {
	ConfigOptions []SessionConfigOption `json:"configOptions"`
}

// SessionModeState is the legacy ACP representation of session modes.
type SessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []SessionMode `json:"availableModes"`
}

// SessionMode describes one legacy ACP mode.
type SessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SetSessionModeRequest changes the legacy ACP session mode.
type SetSessionModeRequest struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// SetSessionModeResponse is an empty ACP response.
type SetSessionModeResponse struct{}

// AvailableCommand advertises a slash command to the client.
type AvailableCommand struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Input       *AvailableCommandInput `json:"input,omitempty"`
}

// AvailableCommandInput describes optional command input.
type AvailableCommandInput struct {
	Hint string `json:"hint"`
}

// AvailableCommandsUpdate replaces the session's advertised command list.
type AvailableCommandsUpdate struct {
	AvailableCommands []AvailableCommand `json:"availableCommands"`
}

// UsageUpdate reports current context usage and optional cumulative cost.
type UsageUpdate struct {
	Used int64 `json:"used"`
	Size int64 `json:"size"`
	Cost *Cost `json:"cost,omitempty"`
}

// Cost is a cumulative session cost.
type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// CompactionUpdate reports a context compaction lifecycle change.
type CompactionUpdate struct {
	CompactionID string         `json:"compactionId"`
	Status       string         `json:"status"`
	Summary      []ContentBlock `json:"summary,omitempty"`
	Error        string         `json:"error,omitempty"`
}

// TextContent creates an ACP text content block.
func TextContent(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// TextToolContent wraps text as tool-call content.
func TextToolContent(text string) ToolCallContent {
	content := TextContent(text)
	return ToolCallContent{Type: "content", Content: &content}
}
