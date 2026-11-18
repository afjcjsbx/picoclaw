package commands

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/usage"
)

type MCPServerInfo struct {
	Name      string
	Enabled   bool
	Deferred  bool
	Connected bool
	ToolCount int
}

type MCPToolParameterInfo struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

type MCPToolInfo struct {
	Name        string
	Description string
	Parameters  []MCPToolParameterInfo
}

// PluginInfo describes an installed plugin package or configured plugin entry.
type PluginInfo struct {
	ID          string
	Name        string
	Root        string
	State       string
	Diagnostics int
}

// ContextStats describes current session context window usage.
type ContextStats struct {
	UsedTokens        int
	TotalTokens       int // model context window
	HistoryTokens     int // history-only tokens
	CompressAtTokens  int // hard budget compression threshold
	SummarizeAtTokens int // soft summarization trigger
	UsedPercent       int // 0-100
	MessageCount      int
}

// CompactReason explains the outcome of a manual compaction request.
type CompactReason string

const (
	CompactReasonCompacted    CompactReason = "compacted"
	CompactReasonBelowTarget  CompactReason = "below_target"
	CompactReasonTooShort     CompactReason = "too_short"
	CompactReasonNoCandidates CompactReason = "no_candidates"
	CompactReasonInProgress   CompactReason = "in_progress"
)

// CompactOutcome reports what a manual compaction did, including why it did
// nothing. TokensBefore and TokensAfter are assembled history token counts.
type CompactOutcome struct {
	Compacted          bool
	Reason             CompactReason
	TokensBefore       int
	TokensAfter        int
	Target             int
	LeafSummaries      int
	CondensedSummaries int
}

// StopResult describes the outcome of a stop request for the current session.
type StopResult struct {
	Stopped  bool
	TaskName string
}

// Runtime provides runtime dependencies to command handlers. It is constructed
// per-request by the agent loop so that per-request state (like session scope)
// can coexist with long-lived callbacks (like GetModelInfo).
type Runtime struct {
	Config             *config.Config
	VoiceMode          string
	SetVoiceMode       func(mode string) error
	GetModelInfo       func() (name, provider string)
	ListAgentIDs       func() []string
	ListDefinitions    func() []Definition
	ListSkillNames     func() []string
	ListMCPServers     func(ctx context.Context) []MCPServerInfo
	ListMCPTools       func(ctx context.Context, serverName string) ([]MCPToolInfo, error)
	ListPlugins        func(ctx context.Context) []PluginInfo
	GetEnabledChannels func() []string
	GetActiveTurnTree  func() []TurnInfo // Active root turn of the session followed by its subagents
	GetContextStats    func() *ContextStats
	GetUsageStats      func() (*usage.Summary, error)
	SwitchModel        func(value string) (oldModel string, err error)
	SwitchChannel      func(value string) error
	ClearHistory       func() error
	CompactContext     func(focus string, force bool) (CompactOutcome, error)
	ReloadConfig       func() error
	StopActiveTurn     func() (StopResult, error)
}
