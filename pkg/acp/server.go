package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

func (r *Runtime) registerHandlers() {
	r.peer.Register("initialize", r.handleInitialize)
	r.peer.Register("session/new", r.handleNewSession)
	r.peer.Register("session/load", r.handleLoadSession)
	r.peer.Register("session/prompt", r.handlePrompt)
	r.peer.Register("session/set_config_option", r.handleSetConfigOption)
	r.peer.Register("session/set_mode", r.handleSetMode)
	r.peer.OnNotification("session/cancel", r.handleCancel)
}

func (r *Runtime) handleInitialize(_ context.Context, params json.RawMessage) (any, error) {
	var request InitializeRequest
	if err := decodeParams(params, &request); err != nil {
		return nil, err
	}
	if request.ProtocolVersion <= 0 {
		return nil, invalidParams("protocolVersion is required")
	}

	r.initializedMu.Lock()
	if r.initialized {
		r.initializedMu.Unlock()
		return nil, &RPCError{Code: CodeInvalidRequest, Message: "already initialized"}
	}
	r.initialized = true
	r.initializedMu.Unlock()

	version := r.version
	if version == "" {
		version = "dev"
	}
	return InitializeResponse{
		ProtocolVersion: ProtocolVersion,
		AgentInfo:       &Implementation{Name: "picoclaw", Version: version},
		AuthMethods:     []AuthMethod{},
		AgentCapabilities: AgentCapabilities{
			LoadSession: true,
			PromptCapabilities: PromptCapabilities{
				EmbeddedContext: true,
			},
			MCPCapabilities:     MCPCapabilities{},
			SessionCapabilities: SessionCapabilities{},
		},
	}, nil
}

func (r *Runtime) handleNewSession(_ context.Context, params json.RawMessage) (any, error) {
	if err := r.requireInitialized(); err != nil {
		return nil, err
	}
	var request NewSessionRequest
	if err := decodeParams(params, &request); err != nil {
		return nil, err
	}
	cwd, err := filepath.Abs(request.CWD)
	if err != nil {
		return nil, invalidParams("cwd must be an absolute path")
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, invalidParams("cwd does not exist")
	}
	workspace, err := filepath.EvalSymlinks(r.workspace)
	if err != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: "workspace does not exist"}
	}
	if cwd != workspace {
		return nil, invalidParams("cwd must match the ACP process workspace")
	}
	if len(request.AdditionalDirectories) > 0 {
		return nil, invalidParams("additionalDirectories are not supported yet")
	}
	if request.SystemPrompt != nil {
		return nil, invalidParams("systemPrompt is not supported yet")
	}
	if len(request.MCPServers) > 0 {
		return nil, invalidParams("session-scoped MCP servers are not supported yet")
	}

	id := r.newSessionID()
	state := &acpSession{
		id:    id,
		key:   r.sessionKey(id),
		model: r.initialModel(),
		mode:  "build",
	}
	if err := r.addSession(state); err != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: err.Error()}
	}
	if err := r.notifyCommands(state); err != nil {
		r.removeSession(state)
		return nil, err
	}
	return NewSessionResponse{
		SessionID:     id,
		Modes:         r.modeState(state),
		ConfigOptions: r.configOptions(state),
	}, nil
}

func (r *Runtime) handleLoadSession(ctx context.Context, params json.RawMessage) (any, error) {
	if err := r.requireInitialized(); err != nil {
		return nil, err
	}
	var request LoadSessionRequest
	if err := decodeParams(params, &request); err != nil {
		return nil, err
	}
	cwd, err := filepath.Abs(request.CWD)
	if err != nil {
		return nil, invalidParams("cwd must be an absolute path")
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, invalidParams("cwd does not exist")
	}
	workspace, err := filepath.EvalSymlinks(r.workspace)
	if err != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: "workspace does not exist"}
	}
	if cwd != workspace {
		return nil, invalidParams("cwd must match the ACP process workspace")
	}
	if len(request.AdditionalDirectories) > 0 {
		return nil, invalidParams("additionalDirectories are not supported yet")
	}
	if request.SystemPrompt != nil {
		return nil, invalidParams("systemPrompt is not supported yet")
	}
	if len(request.MCPServers) > 0 {
		return nil, invalidParams("session-scoped MCP servers are not supported yet")
	}
	if existing := r.sessionByID(request.SessionID); existing != nil && existing.activeTurn() != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: "session has an active turn"}
	}

	key := r.sessionKey(request.SessionID)
	agentInstance := r.agentLoop.GetRegistry().GetDefaultAgent()
	if agentInstance == nil || agentInstance.Sessions == nil {
		return nil, &RPCError{Code: CodeAgentError, Message: "session store is unavailable"}
	}
	if !containsSessionKey(agentInstance.Sessions.ListSessions(), key) {
		return nil, &RPCError{Code: CodeSessionNotFound, Message: "unknown session"}
	}
	history, err := agentInstance.Sessions.GetHistory(ctx, key)
	if err != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: "read session history"}
	}
	state := &acpSession{
		id:    request.SessionID,
		key:   key,
		model: r.initialModel(),
		mode:  "build",
	}
	if err := r.addSession(state); err != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: err.Error()}
	}
	if err := r.replaySession(ctx, state, history); err != nil {
		r.removeSession(state)
		return nil, err
	}
	if err := r.notifyCommands(state); err != nil {
		r.removeSession(state)
		return nil, err
	}
	return LoadSessionResponse{
		Modes:         r.modeState(state),
		ConfigOptions: r.configOptions(state),
	}, nil
}

