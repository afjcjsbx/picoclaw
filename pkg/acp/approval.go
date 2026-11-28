package acp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/agent"
)

var permissionOptions = []PermissionOption{
	{OptionID: "allow-once", Name: "Allow once", Kind: "allow_once"},
	{OptionID: "allow-always", Name: "Allow for this session", Kind: "allow_always"},
	{OptionID: "reject-once", Name: "Reject", Kind: "reject_once"},
	{OptionID: "reject-always", Name: "Reject for this session", Kind: "reject_always"},
}

func (r *Runtime) RequestApproval(ctx context.Context, req agent.ApprovalRequest) (agent.ApprovalDecision, error) {
	state := r.sessionByKey(req.SessionKey)
	if state == nil {
		return agent.ApprovalDecision{Reason: "unknown ACP session"}, nil
	}
	scope := permissionScope(req.Tool, req.Arguments, req.Command)
	if allowed, ok := state.rememberedPermission(scope); ok {
		if allowed {
			return agent.ApprovalDecision{Approved: true}, nil
		}
		return agent.ApprovalDecision{Reason: "rejected by remembered session permission"}, nil
	}

	toolCallID := req.ToolCallID
	if toolCallID == "" {
		toolCallID = uuid.NewString()
	}
	argsJSON, _ := json.Marshal(req.Arguments)
	title := req.Tool
	if req.Command != "" {
		title = req.Tool + ": " + boundedRedactedText(req.Command, 1024)
	}
	requestID := uuid.NewString()
	requestCtx, cancel := context.WithCancel(ctx)
	r.storePermissionCancel(state.id, requestID, cancel)
	defer func() {
		cancel()
		r.clearPermissionCancel(state.id, requestID)
	}()

	var response RequestPermissionResponse
	err := r.peer.Call(requestCtx, "session/request_permission", RequestPermissionRequest{
		SessionID: state.id,
		ToolCall: ToolCallUpdate{
			ToolCallID: toolCallID,
			Title:      title,
			Name:       req.Tool,
			Kind:       toolKind(req.Tool),
			Status:     "pending",
			RawInput:   sanitizedToolValue(string(argsJSON)),
		},
		Options: permissionOptions,
	}, &response)
	if err != nil || ctx.Err() != nil || requestCtx.Err() != nil {
		return agent.ApprovalDecision{Reason: "permission request failed or was canceled"}, nil
	}

	if response.Outcome.Outcome == PermissionOutcomeCancelled {
		return agent.ApprovalDecision{Reason: "permission request canceled"}, nil
	}
	if response.Outcome.Outcome != "selected" {
		return agent.ApprovalDecision{Reason: "unknown permission outcome"}, nil
	}
	switch response.Outcome.OptionID {
	case "allow-once":
		return agent.ApprovalDecision{Approved: true}, nil
	case "allow-always":
		state.rememberPermission(scope, true)
		return agent.ApprovalDecision{Approved: true}, nil
	case "reject-always":
		state.rememberPermission(scope, false)
		return agent.ApprovalDecision{Reason: "rejected for this session"}, nil
	case "reject-once":
		return agent.ApprovalDecision{Reason: "user rejected the operation"}, nil
	default:
		return agent.ApprovalDecision{Reason: "unknown permission option"}, nil
	}
}

func (r *Runtime) storePermissionCancel(sessionID, requestID string, cancel context.CancelFunc) {
	r.permissionsMu.Lock()
	defer r.permissionsMu.Unlock()
	if r.permissions[sessionID] == nil {
		r.permissions[sessionID] = make(map[string]context.CancelFunc)
	}
	r.permissions[sessionID][requestID] = cancel
}

func (r *Runtime) clearPermissionCancel(sessionID, requestID string) {
	r.permissionsMu.Lock()
	defer r.permissionsMu.Unlock()
	delete(r.permissions[sessionID], requestID)
	if len(r.permissions[sessionID]) == 0 {
		delete(r.permissions, sessionID)
	}
}

func (r *Runtime) cancelPermissions(sessionID string) {
	r.permissionsMu.Lock()
	pending := r.permissions[sessionID]
	delete(r.permissions, sessionID)
	cancels := make([]context.CancelFunc, 0, len(pending))
	for _, cancel := range pending {
		cancels = append(cancels, cancel)
	}
	r.permissionsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (r *Runtime) cancelAllPermissions() {
	r.permissionsMu.Lock()
	pending := r.permissions
	r.permissions = make(map[string]map[string]context.CancelFunc)
	cancels := make([]context.CancelFunc, 0)
	for _, requests := range pending {
		for _, cancel := range requests {
			cancels = append(cancels, cancel)
		}
	}
	r.permissionsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

var _ agent.ApprovalBroker = (*Runtime)(nil)
