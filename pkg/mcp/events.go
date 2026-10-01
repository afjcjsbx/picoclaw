package mcp

import (
	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

func (m *Manager) publishServerEvent(
	kind runtimeevents.Kind,
	serverName string,
	cfg config.MCPServerConfig,
	toolCount int,
	err error,
) {
	if m == nil || m.runtimeEvents == nil {
		return
	}

	severity := runtimeevents.SeverityInfo
	if err != nil {
		severity = runtimeevents.SeverityError
	}
	redactor := newServerRedactor(cfg)
	safeCfg := RedactServerConfig(cfg)
	payload := ServerEventPayload{
		Server:    redactor.Redact(serverName),
		Type:      mcpTransportType(cfg),
		URL:       safeCfg.URL,
		Command:   redactor.Redact(cfg.Command),
		ToolCount: toolCount,
	}
	if err != nil {
		payload.Error = redactor.Redact(err.Error())
	}

	m.runtimeEvents.PublishNonBlocking(runtimeevents.Event{
		Kind:     kind,
		Source:   runtimeevents.Source{Component: "mcp", Name: payload.Server},
		Severity: severity,
		Payload:  payload,
		Attrs:    mcpServerEventAttrs(payload),
	})
}

func (m *Manager) publishToolDiscovered(serverName string, cfg config.MCPServerConfig, toolName string) {
	if m == nil || m.runtimeEvents == nil {
		return
	}
	redactor := newServerRedactor(cfg)
	safeCfg := RedactServerConfig(cfg)
	payload := ServerEventPayload{
		Server:  redactor.Redact(serverName),
		Type:    mcpTransportType(cfg),
		URL:     safeCfg.URL,
		Command: redactor.Redact(cfg.Command),
		Tool:    redactor.Redact(toolName),
	}
	m.runtimeEvents.PublishNonBlocking(runtimeevents.Event{
		Kind:     runtimeevents.KindMCPToolDiscovered,
		Source:   runtimeevents.Source{Component: "mcp", Name: payload.Server},
		Severity: runtimeevents.SeverityInfo,
		Payload:  payload,
		Attrs:    mcpServerEventAttrs(payload),
	})
}

func mcpServerEventAttrs(payload ServerEventPayload) map[string]any {
	attrs := map[string]any{}
	setMCPAttrString(attrs, "server", payload.Server)
	setMCPAttrString(attrs, "type", payload.Type)
	setMCPAttrString(attrs, "tool", payload.Tool)
	if payload.ToolCount > 0 {
		attrs["tool_count"] = payload.ToolCount
	}
	setMCPAttrString(attrs, "error", payload.Error)
	return attrs
}

func setMCPAttrString(attrs map[string]any, key, value string) {
	if value != "" {
		attrs[key] = value
	}
}

func mcpTransportType(cfg config.MCPServerConfig) string {
	return config.EffectiveMCPTransportType(cfg)
}
