package acp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestRuntimePromptLifecycle(t *testing.T) {
	client, workspace, updates, _ := newTestRuntime(t, &scriptedProvider{
		responses: []*providers.LLMResponse{{Content: "hello from ACP", FinishReason: "stop"}},
	})
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)

	var response PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("hi")},
	}, &response); err != nil {
		t.Fatalf("session/prompt error = %v", err)
	}
	if response.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", response.StopReason)
	}
	waitForUpdate(t, updates, "agent_message_chunk", "hello from ACP")
}

func TestRuntimeTokenLimitMapsToMaxTokens(t *testing.T) {
	client, workspace, _, _ := newTestRuntime(t, &scriptedProvider{
		responses: []*providers.LLMResponse{
			{Content: "partial", FinishReason: "length"},
			{Content: "partial", FinishReason: "length"},
			{Content: "partial", FinishReason: "length"},
		},
	})
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)

	var response PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("long answer")},
	}, &response); err != nil {
		t.Fatalf("session/prompt error = %v", err)
	}
	if response.StopReason != "max_tokens" {
		t.Fatalf("stop reason = %q, want max_tokens", response.StopReason)
	}
}

func TestRuntimeLoadSessionReplaysHistory(t *testing.T) {
	client, workspace, _, _ := newTestRuntime(t, &scriptedProvider{
		responses: []*providers.LLMResponse{{Content: "persisted answer", FinishReason: "stop"}},
	})
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)
	var promptResponse PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("persist this question")},
	}, &promptResponse); err != nil {
		t.Fatalf("session/prompt error = %v", err)
	}

	replayed := make(chan map[string]any, 32)
	client.OnNotification("session/update", func(_ context.Context, params json.RawMessage) {
		var notification SessionNotification
		if json.Unmarshal(params, &notification) == nil {
			replayed <- notification.Update
		}
	})
	var response LoadSessionResponse
	if err := client.Call(t.Context(), "session/load", LoadSessionRequest{
		SessionID:  sessionID,
		CWD:        workspace,
		MCPServers: []MCPServer{},
	}, &response); err != nil {
		t.Fatalf("session/load error = %v", err)
	}
	waitForUpdate(t, replayed, "user_message_chunk", "persist this question")
	waitForUpdate(t, replayed, "agent_message_chunk", "persisted answer")
}

func TestRuntimeToolCallUpdates(t *testing.T) {
	client, workspace, updates, _ := newTestRuntime(t, &scriptedProvider{
		responses: []*providers.LLMResponse{
			{
				FinishReason: "tool_calls",
				ToolCalls: []providers.ToolCall{{
					ID:        "call-read-1",
					Type:      "function",
					Name:      "read_file",
					Arguments: map[string]any{"path": "note.txt"},
				}},
			},
			{Content: "file read", FinishReason: "stop"},
		},
	})
	writeWorkspaceFile(t, workspace, "note.txt", "ACP file contents")
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)

	var response PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("read the note")},
	}, &response); err != nil {
		t.Fatalf("session/prompt error = %v", err)
	}
	if response.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", response.StopReason)
	}
	waitForUpdate(t, updates, "tool_call", "call-read-1")
	waitForUpdate(t, updates, "tool_call_update", "call-read-1")
}

func TestRuntimePlanModeDeniesMutation(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*providers.LLMResponse{
			{
				FinishReason: "tool_calls",
				ToolCalls: []providers.ToolCall{{
					ID:        "call-write-1",
					Type:      "function",
					Name:      "write_file",
					Arguments: map[string]any{"path": "blocked.txt", "content": "should not exist"},
				}},
			},
			{Content: "plan complete", FinishReason: "stop"},
		},
	}
	client, workspace, updates, _ := newTestRuntime(t, provider)
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)
	if err := client.Call(t.Context(), "session/set_mode", SetSessionModeRequest{
		SessionID: sessionID,
		ModeID:    "plan",
	}, nil); err != nil {
		t.Fatalf("session/set_mode error = %v", err)
	}

	var response PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("write a file")},
	}, &response); err != nil {
		t.Fatalf("session/prompt error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("plan mode created blocked.txt: %v", err)
	}
	waitForUpdate(t, updates, "tool_call_update", "failed")
}

func TestRuntimeCancelReturnsCancelled(t *testing.T) {
	provider := &scriptedProvider{block: true, blockFirst: true, started: make(chan struct{})}
	client, workspace, _, _ := newTestRuntime(t, provider)
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)

	result := make(chan PromptResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		var response PromptResponse
		err := client.Call(t.Context(), "session/prompt", PromptRequest{
			SessionID: sessionID,
			Prompt:    []ContentBlock{TextContent("block")},
		}, &response)
		result <- response
		errCh <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	if err := client.Notify("session/cancel", CancelNotification{SessionID: sessionID}); err != nil {
		t.Fatalf("session/cancel error = %v", err)
	}
	select {
	case response := <-result:
		if err := <-errCh; err != nil {
			t.Fatalf("session/prompt error = %v", err)
		}
		if response.StopReason != StopReasonCancelled {
			t.Fatalf("stop reason = %q, want %s", response.StopReason, StopReasonCancelled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled prompt did not return")
	}

	var next PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("continue")},
	}, &next); err != nil {
		t.Fatalf("next session/prompt error = %v", err)
	}
	if next.StopReason != "end_turn" {
		t.Fatalf("next stop reason = %q, want end_turn", next.StopReason)
	}
}

