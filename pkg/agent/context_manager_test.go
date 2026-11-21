package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// ---------------------------------------------------------------------------
// Factory registry tests
// ---------------------------------------------------------------------------

func TestRegisterContextManager_Success(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return &noopContextManager{}, nil
	}
	if err := RegisterContextManager("test_cm", factory); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	f, ok := lookupContextManager("test_cm")
	if !ok {
		t.Fatal("expected factory to be registered")
	}
	if f == nil {
		t.Fatal("expected non-nil factory")
	}
}

func TestRegisterContextManager_EmptyName(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	err := RegisterContextManager("", func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return &noopContextManager{}, nil
	})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegisterContextManager_NilFactory(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	err := RegisterContextManager("nil_factory", nil)
	if err == nil {
		t.Fatal("expected error for nil factory")
	}
	if !strings.Contains(err.Error(), "factory is nil") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegisterContextManager_Duplicate(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return &noopContextManager{}, nil
	}
	if err := RegisterContextManager("dup_cm", factory); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	err := RegisterContextManager("dup_cm", factory)
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLookupContextManager_Unknown(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	_, ok := lookupContextManager("nonexistent")
	if ok {
		t.Fatal("expected lookup to fail for unknown name")
	}
}

// ---------------------------------------------------------------------------
// resolveContextManager tests
// ---------------------------------------------------------------------------

func TestResolveContextManager_Default(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "", // default → legacy
			},
		},
	}
	al := newCMTestAgentLoop(cfg)

	cm := al.contextManager
	if cm == nil {
		t.Fatal("expected non-nil context manager")
	}
	if _, ok := cm.(*legacyContextManager); !ok {
		t.Fatalf("expected *legacyContextManager, got %T", cm)
	}
}

func TestResolveContextManager_ExplicitLegacy(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "legacy",
			},
		},
	}
	al := newCMTestAgentLoop(cfg)

	if _, ok := al.contextManager.(*legacyContextManager); !ok {
		t.Fatalf("expected *legacyContextManager, got %T", al.contextManager)
	}
}

func TestResolveContextManager_UnknownFallsBackToLegacy(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "unknown_cm",
			},
		},
	}
	al := newCMTestAgentLoop(cfg)

	if _, ok := al.contextManager.(*legacyContextManager); !ok {
		t.Fatalf("expected fallback to *legacyContextManager, got %T", al.contextManager)
	}
}

func TestResolveContextManager_RegisteredFactory(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return &noopContextManager{}, nil
	}
	if err := RegisterContextManager("custom_cm", factory); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "custom_cm",
			},
		},
	}
	al := newCMTestAgentLoop(cfg)

	if _, ok := al.contextManager.(*noopContextManager); !ok {
		t.Fatalf("expected *noopContextManager, got %T", al.contextManager)
	}
}

func TestResolveContextManager_FactoryError(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return nil, os.ErrPermission
	}
	if err := RegisterContextManager("broken_cm", factory); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "broken_cm",
			},
		},
	}
	al := newCMTestAgentLoop(cfg)

	// Should fall back to legacy when factory returns error
	if _, ok := al.contextManager.(*legacyContextManager); !ok {
		t.Fatalf("expected fallback to *legacyContextManager on factory error, got %T", al.contextManager)
	}
}

// ---------------------------------------------------------------------------
// Legacy Assemble tests
// ---------------------------------------------------------------------------

func TestLegacyAssemble_Passthrough(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	history := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	if err := agent.Sessions.SetHistory(context.Background(), "test-session", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	resp, err := al.contextManager.Assemble(context.Background(), &AssembleRequest{
		SessionKey: "test-session",
		Budget:     8000,
		MaxTokens:  4096,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.History) != len(history) {
		t.Fatalf("expected %d messages, got %d", len(history), len(resp.History))
	}
	for i, msg := range resp.History {
		if msg.Content != history[i].Content || msg.Role != history[i].Role {
			t.Fatalf("message %d mismatch: want %+v, got %+v", i, history[i], msg)
		}
	}
}

func TestLegacyAssemble_EmptyHistory(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	resp, err := al.contextManager.Assemble(context.Background(), &AssembleRequest{
		SessionKey: "test-session",
		Budget:     8000,
		MaxTokens:  4096,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.History) != 0 {
		t.Fatalf("expected empty messages, got %d", len(resp.History))
	}
}

// ---------------------------------------------------------------------------
// Legacy Compact overflow tests
// ---------------------------------------------------------------------------

func TestLegacyCompact_Overflow(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	history := []providers.Message{
		{Role: "user", Content: "msg 1"},
		{Role: "assistant", Content: "resp 1"},
		{Role: "user", Content: "msg 2"},
		{Role: "assistant", Content: "resp 2"},
		{Role: "user", Content: "msg 3"},
	}
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-overflow", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	runtimeCh, closeRuntimeEvents := subscribeRuntimeEventsForTest(
		t,
		al,
		16,
		runtimeevents.KindAgentContextCompress,
	)
	defer closeRuntimeEvents()

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-overflow",
		Reason:     ContextCompressReasonRetry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After overflow compression, history should be shorter
	newHistory, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-overflow")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(newHistory) >= len(history) {
		t.Fatalf("expected compressed history, got %d messages (was %d)", len(newHistory), len(history))
	}

	// Summary should contain compression note
	summary, err := defaultAgent.Sessions.GetSummary(context.Background(), "session-overflow")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if !strings.Contains(summary, "Emergency compression") {
		t.Fatalf("expected compression note in summary, got %q", summary)
	}

	// Event should carry the proactive reason
	events := collectRuntimeEventStream(runtimeCh)
	compressEvt, ok := findRuntimeEvent(events, runtimeevents.KindAgentContextCompress)
	if !ok {
		t.Fatal("expected context compress event")
	}
	payload, ok := compressEvt.Payload.(ContextCompressPayload)
	if !ok {
		t.Fatalf("expected ContextCompressPayload, got %T", compressEvt.Payload)
	}
	if payload.Reason != ContextCompressReasonRetry {
		t.Fatalf("expected retry reason, got %q", payload.Reason)
	}
}

func TestLegacyCompact_PrunesOldToolResultsBeforeDroppingTurns(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)
	agent := al.registry.GetDefaultAgent()
	largeOutput := strings.Repeat("tool output ", 30)
	history := []providers.Message{
		{Role: "user", Content: "read a file"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "old-call"}}},
		{Role: "tool", ToolCallID: "old-call", Content: largeOutput},
		{Role: "assistant", Content: "file read"},
		{Role: "user", Content: "run a command"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "new-call"}}},
		{Role: "tool", ToolCallID: "new-call", Content: "recent output"},
		{Role: "assistant", Content: "done"},
	}
	if err := agent.Sessions.SetHistory(context.Background(), "session-prune-tools", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	result, ok := al.contextManager.(*legacyContextManager).forceCompression("session-prune-tools")
	if !ok {
		t.Fatal("expected old tool output to be pruned")
	}
	got, err := agent.Sessions.GetHistory(context.Background(), "session-prune-tools")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got) != len(history) || result.DroppedMessages != 0 {
		t.Fatalf("pruning dropped messages: got %d messages, dropped %d", len(got), result.DroppedMessages)
	}
	data := readArchivedToolResult(t, got[2].Content)
	if string(data) != largeOutput || got[2].ToolCallID != "old-call" {
		t.Fatalf("old tool result not safely archived: %+v", got[2])
	}
	if got[6].Content != "recent output" {
		t.Fatalf("recent tool result should remain intact: %+v", got[6])
	}
}

