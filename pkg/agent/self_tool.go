package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// RuntimeControl is the read/write boundary exposed to SelfTool.
type RuntimeControl interface {
	Snapshot(ctx context.Context) map[string]any
	// A nil value clears the selected session override.
	Set(ctx context.Context, key string, value any) error
}

// SelfTool exposes a small runtime snapshot and three session settings.
type SelfTool struct {
	runtime  RuntimeControl
	allowSet bool
}

func NewSelfTool(runtime RuntimeControl, allowSet bool) *SelfTool {
	return &SelfTool{runtime: runtime, allowSet: allowSet}
}

func (t *SelfTool) Name() string { return "self" }

func (t *SelfTool) Description() string {
	description := "Inspect this session's model, available model presets, iteration/context limits, tool names, " +
		"active turn and context_usage with action check (no key or value). " +
		"The budget describes the last LLM input, excluding its reply and subsequent tool results; " +
		"source is provider or local_estimate. Turn/budget are null when unavailable. " +
		"Use set with key and value to change max_iterations (integer 1–100), " +
		"context_window_tokens (integer 4096–1000000), or model_preset (a configured preset). " +
		"Limits apply immediately; model changes start next turn. Use value null to restore the configured default."
	if !t.allowSet {
		description += " READ-ONLY MODE: set is disabled."
	}
	return description
}

func (t *SelfTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string", "enum": []string{"check", "set"},
				"description": "check returns the complete snapshot; set changes one session setting.",
			},
			"key": map[string]any{
				"type": "string", "enum": []string{"max_iterations", "context_window_tokens", "model_preset"},
				"description": "Required only for set.",
			},
			"value": map[string]any{
				"description": "Required only for set: integer 1–100 for max_iterations, integer 4096–1000000 " +
					"for context_window_tokens, a configured model_preset name, or null to restore the configured default.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SelfTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	for name := range args {
		if name != "action" && name != "key" && name != "value" {
			return tools.ErrorResult("unknown argument: " + name)
		}
	}
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "check" && action != "set" {
		return tools.ErrorResult("action must be check or set")
	}
	if action == "check" {
		if _, exists := args["key"]; exists {
			return tools.ErrorResult("key is only accepted for set")
		}
		if _, exists := args["value"]; exists {
			return tools.ErrorResult("value is only accepted for set")
		}
		if t.runtime == nil {
			return tools.ErrorResult("runtime control is unavailable")
		}
		encoded, err := json.Marshal(t.runtime.Snapshot(ctx))
		if err != nil {
			return tools.ErrorResult("runtime snapshot is not JSON-compatible")
		}
		return tools.NewToolResult(string(encoded))
	}
	if !t.allowSet {
		return tools.ErrorResult("set is disabled: self tool is read-only")
	}
	key, ok := args["key"].(string)
	if !ok {
		return tools.ErrorResult("set requires a string key")
	}
	key = strings.TrimSpace(key)
	if key != "max_iterations" && key != "context_window_tokens" && key != "model_preset" {
		return tools.ErrorResult("key must be max_iterations, context_window_tokens or model_preset")
	}
	value, exists := args["value"]
	if !exists {
		return tools.ErrorResult("set requires value (use null to restore the configured default)")
	}
	if t.runtime == nil {
		return tools.ErrorResult("runtime control is unavailable")
	}
	if err := t.runtime.Set(ctx, key, value); err != nil {
		return tools.ErrorResult(err.Error())
	}
	logger.InfoCF("agent", "SelfTool session setting updated", map[string]any{
		"key": key, "agent": tools.ToolAgentID(ctx), "session": tools.ToolSessionKey(ctx),
	})
	if key == "model_preset" {
		if value == nil {
			return tools.NewToolResult("model preset reset to the configured default starting next turn")
		}
		return tools.NewToolResult(fmt.Sprintf("model preset will change to %q starting next turn", value))
	}
	if value == nil {
		return tools.NewToolResult(key + " reset to the configured default")
	}
	return tools.NewToolResult(key + " updated")
}

// clearSessionRuntime lets /clear restore the configured agent settings.
func (al *AgentLoop) clearSessionRuntime(agent *AgentInstance, sessionKey string) {
	key := sessionModelKey{agentID: agent.ID, sessionKey: sessionKey}
	al.clearSessionModelOverride(key)
	al.sessionRuntime.Delete(key)
}
