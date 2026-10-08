package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/tools"
)

const maxSelfRuntimeKeys = 64

// RuntimeControl is the read/write boundary exposed to SelfTool.
type RuntimeControl interface {
	Snapshot(ctx context.Context) map[string]any
	Set(ctx context.Context, key string, value any) error
}

type selfSession struct{ agent, session string }

// SelfTool gives the agent a filtered view of its runtime and a session-local
// scratchpad. ToolRegistry.Clone shares tool instances, so state remains keyed
// by agent and session instead of being copied into subagent registries.
type SelfTool struct {
	runtime    RuntimeControl
	allowSet   bool
	mu         sync.Mutex
	scratchpad map[selfSession]map[string]any
}

func NewSelfTool(runtime RuntimeControl, allowSet bool) *SelfTool {
	return &SelfTool{
		runtime:    runtime,
		allowSet:   allowSet,
		scratchpad: make(map[selfSession]map[string]any),
	}
}

func (t *SelfTool) Name() string { return "self" }

func (t *SelfTool) Description() string {
	base := "Inspect PicoClaw's filtered runtime state or keep temporary notes in this session's scratchpad.\n" +
		"Actions: check, set.\n" +
		"- check (no key): show the runtime snapshot and scratchpad.\n" +
		"- check (key): inspect a value; dot paths are supported (for example, " +
		"web_config.enabled or request.channel). Available fields include " +
		"max_iterations, context_window_tokens, model, model_preset, model_presets, " +
		"workspace, tool_names, web_config, exec_config, and request.channel/chat_id/sender_id.\n" +
		"- set (key, value): change max_iterations (integer 1–100) for this session, " +
		"effective immediately, select a configured " +
		"model_preset for this session (effective next turn), or store a JSON-safe " +
		"scratchpad note. Scratchpad notes are shared across turns in this session, " +
		"lost on restart, and limited to 64 keys and 10 levels of nesting.\n" +
		"Direct model changes are disabled; context_window_tokens cannot be changed " +
		"during an active session. Runtime snapshot fields such as workspace, " +
		"web_config, exec_config, model_presets, tool_names, and request are read-only.\n" +
		"Use check for model/settings questions or to diagnose tool behavior; check " +
		"context_window_tokens and max_iterations before a large task. Use model_preset " +
		"when asked to switch to a configured model, and scratchpad notes for " +
		"session-only reminders."
	if t.allowSet {
		return base + "\nIMPORTANT: Before using set, predict its impact. Warn the user before " +
			"critical changes, such as switching the session's model preset."
	}
	return base + "\nREAD-ONLY MODE: set is disabled."
}

func (t *SelfTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"check", "set"},
				"description": "Use check to inspect runtime state or set to update an allowed setting or scratchpad note.",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Optional target. For check, omit it for a full snapshot or use a dot path such as web_config.enabled or request.channel. For set, use max_iterations, model_preset, or a scratchpad note key.",
			},
			"value": map[string]any{
				"description": "Required for set. Use an integer from 1 to 100 for max_iterations, a configured preset name for model_preset, or a JSON-safe value for a scratchpad note (maximum 64 keys and 10 nesting levels).",
			},
		},
		"required": []string{"action"},
	}
}