func (r *Runtime) handlePrompt(ctx context.Context, params json.RawMessage) (any, error) {
	if err := r.requireInitialized(); err != nil {
		return nil, err
	}
	var request PromptRequest
	if err := decodeParams(params, &request); err != nil {
		return nil, err
	}
	state := r.sessionByID(request.SessionID)
	if state == nil {
		return nil, &RPCError{Code: CodeSessionNotFound, Message: "unknown session"}
	}
	content, err := promptText(request.Prompt)
	if err != nil {
		return nil, invalidParams(err.Error())
	}

	turn := newACPTurn(r.newSessionID(), state)
	if err := state.setActive(turn); err != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: err.Error()}
	}
	r.channel.SetActiveTurn(state.id, turn.id)
	defer r.channel.ClearActiveTurn(state.id, turn.id)
	if err := r.publishInbound(ctx, state, turn, content); err != nil {
		turn.finish("end_turn", err)
		return nil, &RPCError{Code: CodeAgentError, Message: err.Error()}
	}

	stopReason, turnErr := turn.result()
	if turnErr != nil {
		return nil, &RPCError{Code: CodeAgentError, Message: turnErr.Error()}
	}
	return PromptResponse{StopReason: stopReason}, nil
}

func (r *Runtime) handleCancel(ctx context.Context, params json.RawMessage) {
	var request CancelNotification
	if err := decodeParams(params, &request); err != nil {
		return
	}
	state := r.sessionByID(request.SessionID)
	if state == nil {
		return
	}
	turn := state.activeTurn()
	if turn == nil {
		return
	}
	turn.canceled.Store(true)
	r.cancelPermissions(state.id)
	state.mu.Lock()
	state.canceledTurnID = turn.id
	state.mu.Unlock()
	if err := r.agentLoop.CancelSession(state.key); err != nil {
		logger.DebugCF("acp", "Hard abort was not immediate", map[string]any{
			"session_id": state.id,
			"error":      err.Error(),
		})
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !r.agentLoop.SessionIdle(state.key) {
		select {
		case <-ctx.Done():
			turn.finish(StopReasonCancelled, nil)
			return
		case <-ticker.C:
		case <-timer.C:
			logger.WarnCF("acp", "Session cancellation timed out", map[string]any{"session_id": state.id})
			turn.finish(StopReasonCancelled, nil)
			return
		}
	}
	turn.finish(StopReasonCancelled, nil)
}

func (r *Runtime) handleSetConfigOption(_ context.Context, params json.RawMessage) (any, error) {
	var request SetSessionConfigOptionRequest
	if err := decodeParams(params, &request); err != nil {
		return nil, err
	}
	state := r.sessionByID(request.SessionID)
	if state == nil {
		return nil, &RPCError{Code: CodeSessionNotFound, Message: "unknown session"}
	}
	if err := r.setConfigOption(state, request.ConfigID, request.Value); err != nil {
		return nil, invalidParams(err.Error())
	}
	return SetSessionConfigOptionResponse{ConfigOptions: r.configOptions(state)}, nil
}

func (r *Runtime) handleSetMode(_ context.Context, params json.RawMessage) (any, error) {
	var request SetSessionModeRequest
	if err := decodeParams(params, &request); err != nil {
		return nil, err
	}
	state := r.sessionByID(request.SessionID)
	if state == nil {
		return nil, &RPCError{Code: CodeSessionNotFound, Message: "unknown session"}
	}
	if err := r.setConfigOption(state, "mode", request.ModeID); err != nil {
		return nil, invalidParams(err.Error())
	}
	return SetSessionModeResponse{}, nil
}

func (r *Runtime) requireInitialized() error {
	r.initializedMu.RLock()
	defer r.initializedMu.RUnlock()
	if !r.initialized {
		return &RPCError{Code: CodeAgentError, Message: "initialize must be called first"}
	}
	return nil
}

func (r *Runtime) initialModel() string {
	models := r.modelOptions()
	for _, model := range models {
		if model.Name == r.defaultModel {
			return model.Name
		}
	}
	if len(models) > 0 {
		return models[0].Name
	}
	return r.defaultModel
}

func (r *Runtime) removeSession(state *acpSession) {
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	delete(r.sessions, state.id)
	delete(r.byKey, state.key)
}

func containsSessionKey(keys []string, key string) bool {
	for _, candidate := range keys {
		if candidate == key {
			return true
		}
	}
	return false
}

func decodeParams(params json.RawMessage, target any) error {
	if len(params) == 0 || strings.TrimSpace(string(params)) == "null" {
		return invalidParams("params are required")
	}
	if err := json.Unmarshal(params, target); err != nil {
		return invalidParams(err.Error())
	}
	return nil
}

func invalidParams(message string) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: message}
}

func promptText(blocks []ContentBlock) (string, error) {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "resource_link":
			if block.URI == "" {
				return "", fmt.Errorf("resource_link requires uri")
			}
			label := block.Name
			if label == "" {
				label = block.URI
			}
			parts = append(parts, label+": "+block.URI)
		case "resource":
			if block.Resource == nil {
				return "", fmt.Errorf("resource content is required")
			}
			if block.Resource.Text != "" {
				parts = append(parts, block.Resource.Text)
			} else if block.Resource.Blob != "" {
				return "", fmt.Errorf("binary embedded resources are not supported yet")
			}
		default:
			return "", fmt.Errorf("unsupported content type %q", block.Type)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}