func TestLegacyCompact_Overflow_ProactiveReason(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	history := []providers.Message{
		{Role: "user", Content: "msg 1"},
		{Role: "assistant", Content: "resp 1"},
		{Role: "user", Content: "msg 2"},
		{Role: "assistant", Content: "resp 2"},
		{Role: "user", Content: "msg 3"},
	}
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-proactive", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	runtimeCh, closeRuntimeEvents := subscribeRuntimeEventsForTest(
		t,
		al,
		16,
		runtimeevents.KindAgentContextCompress,
	)
	defer closeRuntimeEvents()

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-proactive",
		Reason:     ContextCompressReasonProactive,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	events := collectRuntimeEventStream(runtimeCh)
	compressEvt, ok := findRuntimeEvent(events, runtimeevents.KindAgentContextCompress)
	if !ok {
		t.Fatal("expected context compress event")
	}
	if compressEvt.Scope.AgentID != defaultAgent.ID {
		t.Fatalf("event agent = %q, want %q", compressEvt.Scope.AgentID, defaultAgent.ID)
	}
	payload, ok := compressEvt.Payload.(ContextCompressPayload)
	if !ok {
		t.Fatalf("expected ContextCompressPayload, got %T", compressEvt.Payload)
	}
	if payload.Reason != ContextCompressReasonProactive {
		t.Fatalf("expected proactive reason, got %q", payload.Reason)
	}
}

func TestLegacyCompact_Overflow_TooShortToCompress(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	history := []providers.Message{
		{Role: "user", Content: "only one"},
	}
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-tiny", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-tiny",
		Reason:     ContextCompressReasonRetry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// History should be unchanged (too short to compress)
	newHistory, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-tiny")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(newHistory) != len(history) {
		t.Fatalf("expected history unchanged, got %d messages (was %d)", len(newHistory), len(history))
	}
}

// ---------------------------------------------------------------------------
// Legacy Compact post-turn tests
// ---------------------------------------------------------------------------

func TestLegacyCompact_Threshold_NothingToSummarize(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	history := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-small", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-small",
		Reason:     ContextCompressReasonThreshold,
	})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}

	newHistory, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-small")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(newHistory) != len(history) {
		t.Fatalf("expected unchanged history, got %d messages (was %d)", len(newHistory), len(history))
	}
}

func newSummarizeThresholdAgentLoop(t *testing.T, contextWindow int) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:             t.TempDir(),
				ModelName:             "test-model",
				MaxTokens:             4096,
				MaxToolIterations:     10,
				ContextWindow:         contextWindow,
				SummarizeTokenPercent: 75,
			},
		},
	}
	return NewAgentLoop(cfg, bus.NewMessageBus(), &simpleMockProvider{response: "summary"})
}

func alternatingHistory(turns int, content string) []providers.Message {
	history := make([]providers.Message, 0, turns*2)
	for i := 0; i < turns; i++ {
		history = append(history,
			providers.Message{Role: "user", Content: content},
			providers.Message{Role: "assistant", Content: content},
		)
	}
	return history
}

func TestLegacyCompact_Threshold_SummarizesSynchronously(t *testing.T) {
	al := newSummarizeThresholdAgentLoop(t, 8000)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	history := alternatingHistory(3, strings.Repeat("x", 2000))
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-threshold", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-threshold",
		Reason:     ContextCompressReasonThreshold,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No waiting: the session is already compacted when Compact returns.
	newHistory, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-threshold")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(newHistory) >= len(history) {
		t.Fatalf("expected summarization to reduce history from %d messages, got %d", len(history), len(newHistory))
	}
	summary, err := defaultAgent.Sessions.GetSummary(context.Background(), "session-threshold")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if summary != "summary" {
		t.Fatalf("summary = %q, want %q", summary, "summary")
	}
}