func (t *SelfTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "inspect":
		action = "check"
	case "modify":
		action = "set"
	}
	sessionID := selfSessionID(ctx)
	log := func(detail string) {
		logger.InfoCF("agent", "SelfTool audit", map[string]any{
			"event":   fmt.Sprintf("self.%s | %s | session:%s", action, detail, sessionID),
			"agent":   tools.ToolAgentID(ctx),
			"session": sessionID,
		})
	}
	key, _ := args["key"].(string)
	if rawKey, exists := args["key"]; exists && rawKey != nil {
		if _, ok := rawKey.(string); !ok {
			log("REJECTED invalid key type")
			return tools.ErrorResult("key must be a string")
		}
	}
	key = strings.TrimSpace(key)
	for name := range args {
		if name != "action" && name != "key" && name != "value" {
			log("REJECTED unknown argument")
			return tools.ErrorResult("unknown argument: " + name)
		}
	}
	if action != "check" && action != "set" {
		log("REJECTED invalid action")
		return tools.ErrorResult("action must be check or set")
	}
	if t.runtime == nil {
		log("REJECTED unavailable runtime")
		return tools.ErrorResult("runtime control is unavailable")
	}
	if action == "check" {
		if _, exists := args["value"]; exists {
			log("REJECTED unexpected value")
			return tools.ErrorResult("value is only accepted for set")
		}
		view := t.runtime.Snapshot(ctx)
		view["scratchpad"] = t.sessionNotes(ctx)
		if key == "" {
			log("check summary")
			return tools.NewToolResult(formatSelfValue(view))
		}
		if err := validateSelfPath(key); err != nil {
			log("BLOCKED " + key)
			return tools.ErrorResult(err.Error())
		}
		value, err := resolveSelfPath(view, key)
		if err != nil {
			if note, ok := t.getNote(ctx, key); ok {
				log("check " + key)
				return tools.NewToolResult(formatSelfValue(note))
			}
			log("REJECTED " + key)
			return tools.ErrorResult(err.Error())
		}
		log("check " + key)
		return tools.NewToolResult(formatSelfValue(value))
	}
	if !t.allowSet {
		log("READ_ONLY " + key)
		return tools.ErrorResult("set is disabled: self tool is read-only")
	}
	if key == "" {
		log("REJECTED empty key")
		return tools.ErrorResult("set requires a non-empty key")
	}
	value, exists := args["value"]
	if !exists {
		log("REJECTED missing value")
		return tools.ErrorResult("set requires value")
	}
	if err := validateSelfPath(key); err != nil {
		log("BLOCKED " + key)
		return tools.ErrorResult(err.Error())
	}
	if isSelfReadOnly(key) {
		log("READ_ONLY " + key)
		return tools.ErrorResult("property is read-only: " + key)
	}
	if key == "max_iterations" {
		if _, err := selfInt(value); err != nil {
			log("REJECTED type mismatch " + key)
			return tools.ErrorResult("max_iterations must be an integer")
		}
		if err := t.runtime.Set(ctx, key, value); err != nil {
			log("REJECTED " + key)
			return tools.ErrorResult(err.Error())
		}
		log("set " + key)
		return tools.NewToolResult("max_iterations updated")
	}
	if isSelfRuntimeKey(key) {
		if err := t.runtime.Set(ctx, key, value); err != nil {
			log("REJECTED " + key)
			return tools.ErrorResult(err.Error())
		}
		log("set " + key)
		if key == "model_preset" {
			return tools.NewToolResult(
				fmt.Sprintf(
					"model preset for this session will change to %q starting next turn",
					value,
				),
			)
		}
		return tools.NewToolResult(key + " updated")
	}
	if isSelfRuntimeRoot(key) {
		log("REJECTED invalid runtime path " + key)
		return tools.ErrorResult("key is not a writable runtime property: " + key)
	}
	if err := validateSelfJSON(value, 0); err != nil {
		log("REJECTED JSON value")
		return tools.ErrorResult(err.Error())
	}
	if err := validateSelfSensitiveKeys(value); err != nil {
		log("BLOCKED sensitive scratchpad key")
		return tools.ErrorResult(err.Error())
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		log("REJECTED JSON value")
		return tools.ErrorResult("value must be JSON-compatible")
	}
	var clone any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		log("REJECTED JSON value")
		return tools.ErrorResult("value must be JSON-compatible")
	}
	if err := t.setNote(ctx, key, clone); err != nil {
		log("REJECTED " + key)
		return tools.ErrorResult(err.Error())
	}
	log("set scratchpad " + key)
	return tools.NewToolResult("scratchpad updated: " + key)
}

type agentRuntimeControl struct {
	agent *AgentInstance
	cfg   *config.Config
}

