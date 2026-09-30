package agent

import (
	"context"
	"slices"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

// pluginHook enforces per-agent and per-stage scope before sending any data to
// an external process. Global events are hidden from agent-restricted plugins.
type pluginHook struct {
	*ProcessHook
	agents []string
	stages []string
}

func (h *pluginHook) allowed(agent, stage string) bool {
	return (h.agents == nil || slices.Contains(h.agents, agent)) && slices.Contains(h.stages, stage)
}

func (h *pluginHook) OnRuntimeEvent(ctx context.Context, evt runtimeevents.Event) error {
	if h.agents != nil && !slices.Contains(h.agents, evt.Scope.AgentID) {
		return nil
	}
	return h.ProcessHook.OnRuntimeEvent(ctx, evt)
}

func (h *pluginHook) BeforeLLM(ctx context.Context, req *LLMHookRequest) (*LLMHookRequest, HookDecision, error) {
	if !h.allowed(req.Meta.AgentID, "before_llm") {
		return req, HookDecision{Action: HookActionContinue}, nil
	}
	return h.ProcessHook.BeforeLLM(ctx, req)
}

func (h *pluginHook) AfterLLM(ctx context.Context, req *LLMHookResponse) (*LLMHookResponse, HookDecision, error) {
	if !h.allowed(req.Meta.AgentID, "after_llm") {
		return req, HookDecision{Action: HookActionContinue}, nil
	}
	return h.ProcessHook.AfterLLM(ctx, req)
}

func (h *pluginHook) BeforeTool(
	ctx context.Context,
	req *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	if !h.allowed(req.Meta.AgentID, "before_tool") {
		return req, HookDecision{Action: HookActionContinue}, nil
	}
	return h.ProcessHook.BeforeTool(ctx, req)
}

func (h *pluginHook) AfterTool(
	ctx context.Context,
	req *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	if !h.allowed(req.Meta.AgentID, "after_tool") {
		return req, HookDecision{Action: HookActionContinue}, nil
	}
	return h.ProcessHook.AfterTool(ctx, req)
}

func (h *pluginHook) ApproveTool(ctx context.Context, req *ToolApprovalRequest) (ApprovalDecision, error) {
	if !h.allowed(req.Meta.AgentID, "approve_tool") {
		return ApprovalDecision{Approved: true}, nil
	}
	return h.ProcessHook.ApproveTool(ctx, req)
}
