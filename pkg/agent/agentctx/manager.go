package agentctx

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// ContextCompressReason identifies why context compaction ran.
type ContextCompressReason string

const (
	// ContextCompressReasonProactive indicates compression before the first LLM call.
	ContextCompressReasonProactive ContextCompressReason = "proactive_budget"
	// ContextCompressReasonRetry indicates compression during context-error retry handling.
	ContextCompressReasonRetry ContextCompressReason = "llm_retry"
	// ContextCompressReasonSummarize indicates post-turn async summarization.
	ContextCompressReasonSummarize ContextCompressReason = "summarize"
)

// AgentRef is the read-only slice of an agent that a ContextManager needs.
type AgentRef struct {
	ID                        string
	Model                     string
	Workspace                 string
	MaxTokens                 int
	ContextWindow             int
	SummarizeMessageThreshold int
	SummarizeTokenPercent     int
	Provider                  providers.LLMProvider
	Sessions                  session.SessionStore
}

// CompressResult describes a completed forced compression.
type CompressResult struct {
	Reason            ContextCompressReason
	DroppedMessages   int
	RemainingMessages int
}

// SummarizeResult describes a completed async session summarization.
type SummarizeResult struct {
	SummarizedMessages int
	KeptMessages       int
	SummaryLen         int
	OmittedOversized   bool
}

// Host is the narrow runtime surface a ContextManager may depend on.
// The agent loop implements it; agentctx never imports the loop.
type Host interface {
	// DefaultAgent returns the default agent, or false if none is configured.
	DefaultAgent() (AgentRef, bool)
	// AgentForSession returns the agent owning the session (default agent as fallback).
	AgentForSession(sessionKey string) (AgentRef, bool)
	// RegisterTool registers a tool with every agent.
	RegisterTool(tool tools.Tool)
	// ActiveRequestsInc/Dec bracket in-flight LLM requests for graceful shutdown.
	ActiveRequestsInc()
	ActiveRequestsDec()
	EmitContextCompress(sessionKey string, res CompressResult)
	EmitSessionSummarize(agentID, sessionKey string, res SummarizeResult)
}

// ContextManager manages conversation context via a pluggable strategy.
// Exactly ONE ContextManager is active per AgentLoop, selected by config.
// The default ("legacy") preserves current summarization behavior.
type ContextManager interface {
	// Assemble builds budget-aware context from the ContextManager's own storage.
	// Called before BuildMessages. Returns assembled messages ready for LLM.
	Assemble(ctx context.Context, req *AssembleRequest) (*AssembleResponse, error)

	// Compact compresses conversation history.
	// Called after turn completes (may be async internally) and on context overflow (sync).
	Compact(ctx context.Context, req *CompactRequest) error

	// Ingest records a message into the ContextManager's own storage.
	// Called after each message is persisted to session JSONL.
	Ingest(ctx context.Context, req *IngestRequest) error

	// Clear removes all stored context for a session (messages, summaries, etc.).
	// Called when the user issues /clear or /reset.
	Clear(ctx context.Context, sessionKey string) error
}

// AssembleRequest is the input to Assemble.
type AssembleRequest struct {
	SessionKey string // session identifier
	Budget     int    // context window in tokens
	MaxTokens  int    // max response tokens
}

// AssembleResponse is the output of Assemble.
type AssembleResponse struct {
	History []providers.Message // assembled conversation history for BuildMessages
	Summary string              // conversation summary embedded into system prompt by BuildMessages
}

// CompactRequest is the input to Compact.
type CompactRequest struct {
	SessionKey string                // session identifier
	Reason     ContextCompressReason // proactive_budget | llm_retry | summarize
	Budget     int                   // context window budget (used for retry aggressive compaction)
}

// IngestRequest is the input to Ingest.
type IngestRequest struct {
	SessionKey string            // session identifier
	Message    providers.Message // the message just persisted
}

// ContextManagerFactory constructs a ContextManager from config.
// host provides access to the runtime resources (agents, tools, events).
// cfg is the raw JSON configuration from config.json (may be nil).
type ContextManagerFactory func(cfg json.RawMessage, host Host) (ContextManager, error)

var (
	cmRegistryMu sync.RWMutex
	cmRegistry   = map[string]ContextManagerFactory{}
)

// RegisterContextManager registers a named ContextManager factory.
func RegisterContextManager(name string, factory ContextManagerFactory) error {
	if name == "" {
		return fmt.Errorf("context manager name is required")
	}
	if factory == nil {
		return fmt.Errorf("context manager %q factory is nil", name)
	}

	cmRegistryMu.Lock()
	defer cmRegistryMu.Unlock()

	if _, exists := cmRegistry[name]; exists {
		return fmt.Errorf("context manager %q is already registered", name)
	}
	cmRegistry[name] = factory
	return nil
}

func LookupContextManager(name string) (ContextManagerFactory, bool) {
	cmRegistryMu.RLock()
	defer cmRegistryMu.RUnlock()

	f, ok := cmRegistry[name]
	return f, ok
}

// ResetContextManagers empties the factory registry and returns a function
// restoring the previous state. Intended for tests.
func ResetContextManagers() (restore func()) {
	cmRegistryMu.Lock()
	original := cmRegistry
	cmRegistry = map[string]ContextManagerFactory{}
	cmRegistryMu.Unlock()

	return func() {
		cmRegistryMu.Lock()
		cmRegistry = original
		cmRegistryMu.Unlock()
	}
}