func (c *agentRuntimeControl) Snapshot(ctx context.Context) map[string]any {
	if c.agent == nil {
		return map[string]any{}
	}
	agent := c.agent
	maxIterations := agent.MaxIterations
	if ts := turnStateFromContext(ctx); ts != nil && ts.agent != nil {
		agent = ts.agent
		maxIterations = ts.maxIterations()
	} else {
		mu := c.agent.modelStateMutex()
		mu.RLock()
		defer mu.RUnlock()
	}
	view := map[string]any{
		"max_iterations":        maxIterations,
		"context_window_tokens": agent.ContextWindow,
		"model":                 agent.Model,
		"model_preset":          agent.Model,
		"workspace":             agent.Workspace,
		"tool_names":            agent.Tools.List(),
		"request":               requestSelfSnapshot(ctx),
	}
	if c.cfg != nil {
		web := c.cfg.Tools.Web
		presets := make([]string, 0, len(c.cfg.ModelList))
		for _, model := range c.cfg.ModelList {
			if model != nil && strings.TrimSpace(model.ModelName) != "" {
				presets = append(presets, model.ModelName)
			}
		}
		view["web_config"] = map[string]any{
			"enabled": web.Enabled, "provider": web.Provider, "prefer_native": web.PreferNative,
			"fetch_limit_bytes": web.FetchLimitBytes, "format": web.Format,
		}
		view["exec_config"] = map[string]any{
			"enabled": c.cfg.Tools.Exec.Enabled, "allow_remote": c.cfg.Tools.Exec.AllowRemote,
			"enable_deny_patterns": c.cfg.Tools.Exec.EnableDenyPatterns,
			"timeout_seconds":      c.cfg.Tools.Exec.TimeoutSeconds,
		}
		view["model_presets"] = presets
	}
	return view
}

func (c *agentRuntimeControl) Set(ctx context.Context, key string, value any) error {
	switch key {
	case "model":
		preset, ok := value.(string)
		if !ok || strings.TrimSpace(preset) == "" {
			return fmt.Errorf("%s must be a non-empty string", key)
		}
		return fmt.Errorf("model cannot be changed directly through SelfTool; use model_preset")
	case "model_preset":
		preset, ok := value.(string)
		if !ok || strings.TrimSpace(preset) == "" {
			return fmt.Errorf("model_preset must be a non-empty string")
		}
		ts := turnStateFromContext(ctx)
		al := AgentLoopFromContext(ctx)
		if ts == nil || ts.agent == nil || al == nil {
			return fmt.Errorf("session model changes require an active agent turn")
		}
		return al.setSessionModelPreset(ts.agentID, ts.sessionKey, ts.agent, preset)
	case "context_window_tokens":
		window, err := selfInt(value)
		if err != nil {
			return fmt.Errorf("context_window_tokens must be an integer")
		}
		if window < 4096 || window > 1_000_000 {
			return fmt.Errorf("context_window_tokens must be between 4096 and 1000000")
		}
		if turnStateFromContext(ctx) != nil {
			return fmt.Errorf("context_window_tokens cannot be changed during an active session")
		}
		if c.agent == nil {
			return fmt.Errorf("runtime control is unavailable")
		}
		mu := c.agent.modelStateMutex()
		mu.Lock()
		c.agent.ContextWindow = window
		mu.Unlock()
		return nil
	case "max_iterations":
	default:
		return fmt.Errorf(
			"%s cannot be changed safely through SelfTool; use the agent command or configuration",
			key,
		)
	}
	iterations, err := selfInt(value)
	if err != nil {
		return fmt.Errorf("max_iterations must be an integer: %w", err)
	}
	if iterations < 1 || iterations > 100 {
		return fmt.Errorf("max_iterations must be between 1 and 100")
	}
	ts := turnStateFromContext(ctx)
	al := AgentLoopFromContext(ctx)
	if ts == nil || al == nil {
		return fmt.Errorf("session iteration limit changes require an active agent turn")
	}
	al.setSessionIterationLimit(ts.agentID, ts.sessionKey, iterations)
	ts.maxIterationsOverride.Store(int64(iterations))
	return nil
}