// readArchivedToolResult returns the file an archive stub points at.
func readArchivedToolResult(t *testing.T, stub string) []byte {
	t.Helper()
	rest, ok := strings.CutPrefix(stub, archivedToolResultPrefix)
	if !ok {
		t.Fatalf("content = %q, want an archive stub", stub)
	}
	path, _, _ := strings.Cut(rest, ";")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("archived output unreadable: %v", err)
	}
	return data
}

// Evicted tool outputs are archived under the workspace and the stub fits the
// prune limit, so a later pass leaves it alone instead of archiving it again.
func TestPruneToolResultsArchivesBeforeEviction(t *testing.T) {
	workspace := t.TempDir()
	bulky := strings.Repeat("z", 500)
	history := []providers.Message{
		{Role: "tool", Content: bulky},
		{Role: "tool", Content: "short"},
	}
	if !pruneToolResultsBefore(history, len(history), workspace) {
		t.Fatal("expected the bulky result to be evicted")
	}
	if got := readArchivedToolResult(t, history[0].Content); string(got) != bulky {
		t.Fatalf("archived output = %q, want the original", got)
	}
	if len(history[0].Content) >= len(bulky) {
		t.Fatalf("stub is %d chars, want it shorter than the %d chars it replaces", len(history[0].Content), len(bulky))
	}
	if history[1].Content != "short" {
		t.Fatalf("short result changed: %q", history[1].Content)
	}
	if pruneToolResultsBefore(history, len(history), workspace) {
		t.Fatal("a stub must not be archived again")
	}
	files, _ := os.ReadDir(filepath.Join(workspace, ".artifacts", "evicted"))
	if len(files) != 1 {
		t.Fatalf("archive files = %d, want 1", len(files))
	}
}

// A stub longer than the output it replaces would grow the context, so a result
// barely over the prune limit is cleared instead and leaves no file behind.
func TestPruneToolResultsSkipsArchiveWhenStubIsLonger(t *testing.T) {
	workspace := t.TempDir()
	history := []providers.Message{{Role: "tool", Content: strings.Repeat("z", minToolResultChars+1)}}
	workspace = filepath.Join(workspace, strings.Repeat("d", 200)) // deep path: the stub outgrows the output
	if !pruneToolResultsBefore(history, 1, workspace) {
		t.Fatal("expected eviction")
	}
	if history[0].Content != prunedToolResult {
		t.Fatalf("content = %q, want %q", history[0].Content, prunedToolResult)
	}
	files, _ := os.ReadDir(filepath.Join(workspace, ".artifacts", "evicted"))
	if len(files) != 0 {
		t.Fatalf("archive files = %d, want 0", len(files))
	}
}

// Without a usable archive the eviction stays lossy but still frees the context.
func TestPruneToolResultsFallsBackWhenArchiveFails(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range []string{"", blocked} { // no workspace; .artifacts under a regular file
		history := []providers.Message{{Role: "tool", Content: strings.Repeat("z", 500)}}
		if !pruneToolResultsBefore(history, 1, workspace) {
			t.Fatalf("workspace %q: expected eviction", workspace)
		}
		if history[0].Content != prunedToolResult {
			t.Fatalf("workspace %q: content = %q, want %q", workspace, history[0].Content, prunedToolResult)
		}
	}
}