func TestRuntimePermissionRoundTrip(t *testing.T) {
	provider := &scriptedProvider{
		responses: []*providers.LLMResponse{
			{
				FinishReason: "tool_calls",
				ToolCalls: []providers.ToolCall{{
					ID:        "call-exec-1",
					Type:      "function",
					Name:      "exec",
					Arguments: map[string]any{"action": "run", "command": "picoclaw-acp-unknown --version"},
				}},
			},
			{Content: "command attempt finished", FinishReason: "stop"},
		},
	}
	client, workspace, _, permissions := newTestRuntime(t, provider)
	initializeClient(t, client)
	sessionID := newSession(t, client, workspace)

	var response PromptResponse
	if err := client.Call(t.Context(), "session/prompt", PromptRequest{
		SessionID: sessionID,
		Prompt:    []ContentBlock{TextContent("run an unknown command")},
	}, &response); err != nil {
		t.Fatalf("session/prompt error = %v", err)
	}
	if response.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q, want end_turn", response.StopReason)
	}
	select {
	case request := <-permissions:
		if request.SessionID != sessionID {
			t.Fatalf("permission session = %q, want %q", request.SessionID, sessionID)
		}
		if request.ToolCall.ToolCallID != "call-exec-1" {
			t.Fatalf("permission tool call = %q, want call-exec-1", request.ToolCall.ToolCallID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("permission request was not delivered")
	}
}

func TestRuntimeRejectsUnsupportedMCP(t *testing.T) {
	client, workspace, _, _ := newTestRuntime(t, &scriptedProvider{})
	initializeClient(t, client)
	err := client.Call(t.Context(), "session/new", NewSessionRequest{
		CWD:        workspace,
		MCPServers: []MCPServer{{Type: "stdio", Name: "test", Command: "/bin/echo", Args: []string{}}},
	}, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("session/new error = %v, want invalid params", err)
	}
}

func initializeClient(t *testing.T, client *Peer) {
	t.Helper()
	var response InitializeResponse
	if err := client.Call(t.Context(), "initialize", InitializeRequest{ProtocolVersion: 1}, &response); err != nil {
		t.Fatalf("initialize error = %v", err)
	}
	if response.ProtocolVersion != 1 {
		t.Fatalf("protocol version = %d, want 1", response.ProtocolVersion)
	}
}

func newSession(t *testing.T, client *Peer, workspace string) string {
	t.Helper()
	var response NewSessionResponse
	if err := client.Call(t.Context(), "session/new", NewSessionRequest{
		CWD:        workspace,
		MCPServers: []MCPServer{},
	}, &response); err != nil {
		t.Fatalf("session/new error = %v", err)
	}
	return response.SessionID
}

func waitForUpdate(t *testing.T, updates <-chan map[string]any, kind, contains string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	last := ""
	for {
		select {
		case update := <-updates:
			payload, _ := json.Marshal(update)
			last = string(payload)
			if update["sessionUpdate"] != kind {
				continue
			}
			if update["sessionUpdate"] == "agent_message_chunk" && !strings.Contains(last, contains) {
				t.Logf("unexpected agent message update: %s", last)
			}
			if !strings.Contains(last, contains) {
				continue
			}
			return
		case <-timer.C:
			t.Fatalf("did not receive %s update containing %q; last update: %s", kind, contains, last)
		}
	}
}

func newTestRuntime(
	t *testing.T,
	provider providers.LLMProvider,
) (*Peer, string, <-chan map[string]any, <-chan RequestPermissionRequest) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.ModelList = nil
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.ModelName = "fake-model"

	left, right := net.Pipe()
	client := NewPeer(left, left, defaultMaxMessageBytes)
	server := NewPeer(right, right, defaultMaxMessageBytes)
	updates := make(chan map[string]any, 64)
	permissions := make(chan RequestPermissionRequest, 8)
	client.OnNotification("session/update", func(_ context.Context, params json.RawMessage) {
		var notification SessionNotification
		if json.Unmarshal(params, &notification) == nil {
			updates <- notification.Update
		}
	})
	client.Register("session/request_permission", func(_ context.Context, params json.RawMessage) (any, error) {
		var request RequestPermissionRequest
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, err
		}
		permissions <- request
		return RequestPermissionResponse{Outcome: PermissionOutcome{Outcome: "selected", OptionID: "allow-once"}}, nil
	})

	runtime, err := NewRuntime(RuntimeOptions{
		Config:    cfg,
		Workspace: workspace,
		Version:   "test",
		Peer:      server,
		Provider:  provider,
		ModelID:   "fake-model",
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _ = client.Serve(ctx) }()
	go func() { done <- runtime.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		client.Close()
		server.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("ACP runtime did not shut down")
		}
	})
	return client, workspace, updates, permissions
}

type scriptedProvider struct {
	mu         sync.Mutex
	responses  []*providers.LLMResponse
	calls      int
	block      bool
	blockFirst bool
	started    chan struct{}
}

func (p *scriptedProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := p.calls
	p.calls++
	block := p.block && (!p.blockFirst || index == 0)
	started := p.started
	var response *providers.LLMResponse
	if index < len(p.responses) {
		response = p.responses[index]
	}
	p.mu.Unlock()
	if block {
		if started != nil {
			select {
			case <-started:
			default:
				close(started)
			}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if response == nil {
		return &providers.LLMResponse{Content: "done", FinishReason: "stop"}, nil
	}
	return response, nil
}

func (p *scriptedProvider) GetDefaultModel() string {
	return "fake-model"
}

func writeWorkspaceFile(t *testing.T, workspace, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
}