func (al *AgentLoop) setSessionIterationLimit(agentID, sessionKey string, limit int) {
	al.sessionLimits.Store(sessionModelKey{agentID: agentID, sessionKey: sessionKey}, limit)
}

// sessionIterationLimit returns the max_iterations a session set, or 0 if none.
func (al *AgentLoop) sessionIterationLimit(agentID, sessionKey string) int {
	limit, _ := al.sessionLimits.Load(sessionModelKey{agentID: agentID, sessionKey: sessionKey})
	value, _ := limit.(int)
	return value
}

func requestSelfSnapshot(ctx context.Context) map[string]any {
	request := map[string]any{
		"channel":   tools.ToolChannel(ctx),
		"chat_id":   tools.ToolChatID(ctx),
		"sender_id": "",
	}
	if ts := turnStateFromContext(ctx); ts != nil {
		request["channel"] = ts.channel
		request["chat_id"] = ts.chatID
		request["sender_id"] = ts.opts.Dispatch.SenderID()
	}
	return request
}

func (t *SelfTool) sessionNotes(ctx context.Context) map[string]any {
	key := selfSessionFor(ctx)
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneSelfMap(t.scratchpad[key])
}

func (t *SelfTool) getNote(ctx context.Context, key string) (any, bool) {
	session := selfSessionFor(ctx)
	t.mu.Lock()
	defer t.mu.Unlock()
	value, ok := t.scratchpad[session][key]
	return value, ok
}

func (t *SelfTool) setNote(ctx context.Context, key string, value any) error {
	session := selfSessionFor(ctx)
	t.mu.Lock()
	defer t.mu.Unlock()
	entries := t.scratchpad[session]
	if entries == nil {
		entries = make(map[string]any)
		t.scratchpad[session] = entries
	}
	if _, exists := entries[key]; !exists && len(entries) >= maxSelfRuntimeKeys {
		return fmt.Errorf(
			"scratchpad is full (%d keys); remove an unused key first",
			maxSelfRuntimeKeys,
		)
	}
	entries[key] = value
	return nil
}

func selfSessionFor(ctx context.Context) selfSession {
	agentID, sessionID := tools.ToolAgentID(ctx), tools.ToolSessionKey(ctx)
	if ts := turnStateFromContext(ctx); ts != nil {
		if agentID == "" {
			agentID = ts.agentID
		}
		if sessionID == "" {
			sessionID = ts.sessionKey
		}
	}
	if sessionID == "" && (tools.ToolChannel(ctx) != "" || tools.ToolChatID(ctx) != "") {
		sessionID = tools.ToolChannel(ctx) + ":" + tools.ToolChatID(ctx)
	}
	if agentID == "" {
		agentID = "unknown"
	}
	if sessionID == "" {
		sessionID = "unknown"
	}
	return selfSession{agent: agentID, session: sessionID}
}

func selfSessionID(ctx context.Context) string {
	return selfSessionFor(ctx).session
}

var selfBlockedKeys = map[string]struct{}{
	"bus": {}, "provider": {}, "runtime_resolver": {}, "_running": {}, "tools": {},
	"_runtime_vars": {}, "runner": {}, "sessions": {}, "consolidator": {}, "subagents": {},
	"dream": {}, "auto_compact": {}, "context": {}, "commands": {}, "_pending_queues": {},
	"_session_locks": {}, "_active_tasks": {}, "_background_tasks": {}, "restrict_to_workspace": {},
	"channels_config": {}, "_concurrency_gate": {}, "_unified_session": {}, "_extra_hooks": {},
	"_hook_factories": {},
}

var selfSensitiveParts = []string{
	"api_key", "secret", "password", "token", "credential", "private_key", "access_token", "refresh_token", "auth",
}