// The archive stub carries the tool that produced the output and its key
// details, so the model can decide whether to read the archive or re-run it.
func TestPruneToolResultStubCarriesToolDetails(t *testing.T) {
	workspace := t.TempDir()
	original := "[exec] {\"exitCode\":2,\"timedOut\":false,\"totalBytes\":2500}\n" +
		strings.Repeat("FAIL ", 500)
	history := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "c1",
				Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"go test ./..."}`},
			}},
		},
		{Role: "tool", ToolCallID: "c1", Content: original},
	}
	if !pruneToolResultsBefore(history, len(history), workspace) {
		t.Fatal("expected the bulky result to be evicted")
	}
	stub := history[1].Content
	if !strings.HasPrefix(stub, archivedToolResultPrefix) {
		t.Fatalf("stub = %q, want an archive stub", stub)
	}
	for _, want := range []string{"exec", "go test ./...", "exit=2", "KB"} {
		if !strings.Contains(stub, want) {
			t.Fatalf("stub %q missing %q", stub, want)
		}
	}
	if data := readArchivedToolResult(t, stub); string(data) != original {
		t.Fatalf("archived output = %q, want the original", data)
	}
}

// A tool result that already spilled its full payload keeps that pointer
// instead of writing a second bounded copy under .artifacts/evicted.
func TestPruneToolResultReusesExistingArtifact(t *testing.T) {
	workspace := t.TempDir()
	spillDir := filepath.Join(workspace, ".artifacts", "tool")
	if err := os.MkdirAll(spillDir, 0o750); err != nil {
		t.Fatal(err)
	}
	spill := filepath.Join(spillDir, "full.log")
	full := strings.Repeat("full output line\n", 100)
	if err := os.WriteFile(spill, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	original := fmt.Sprintf(
		"[exec] {\"exitCode\":0,\"artifactPath\":%q}\n%s\n\nLocal artifact paths: [file:%s]\nUse send_file.",
		spill,
		strings.Repeat("preview line\n", 30),
		spill,
	)
	history := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "c1",
				Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"cat full.log"}`},
			}},
		},
		{Role: "tool", ToolCallID: "c1", Content: original},
	}
	if !pruneToolResultsBefore(history, len(history), workspace) {
		t.Fatal("expected the result to be evicted")
	}
	stub := history[1].Content
	if !strings.Contains(stub, spill) {
		t.Fatalf("stub = %q, want the existing spill path %q", stub, spill)
	}
	if data := readArchivedToolResult(t, stub); string(data) != full {
		t.Fatalf("spill content = %q, want the full output", data)
	}
	files, err := os.ReadDir(filepath.Join(workspace, ".artifacts", "evicted"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("archive dir: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("evicted archive files = %d, want 0 (reused spill)", len(files))
	}
}

// Providers may reuse a tool-call id across turns (for example "call_0").
// The stub must describe the call in the same turn, not the newest call with
// that id.
func TestPruneToolResultStubResolvesReusedCallIDs(t *testing.T) {
	workspace := t.TempDir()
	first := strings.Repeat("first output ", 60)
	second := strings.Repeat("second output ", 60)
	history := []providers.Message{
		{Role: "user", Content: "run the first command"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "call_0",
				Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"first-command"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call_0", Content: first},
		{Role: "user", Content: "read the second file"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "call_0",
				Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"second.txt"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call_0", Content: second},
	}
	if !pruneToolResultsBefore(history, len(history), workspace) {
		t.Fatal("expected both results to be evicted")
	}
	if !strings.Contains(history[2].Content, "first-command") {
		t.Fatalf("first stub = %q, want the first command", history[2].Content)
	}
	if !strings.Contains(history[5].Content, "second.txt") {
		t.Fatalf("second stub = %q, want the second path", history[5].Content)
	}
}

// Identical old outputs are collapsed into a back-reference instead of being
// archived once per copy. The newest copy keeps the archive pointer.
func TestPruneToolResultsDedupesIdenticalOutputs(t *testing.T) {
	workspace := t.TempDir()
	bulky := strings.Repeat("same output ", 100)
	history := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "old",
				Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"a.txt"}`},
			}},
		},
		{Role: "tool", ToolCallID: "old", Content: bulky},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "new",
				Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"a.txt"}`},
			}},
		},
		{Role: "tool", ToolCallID: "new", Content: bulky},
	}
	if !pruneToolResultsBefore(history, len(history), workspace) {
		t.Fatal("expected the duplicates to be pruned")
	}
	if !strings.HasPrefix(history[1].Content, duplicateToolResultPrefix) {
		t.Fatalf("older duplicate = %q, want a duplicate stub", history[1].Content)
	}
	if !strings.Contains(history[1].Content, "archived") {
		t.Fatalf("older duplicate = %q, want the archive pointer", history[1].Content)
	}
	if !strings.HasPrefix(history[3].Content, archivedToolResultPrefix) {
		t.Fatalf("newest copy = %q, want an archive stub", history[3].Content)
	}
	files, err := os.ReadDir(filepath.Join(workspace, ".artifacts", "evicted"))
	if err != nil {
		t.Fatalf("archive dir: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("archive files = %d, want 1", len(files))
	}
	if data := readArchivedToolResult(t, history[3].Content); string(data) != bulky {
		t.Fatalf("archived output = %q, want the original", data)
	}
}

// A duplicate that points at a full copy in the protected tail is safe to
// collapse without writing an archive: the newer copy remains in the prompt.
func TestPruneToolResultsDedupesAgainstProtectedTail(t *testing.T) {
	workspace := t.TempDir()
	bulky := strings.Repeat("same output ", 100)
	history := []providers.Message{
		{Role: "tool", Content: bulky},
		{Role: "assistant", Content: "boundary"},
		{Role: "tool", Content: bulky},
	}
	if !pruneToolResultsBefore(history, 1, workspace) {
		t.Fatal("expected the older duplicate to be pruned")
	}
	if !strings.HasPrefix(history[0].Content, duplicateToolResultPrefix) {
		t.Fatalf("older duplicate = %q, want a duplicate stub", history[0].Content)
	}
	if history[2].Content != bulky {
		t.Fatalf("protected copy changed: %q", history[2].Content)
	}
	files, err := os.ReadDir(filepath.Join(workspace, ".artifacts", "evicted"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("archive dir: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("archive files = %d, want 0", len(files))
	}
}

// Oversized tool-call arguments are archived and replaced by a compact, valid
// JSON object that still carries the original key names and an archive pointer.
func TestPruneToolCallArgumentsArchivesOversizedArgs(t *testing.T) {
	workspace := t.TempDir()
	content := strings.Repeat("a", maxToolCallArgsChars+100)
	argsBytes, err := json.Marshal(map[string]any{"path": "big.txt", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsBytes)
	history := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "c1",
				Function: &providers.FunctionCall{Name: "write_file", Arguments: args},
			}},
		},
	}
	if !pruneToolResultsBefore(history, 1, workspace) {
		t.Fatal("expected the oversized arguments to be compacted")
	}
	compact := history[0].ToolCalls[0].Function.Arguments
	if len(compact) >= len(args) {
		t.Fatalf("compact arguments = %d chars, want fewer than %d", len(compact), len(args))
	}
	var parsed map[string]any
	if unmarshalErr := json.Unmarshal([]byte(compact), &parsed); unmarshalErr != nil {
		t.Fatalf("compact arguments are not valid JSON: %v", unmarshalErr)
	}
	if parsed["path"] != "big.txt" {
		t.Fatalf("compact arguments lost path: %+v", parsed)
	}
	archivePath, ok := parsed[archivedArgsKey].(string)
	if !ok || archivePath == "" {
		t.Fatalf("compact arguments lack %s: %+v", archivedArgsKey, parsed)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archived arguments: %v", err)
	}
	if string(data) != args {
		t.Fatalf("archived arguments = %q, want the original", data)
	}
	value, ok := parsed["content"].(string)
	if !ok || len(value) > maxToolArgValueChars+len("...") {
		t.Fatalf("content value was not compacted: %q", value)
	}
}

