package gateway

import (
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

type gatewayEventPayload struct {
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

func publishGatewayEvent(
	al *agent.AgentLoop,
	kind runtimeevents.Kind,
	startedAt time.Time,
	err error,
) {
	if al == nil || al.RuntimeEventBus() == nil {
		return
	}

	severity := runtimeevents.SeverityInfo
	payload := gatewayEventPayload{}
	if !startedAt.IsZero() {
		payload.DurationMS = time.Since(startedAt).Milliseconds()
	}
	if err != nil {
		severity = runtimeevents.SeverityError
		payload.Error = err.Error()
	}

	al.RuntimeEventBus().PublishNonBlocking(runtimeevents.Event{
		Kind:     kind,
		Source:   runtimeevents.Source{Component: "gateway"},
		Severity: severity,
		Payload:  payload,
		Attrs:    gatewayEventAttrs(payload),
	})
}

func gatewayEventAttrs(payload gatewayEventPayload) map[string]any {
	attrs := map[string]any{}
	if payload.DurationMS > 0 {
		attrs["duration_ms"] = payload.DurationMS
	}
	if payload.Error != "" {
		attrs["error"] = payload.Error
	}
	return attrs
}

type cronEventPayload struct {
	Reason      string   `json:"reason,omitempty"`
	Enabled     bool     `json:"enabled"`
	Created     int      `json:"created,omitempty"`
	Updated     int      `json:"updated,omitempty"`
	Unchanged   int      `json:"unchanged,omitempty"`
	Removed     int      `json:"removed,omitempty"`
	RemovedKeys []string `json:"removed_keys,omitempty"`
}

func publishCronEvent(al *agent.AgentLoop, kind runtimeevents.Kind, payload cronEventPayload) {
	if al == nil || al.RuntimeEventBus() == nil {
		return
	}
	attrs := map[string]any{
		"reason":  payload.Reason,
		"enabled": payload.Enabled,
	}
	if payload.Created > 0 {
		attrs["created"] = payload.Created
	}
	if payload.Updated > 0 {
		attrs["updated"] = payload.Updated
	}
	if payload.Removed > 0 {
		attrs["removed"] = payload.Removed
	}
	agents := []string{""}
	if registry := al.GetRegistry(); registry != nil {
		if ids := registry.ListAgentIDs(); len(ids) > 0 {
			agents = ids
		}
	}
	for _, agentID := range agents {
		al.RuntimeEventBus().PublishNonBlocking(runtimeevents.Event{
			Kind:     kind,
			Source:   runtimeevents.Source{Component: "gateway"},
			Scope:    runtimeevents.Scope{AgentID: agentID},
			Severity: runtimeevents.SeverityInfo,
			Payload:  payload,
			Attrs:    attrs,
		})
	}
}