func validateSelfPath(path string) error {
	// Runtime keys are fixed and harmless; "context_window_tokens" would
	// otherwise match the "token" credential filter.
	if isSelfRuntimeKey(path) {
		return nil
	}
	for _, part := range strings.Split(path, ".") {
		lower := strings.ToLower(part)
		if part == "" || strings.HasPrefix(part, "__") || strings.HasSuffix(part, "__") {
			return fmt.Errorf("access denied")
		}
		if _, blocked := selfBlockedKeys[lower]; blocked || selfSensitiveName(lower) {
			return fmt.Errorf("access denied")
		}
	}
	return nil
}

func selfSensitiveName(name string) bool {
	for _, part := range selfSensitiveParts {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

func isSelfReadOnly(path string) bool {
	root := strings.Split(path, ".")[0]
	switch root {
	case "tool_names",
		"exec_config",
		"web_config",
		"model_presets",
		"workspace",
		"provider_retry_mode",
		"max_tool_result_chars",
		"request":
		return true
	default:
		return false
	}
}

func isSelfRuntimeKey(path string) bool {
	switch path {
	case "max_iterations", "context_window_tokens", "model", "model_preset":
		return true
	default:
		return false
	}
}

func isSelfRuntimeRoot(path string) bool {
	switch strings.Split(path, ".")[0] {
	case "max_iterations", "context_window_tokens", "model", "model_preset":
		return true
	default:
		return false
	}
}

func resolveSelfPath(view map[string]any, path string) (any, error) {
	var current any = view
	for _, part := range strings.Split(path, ".") {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("key not found: %s", path)
		}
		value, ok := mapping[part]
		if !ok {
			return nil, fmt.Errorf("key not found: %s", path)
		}
		current = value
	}
	return current, nil
}

func validateSelfJSON(value any, depth int) error {
	if depth > 10 {
		return fmt.Errorf("nesting too deep; maximum is 10 levels")
	}
	switch value := value.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return nil
	case float32:
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("value must be JSON-compatible")
		}
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("value must be JSON-compatible")
		}
	case []any:
		for _, item := range value {
			if err := validateSelfJSON(item, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range value {
			if err := validateSelfJSON(item, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported value type %T", value)
	}
	return nil
}

func validateSelfSensitiveKeys(value any) error {
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if err := validateSelfSensitiveKeys(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range value {
			if selfSensitiveName(strings.ToLower(key)) {
				return fmt.Errorf("access denied")
			}
			if err := validateSelfSensitiveKeys(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func selfInt(value any) (int, error) {
	switch value := value.(type) {
	case int:
		return value, nil
	case int8:
		return int(value), nil
	case int16:
		return int(value), nil
	case int32:
		return int(value), nil
	case int64:
		return int(value), nil
	case uint:
		return int(value), nil
	case uint8:
		return int(value), nil
	case uint16:
		return int(value), nil
	case uint32:
		return int(value), nil
	case uint64:
		return int(value), nil
	case float64:
		if math.Trunc(value) == value && value >= float64(math.MinInt) && value <= float64(math.MaxInt) {
			return int(value), nil
		}
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed), nil
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("expected a whole number")
}

func formatSelfValue(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case string:
		return value
	case map[string]any:
		if len(value) == 0 {
			return "{}"
		}
		encoded, _ := json.Marshal(value)
		if len(value) <= 5 && len(encoded) <= 200 {
			return string(encoded)
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 15 {
			keys = keys[:15]
		}
		return "{" + strings.Join(keys, ", ") + ", ...}"
	case []any:
		if len(value) > 20 {
			return fmt.Sprintf("[%d items]", len(value))
		}
		encoded, _ := json.Marshal(value)
		return string(encoded)
	case []string:
		if len(value) > 20 {
			return fmt.Sprintf("[%d items]", len(value))
		}
		encoded, _ := json.Marshal(value)
		return string(encoded)
	default:
		return fmt.Sprint(value)
	}
}

func cloneSelfMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}