// A tool round whose result sits at the prune boundary is still unread: its
// arguments must not be rewritten.
func TestPruneToolCallArgumentsKeepsBoundaryToolRound(t *testing.T) {
	workspace := t.TempDir()
	content := strings.Repeat("a", maxToolCallArgsChars+100)
	argsBytes, err := json.Marshal(map[string]any{"path": "big.txt", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	args := string(argsBytes)
	history := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "c1",
				Function: &providers.FunctionCall{Name: "write_file", Arguments: args},
			}},
		},
		{Role: "tool", ToolCallID: "c1", Content: "short result"},
	}
	if pruneToolResultsBefore(history, 1, workspace) {
		t.Fatal("boundary tool round must not be pruned")
	}
	if got := history[0].ToolCalls[0].Function.Arguments; got != args {
		t.Fatalf("boundary arguments changed: %q", got)
	}
}

// Invalid JSON cannot be rewritten without breaking the provider payload, so
// the arguments stay byte-exact.
func TestPruneToolCallArgumentsLeavesInvalidJSON(t *testing.T) {
	workspace := t.TempDir()
	args := `{"content":"` + strings.Repeat("a", maxToolCallArgsChars+100)
	history := []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:       "c1",
				Function: &providers.FunctionCall{Name: "write_file", Arguments: args},
			}},
		},
	}
	if pruneToolResultsBefore(history, 1, workspace) {
		t.Fatal("invalid JSON arguments must not be pruned")
	}
	if got := history[0].ToolCalls[0].Function.Arguments; got != args {
		t.Fatalf("arguments changed: %q", got)
	}
}

// With nothing older than the active turn to summarize, the threshold path
// clears old tool outputs instead, and reports a clean session as nothing to do.
func TestLegacyCompact_Threshold_PrunesToolOutputsOfSingleTurn(t *testing.T) {
	al := newSummarizeThresholdAgentLoop(t, 8000)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	bulky := strings.Repeat("z", 500)
	recent := strings.Repeat("y", 500)
	newest := strings.Repeat("x", 500)
	history := []providers.Message{
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "c1", Name: "t"}}},
		{Role: "tool", ToolCallID: "c1", Content: bulky},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "c2", Name: "t"}}},
		{Role: "tool", ToolCallID: "c2", Content: recent},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "c3", Name: "t"}}},
		{Role: "tool", ToolCallID: "c3", Content: newest},
	}
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-single-turn", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	req := &CompactRequest{SessionKey: "session-single-turn", Reason: ContextCompressReasonThreshold}
	if err := al.contextManager.Compact(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-single-turn")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got) != len(history) {
		t.Fatalf("messages = %d, want %d (pruning must not drop messages)", len(got), len(history))
	}
	if data := readArchivedToolResult(t, got[2].Content); string(data) != bulky {
		t.Fatalf("archived output = %q, want the original", data)
	}
	if got[6].Content != newest {
		t.Fatalf("recent tool output must be kept, got %q", got[6].Content)
	}

	if err := al.contextManager.Compact(context.Background(), req); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("second compaction err = %v, want ErrNothingToCompact", err)
	}
}

// Legacy no longer summarizes after the turn: the post-turn call is a no-op and
// the work happens synchronously before the next LLM call.
func TestLegacyCompact_PostTurnSummarizeIsNoOp(t *testing.T) {
	al := newSummarizeThresholdAgentLoop(t, 8000)
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	history := alternatingHistory(3, strings.Repeat("x", 2000))
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-post-turn", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-post-turn",
		Reason:     ContextCompressReasonSummarize,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	newHistory, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-post-turn")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(newHistory) != len(history) {
		t.Fatalf("expected history unchanged, got %d messages (was %d)", len(newHistory), len(history))
	}
}

func TestComputeContextUsage_SummarizeAtIsPercentOfUsableWindow(t *testing.T) {
	al := newSummarizeThresholdAgentLoop(t, 8000)
	usage := computeContextUsage(al.registry.GetDefaultAgent(), "session-usage")
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.CompressAtTokens != 8000-4096 {
		t.Fatalf("CompressAtTokens = %d, want %d", usage.CompressAtTokens, 8000-4096)
	}
	if want := usage.CompressAtTokens * 75 / 100; usage.SummarizeAtTokens != want {
		t.Fatalf("SummarizeAtTokens = %d, want %d", usage.SummarizeAtTokens, want)
	}
}

// ---------------------------------------------------------------------------
// Legacy Ingest tests
// ---------------------------------------------------------------------------

