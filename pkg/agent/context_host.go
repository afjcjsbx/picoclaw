package agent

import (
	"github.com/sipeed/picoclaw/pkg/agent/agentctx"
	"github.com/sipeed/picoclaw/pkg/agent/agentevents"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// contextHost exposes the AgentLoop to ContextManagers through the narrow
// agentctx.Host interface.
type contextHost struct{ al *AgentLoop }

var _ agentctx.Host = contextHost{}

func newAgentRef(a *AgentInstance) (agentctx.AgentRef, bool) {
	if a == nil {
		return agentctx.AgentRef{}, false
	}
	return agentctx.AgentRef{
		ID:                        a.ID,
		Model:                     a.Model,
		Workspace:                 a.Workspace,
		MaxTokens:                 a.MaxTokens,
		ContextWindow:             a.ContextWindow,
		SummarizeMessageThreshold: a.SummarizeMessageThreshold,
		SummarizeTokenPercent:     a.SummarizeTokenPercent,
		Provider:                  a.Provider,
		Sessions:                  a.Sessions,
	}, true
}

func (h contextHost) DefaultAgent() (agentctx.AgentRef, bool) {
	return newAgentRef(h.al.registry.GetDefaultAgent())
}

func (h contextHost) AgentForSession(sessionKey string) (agentctx.AgentRef, bool) {
	return newAgentRef(h.al.agentForSession(sessionKey))
}

func (h contextHost) RegisterTool(tool tools.Tool) { h.al.RegisterTool(tool) }

func (h contextHost) ActiveRequestsInc() { h.al.activeRequestsInc() }

func (h contextHost) ActiveRequestsDec() { h.al.activeRequestsDec() }

func (h contextHost) EmitContextCompress(sessionKey string, res agentctx.CompressResult) {
	h.al.emitEvent(
		runtimeevents.KindAgentContextCompress,
		h.al.newTurnEventScope("", sessionKey, nil).meta(0, "forceCompression", "turn.context.compress"),
		agentevents.ContextCompressPayload{
			Reason:            res.Reason,
			DroppedMessages:   res.DroppedMessages,
			RemainingMessages: res.RemainingMessages,
		},
	)
}

func (h contextHost) EmitSessionSummarize(agentID, sessionKey string, res agentctx.SummarizeResult) {
	h.al.emitEvent(
		runtimeevents.KindAgentSessionSummarize,
		h.al.newTurnEventScope(agentID, sessionKey, nil).meta(0, "summarizeSession", "turn.session.summarize"),
		agentevents.SessionSummarizePayload{
			SummarizedMessages: res.SummarizedMessages,
			KeptMessages:       res.KeptMessages,
			SummaryLen:         res.SummaryLen,
			OmittedOversized:   res.OmittedOversized,
		},
	)
}