func TestLegacyIngest_NoOp(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	err := al.contextManager.Ingest(context.Background(), &IngestRequest{
		SessionKey: "session-ingest",
		Message:    providers.Message{Role: "user", Content: "test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Mock ContextManager — verifies dispatch through AgentLoop
// ---------------------------------------------------------------------------

func TestAgentLoop_UsesCustomContextManager(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	mock := &trackingContextManager{}
	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return mock, nil
	}
	if err := RegisterContextManager("tracking_cm", factory); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "tracking_cm",
			},
		},
	}
	al := newCMTestAgentLoop(cfg)

	// Verify the mock was installed
	if al.contextManager != mock {
		t.Fatalf("expected mock context manager, got %T", al.contextManager)
	}

	// Direct method calls
	_, err := mock.Assemble(context.Background(), &AssembleRequest{
		SessionKey: "s1",
		Budget:     8000,
		MaxTokens:  4096,
	})
	if err != nil {
		t.Fatalf("Assemble error: %v", err)
	}
	if mock.assembleCalls.Load() != 1 {
		t.Fatalf("expected 1 assemble call, got %d", mock.assembleCalls.Load())
	}

	err = mock.Compact(context.Background(), &CompactRequest{
		SessionKey: "s1",
		Reason:     ContextCompressReasonRetry,
	})
	if err != nil {
		t.Fatalf("Compact error: %v", err)
	}
	if mock.compactCalls.Load() != 1 {
		t.Fatalf("expected 1 compact call, got %d", mock.compactCalls.Load())
	}

	err = mock.Ingest(context.Background(), &IngestRequest{
		SessionKey: "s1",
		Message:    providers.Message{Role: "user", Content: "test"},
	})
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if mock.ingestCalls.Load() != 1 {
		t.Fatalf("expected 1 ingest call, got %d", mock.ingestCalls.Load())
	}
}

func TestIngestCalledDuringTurn(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	mock := &trackingContextManager{}
	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return mock, nil
	}
	if err := RegisterContextManager("ingest_track_cm", factory); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "ingest_track_cm",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "done"})
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	// Run a turn — ingestMessage is called for user message and final assistant message
	_, err := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey:      "session-ingest-turn",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "test ingest",
		DefaultResponse: defaultResponse,
		EnableSummary:   false,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop failed: %v", err)
	}

	// Should have at least 2 ingest calls: user message + final assistant message
	if mock.ingestCalls.Load() < 2 {
		t.Fatalf("expected >= 2 ingest calls during turn, got %d", mock.ingestCalls.Load())
	}
}

func TestClearCommandRoutedAgentCallsContextManagerClear(t *testing.T) {
	cleanup := resetCMRegistry()
	defer cleanup()

	mock := &trackingContextManager{}
	factory := func(cfg json.RawMessage, al *AgentLoop) (ContextManager, error) {
		return mock, nil
	}
	if err := RegisterContextManager("clear_track_cm", factory); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	workspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         filepath.Join(workspace, "default"),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ContextManager:    "clear_track_cm",
			},
			List: []config.AgentConfig{
				{
					ID:        "main",
					Default:   true,
					Workspace: filepath.Join(workspace, "main"),
				},
				{
					ID:        "support",
					Workspace: filepath.Join(workspace, "support"),
				},
			},
			Dispatch: &config.DispatchConfig{
				Rules: []config.DispatchRule{
					{
						Name:  "support-dingtalk",
						Agent: "support",
						When: config.DispatchSelector{
							Channel: "dingtalk",
						},
					},
				},
			},
		},
		Session: config.SessionConfig{
			Dimensions: []string{"chat"},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &simpleMockProvider{response: "done"})
	if al.contextManager != mock {
		t.Fatalf("expected mock context manager, got %T", al.contextManager)
	}

	msg := testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "dingtalk",
			ChatID:   "chat1",
			ChatType: "direct",
			SenderID: "user1",
		},
		Content: "/clear",
	})
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	sessionKey := al.allocateRouteSession(route, msg).SessionKey

	if _, err := al.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	if got := mock.clearCalls.Load(); got != 1 {
		t.Fatalf("Clear calls = %d, want 1", got)
	}
	mock.mu.Lock()
	gotKey := mock.lastClearKey
	mock.mu.Unlock()
	if gotKey != sessionKey {
		t.Fatalf("Clear session key = %q, want %q", gotKey, sessionKey)
	}
}

// ---------------------------------------------------------------------------
// forceCompression edge cases (via legacy Compact)
// ---------------------------------------------------------------------------

func TestLegacyCompact_Overflow_SingleTurnKeepsLastUserMessage(t *testing.T) {
	cfg := testConfig(t)
	al := newCMTestAgentLoop(cfg)

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("expected default agent")
	}

	// History with only 2 messages — forceCompression should still handle it
	history := []providers.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: "first answer"},
	}
	if err := defaultAgent.Sessions.SetHistory(context.Background(), "session-2msg", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-2msg",
		Reason:     ContextCompressReasonRetry,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	newHistory, err := defaultAgent.Sessions.GetHistory(context.Background(), "session-2msg")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	// With 2 messages, forceCompression returns false (len <= 2), so no compression
	if len(newHistory) != len(history) {
		t.Fatalf("expected no compression for 2-message history, got %d", len(newHistory))
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// noopContextManager is a minimal ContextManager that does nothing.
type noopContextManager struct{}

func (m *noopContextManager) Assemble(_ context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	return &AssembleResponse{}, nil
}
func (m *noopContextManager) Compact(_ context.Context, _ *CompactRequest) error { return nil }
func (m *noopContextManager) Ingest(_ context.Context, _ *IngestRequest) error   { return nil }
func (m *noopContextManager) Clear(_ context.Context, _ string) error            { return nil }

// trackingContextManager tracks call counts for each method.
type trackingContextManager struct {
	assembleCalls atomic.Int64
	compactCalls  atomic.Int64
	ingestCalls   atomic.Int64
	clearCalls    atomic.Int64
	mu            sync.Mutex
	lastAssemble  *AssembleRequest
	lastCompact   *CompactRequest
	lastIngest    *IngestRequest
	lastClearKey  string
}

func (m *trackingContextManager) Assemble(_ context.Context, req *AssembleRequest) (*AssembleResponse, error) {
	m.assembleCalls.Add(1)
	m.mu.Lock()
	m.lastAssemble = req
	m.mu.Unlock()
	return &AssembleResponse{}, nil
}

func (m *trackingContextManager) Compact(_ context.Context, req *CompactRequest) error {
	m.compactCalls.Add(1)
	m.mu.Lock()
	m.lastCompact = req
	m.mu.Unlock()
	return nil
}

func (m *trackingContextManager) Ingest(_ context.Context, req *IngestRequest) error {
	m.ingestCalls.Add(1)
	m.mu.Lock()
	m.lastIngest = req
	m.mu.Unlock()
	return nil
}

func (m *trackingContextManager) Clear(_ context.Context, sessionKey string) error {
	m.clearCalls.Add(1)
	m.mu.Lock()
	m.lastClearKey = sessionKey
	m.mu.Unlock()
	return nil
}

// resetCMRegistry clears the global factory registry and returns a cleanup
// function that restores the original state after the test.
func resetCMRegistry() func() {
	cmRegistryMu.Lock()
	original := make(map[string]ContextManagerFactory, len(cmRegistry))
	for k, v := range cmRegistry {
		original[k] = v
	}
	cmRegistry = make(map[string]ContextManagerFactory)
	cmRegistryMu.Unlock()

	return func() {
		cmRegistryMu.Lock()
		cmRegistry = original
		cmRegistryMu.Unlock()
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
}

func newCMTestAgentLoop(cfg *config.Config) *AgentLoop {
	msgBus := bus.NewMessageBus()
	return NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "test"})
}

func TestContextFeedback_PublishedWhenToolFeedbackEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agents.Defaults.ToolFeedback = config.ToolFeedbackConfig{Enabled: true}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "test"})
	agent := al.registry.GetDefaultAgent()
	ts := &turnState{agent: agent, sessionKey: "sk-feedback", channel: "telegram", chatID: "chat-1"}

	al.notifyContextFeedback(ts, "notice")

	select {
	case out := <-msgBus.OutboundChan():
		if out.Channel != "telegram" || out.ChatID != "chat-1" {
			t.Fatalf("unexpected target: %+v", out)
		}
		if out.Context.Raw[metadataKeyMessageKind] != messageKindToolFeedback {
			t.Fatalf("kind = %q, want %q", out.Context.Raw[metadataKeyMessageKind], messageKindToolFeedback)
		}
		if out.Context.Raw[channels.ToolFeedbackTerminalKey] != "true" {
			t.Fatalf(
				"terminal flag = %q, want true: context notices must not seed reusable progress cards",
				out.Context.Raw[channels.ToolFeedbackTerminalKey],
			)
		}
		if out.Content != "notice" {
			t.Fatalf("content = %q, want %q", out.Content, "notice")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected context feedback outbound")
	}
}

func TestContextFeedback_PublishedOnPicoChannel(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agents.Defaults.ToolFeedback = config.ToolFeedbackConfig{Enabled: true}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "test"})
	agent := al.registry.GetDefaultAgent()
	ts := &turnState{agent: agent, sessionKey: "sk-feedback-pico", channel: "pico", chatID: "chat-pico"}

	al.notifyContextFeedback(ts, "notice")

	select {
	case out := <-msgBus.OutboundChan():
		if out.Channel != "pico" || out.ChatID != "chat-pico" {
			t.Fatalf("unexpected target: %+v", out)
		}
		if out.Context.Raw[metadataKeyMessageKind] != messageKindToolFeedback {
			t.Fatalf("kind = %q, want %q", out.Context.Raw[metadataKeyMessageKind], messageKindToolFeedback)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected context feedback outbound on pico")
	}
}

func TestContextFeedback_SuppressedWhenToolFeedbackDisabled(t *testing.T) {
	cfg := testConfig(t)
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "test"})
	agent := al.registry.GetDefaultAgent()
	ts := &turnState{agent: agent, sessionKey: "sk-feedback-off", channel: "telegram", chatID: "chat-1"}

	al.notifyContextFeedback(ts, "notice")

	select {
	case out := <-msgBus.OutboundChan():
		t.Fatalf("unexpected outbound when tool feedback disabled: %+v", out)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestLegacyCompact_PublishesFeedbackWhenToolFeedbackEnabled(t *testing.T) {
	cfg := testConfig(t)
	cfg.Agents.Defaults.ToolFeedback = config.ToolFeedbackConfig{Enabled: true}
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus, &simpleMockProvider{response: "test"})
	agent := al.registry.GetDefaultAgent()

	history := []providers.Message{
		{Role: "user", Content: "msg 1"},
		{Role: "assistant", Content: "resp 1"},
		{Role: "user", Content: "msg 2"},
		{Role: "assistant", Content: "resp 2"},
		{Role: "user", Content: "msg 3"},
	}
	if err := agent.Sessions.SetHistory(context.Background(), "session-feedback", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	ts := &turnState{
		agent:      agent,
		sessionKey: "session-feedback",
		channel:    "telegram",
		chatID:     "chat-1",
	}
	al.activeTurnStates.Store("session-feedback", ts)
	defer al.activeTurnStates.Delete("session-feedback")

	if err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-feedback",
		Reason:     ContextCompressReasonRetry,
	}); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	select {
	case out := <-msgBus.OutboundChan():
		if out.Channel != "telegram" || out.ChatID != "chat-1" {
			t.Fatalf("unexpected target: %+v", out)
		}
		if out.Context.Raw[metadataKeyMessageKind] != messageKindToolFeedback {
			t.Fatalf("kind = %q, want %q", out.Context.Raw[metadataKeyMessageKind], messageKindToolFeedback)
		}
		if !strings.Contains(out.Content, contextFeedbackToolCompact) {
			t.Fatalf("content = %q, want compaction notice", out.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected compaction feedback outbound")
	}
}

// ---------------------------------------------------------------------------
// Legacy manual compaction (/compact)
// ---------------------------------------------------------------------------

// promptRecordingProvider captures the prompts it is asked to summarize.
type promptRecordingProvider struct {
	mu      sync.Mutex
	prompts []string
}

func (p *promptRecordingProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, m := range messages {
		p.prompts = append(p.prompts, m.Content)
	}
	return &providers.LLMResponse{Content: "manual summary"}, nil
}

func (p *promptRecordingProvider) GetDefaultModel() string { return "mock-model" }

func newManualCompactLoop(t *testing.T) (*AgentLoop, *AgentInstance, *promptRecordingProvider) {
	t.Helper()
	provider := &promptRecordingProvider{}
	al := NewAgentLoop(testConfig(t), bus.NewMessageBus(), provider)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	return al, agent, provider
}

func TestLegacyCompact_Manual_AppliesFocus(t *testing.T) {
	al, agent, provider := newManualCompactLoop(t)
	history := []providers.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}
	if err := agent.Sessions.SetHistory(context.Background(), "session-manual", history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey:   "session-manual",
		Reason:       ContextCompressReasonManual,
		Instructions: "database migration",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manualSummary, err := agent.Sessions.GetSummary(context.Background(), "session-manual")
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if manualSummary != "manual summary" {
		t.Fatalf("summary = %q, want %q", manualSummary, "manual summary")
	}
	manualHistory, err := agent.Sessions.GetHistory(context.Background(), "session-manual")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(manualHistory) >= len(history) {
		t.Fatalf("history len = %d, want fewer than %d", len(manualHistory), len(history))
	}
	if len(provider.prompts) == 0 || !strings.Contains(provider.prompts[0], "database migration") {
		t.Fatalf("summarizer prompt missing focus: %q", provider.prompts)
	}
}

func TestLegacyCompact_Manual_NothingToCompact(t *testing.T) {
	al, agent, provider := newManualCompactLoop(t)
	if err := agent.Sessions.SetHistory(context.Background(), "session-short", []providers.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
	}); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}

	err := al.contextManager.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-short",
		Reason:     ContextCompressReasonManual,
	})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
	if len(provider.prompts) != 0 {
		t.Fatalf("summarizer must not run for short history, got %d calls", len(provider.prompts))
	}
	shortHistory, err := agent.Sessions.GetHistory(context.Background(), "session-short")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(shortHistory) != 2 {
		t.Fatalf("history len = %d, want 2 (untouched)", len(shortHistory))
	}
}

func TestLegacyCompact_Manual_RejectsConcurrentSummarization(t *testing.T) {
	al, agent, _ := newManualCompactLoop(t)
	lcm := al.contextManager.(*legacyContextManager)
	lcm.summarizing.Store(agent.ID+":session-busy", true)

	err := lcm.Compact(context.Background(), &CompactRequest{
		SessionKey: "session-busy",
		Reason:     ContextCompressReasonManual,
	})
	if err == nil || errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want in-progress error", err)
	}
}

func TestProcessMessage_CompactCommand(t *testing.T) {
	al, agent, provider := newManualCompactLoop(t)
	msg := testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{Channel: "telegram", ChatID: "chat1", ChatType: "direct", SenderID: "user1"},
		Content: "/compact focus on database migration",
	})
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute() error = %v", err)
	}
	sessionKey := al.allocateRouteSession(route, msg).SessionKey

	// Short history: nothing to compact, LLM untouched.
	if err = agent.Sessions.SetHistory(context.Background(), sessionKey,
		[]providers.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	resp, err := al.processMessage(context.Background(), msg)
	if err != nil || resp != "No compactable history was found." {
		t.Fatalf("short history: resp=%q err=%v", resp, err)
	}
	if len(provider.prompts) != 0 {
		t.Fatalf("LLM must not be called, got %d calls", len(provider.prompts))
	}

	// Long history: compacted with the focus forwarded to the summarizer.
	history := []providers.Message{
		{Role: "user", Content: "q1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "q2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "q3"},
		{Role: "assistant", Content: "a3"},
	}
	if err = agent.Sessions.SetHistory(context.Background(), sessionKey, history); err != nil {
		t.Fatalf("SetHistory: %v", err)
	}
	resp, err = al.processMessage(context.Background(), msg)
	if err != nil || !strings.HasPrefix(resp, "Context compacted") {
		t.Fatalf("long history: resp=%q err=%v", resp, err)
	}
	if len(provider.prompts) == 0 || !strings.Contains(provider.prompts[0], "focus on database migration") {
		t.Fatalf("summarizer prompt missing focus: %q", provider.prompts)
	}
}
