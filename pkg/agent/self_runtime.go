package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// sessionRuntime holds the per-session overrides the self tool can set. A zero
// value means "follow the configured agent".
type sessionRuntime struct {
	maxIterations int
	contextWindow int
}

func (al *AgentLoop) sessionRuntimeFor(agentID, sessionKey string) sessionRuntime {
	value, _ := al.sessionRuntime.Load(sessionModelKey{agentID: agentID, sessionKey: sessionKey})
	setting, _ := value.(sessionRuntime)
	return setting
}

func (al *AgentLoop) updateSessionRuntime(agentID, sessionKey string, apply func(*sessionRuntime)) {
	key := sessionModelKey{agentID: agentID, sessionKey: sessionKey}
	value, _ := al.sessionRuntime.Load(key)
	setting, _ := value.(sessionRuntime)
	apply(&setting)
	al.sessionRuntime.Store(key, setting)
}

// sessionIterationLimit returns the max_iterations a session set, or 0 if none.
func (al *AgentLoop) sessionIterationLimit(agentID, sessionKey string) int {
	return al.sessionRuntimeFor(agentID, sessionKey).maxIterations
}

// sessionContextWindow returns the context_window_tokens a session set, or 0 if none.
func (al *AgentLoop) sessionContextWindow(agentID, sessionKey string) int {
	return al.sessionRuntimeFor(agentID, sessionKey).contextWindow
}

// agentRuntimeControl is the RuntimeControl the self tool reads and writes
// through. It is bound to the agent instance that owns the tool.
type agentRuntimeControl struct {
	agent *AgentInstance
	cfg   *config.Config
}

func (c *agentRuntimeControl) Snapshot(ctx context.Context) map[string]any {
	if c.agent == nil {
		return map[string]any{}
	}
	agent := c.agent
	modelMu := agent.modelStateMutex()
	modelMu.RLock()
	maxIterations, contextWindow, effectiveModel := agent.MaxIterations, agent.ContextWindow, agent.Model
	modelMu.RUnlock()
	if ts := turnStateFromContext(ctx); ts != nil && ts.agent != nil {
		agent = ts.agent
		maxIterations = ts.maxIterations()
		contextWindow = ts.contextWindow()
		effectiveModel = ts.agent.Model
	}
	view := map[string]any{
		"max_iterations":        maxIterations,
		"context_window_tokens": contextWindow,
		"model":                 effectiveModel,
		"model_presets":         []string{},
		"tool_names":            agent.Tools.List(),
		"turn":                  nil,
		"context_usage":         nil,
	}
	if ts := turnStateFromContext(ctx); ts != nil && ts.agent != nil {
		info := ts.snapshot()
		view["turn"] = map[string]any{
			"phase":                string(info.Phase),
			"iteration":            info.Iteration,
			"max_iterations":       maxIterations,
			"remaining_iterations": max(0, maxIterations-info.Iteration),
			"elapsed_ms":           max(int64(0), time.Since(info.StartedAt).Milliseconds()),
			"depth":                info.Depth,
		}
		view["context_usage"] = ts.selfContextSnapshot(contextWindow)
	}
	if c.cfg != nil {
		view["model_presets"] = selfModelPresets(c.cfg)
	}
	return view
}

type selfContextUsage struct {
	usedTokens    int
	outputReserve int
	source        string
}

func (ts *turnState) recordSelfContextUsage(
	messages []providers.Message,
	defs []providers.ToolDefinition,
	usage *providers.UsageInfo,
	options map[string]any,
) {
	budget := &selfContextUsage{source: "local_estimate", outputReserve: ts.agent.MaxTokens}
	if reserve, err := selfInt(options["max_tokens"]); err == nil {
		budget.outputReserve = max(0, reserve)
	}
	if usage != nil && usage.PromptTokens > 0 {
		budget.usedTokens, budget.source = usage.PromptTokens, "provider"
	} else {
		budget.usedTokens = EstimateToolDefsTokens(defs)
		for _, message := range messages {
			budget.usedTokens += EstimateMessageTokens(message)
		}
	}
	ts.mu.Lock()
	ts.selfContextUsage = budget
	ts.mu.Unlock()
}

// The budget describes the last LLM input, before its reply and subsequent tool results.
func (ts *turnState) selfContextSnapshot(window int) map[string]any {
	ts.mu.RLock()
	budget := ts.selfContextUsage
	ts.mu.RUnlock()
	if budget == nil || window <= 0 {
		return nil
	}
	effectiveWindow := max(0, window-budget.outputReserve)
	percent := 0
	if effectiveWindow > 0 {
		percent = min(100, budget.usedTokens*100/effectiveWindow)
	} else if budget.usedTokens > 0 {
		percent = 100
	}
	return map[string]any{
		"used_tokens":           budget.usedTokens,
		"total_tokens":          window,
		"output_reserve_tokens": budget.outputReserve,
		"available_tokens":      max(0, effectiveWindow-budget.usedTokens),
		"used_percent":          percent,
		"source":                budget.source,
	}
}

func (c *agentRuntimeControl) Set(ctx context.Context, key string, value any) error {
	if key != "max_iterations" && key != "context_window_tokens" && key != "model_preset" {
		return fmt.Errorf("key must be max_iterations, context_window_tokens or model_preset")
	}
	ts, al, err := selfSessionTurn(ctx, "setting changes")
	if err != nil {
		return err
	}
	if key == "model_preset" {
		if value == nil {
			al.clearSessionModelOverride(sessionModelKey{agentID: ts.agentID, sessionKey: ts.sessionKey})
			return nil
		}
		preset, ok := value.(string)
		if !ok || strings.TrimSpace(preset) == "" {
			return fmt.Errorf("model_preset must be a non-empty string or null")
		}
		return al.setSessionModelPreset(ts.agentID, ts.sessionKey, ts.agent, preset)
	}
	number := 0
	if value != nil {
		lower, upper := 1, 100
		if key == "context_window_tokens" {
			lower, upper = 4096, 1000000
		}
		number, err = selfInt(value)
		if err != nil || number < lower || number > upper {
			return fmt.Errorf("%s must be an integer between %d and %d or null", key, lower, upper)
		}
	}
	al.updateSessionRuntime(ts.agentID, ts.sessionKey, func(s *sessionRuntime) {
		if key == "max_iterations" {
			s.maxIterations = number
		} else {
			s.contextWindow = number
		}
	})
	if key == "max_iterations" {
		ts.maxIterationsOverride.Store(int64(number))
	} else {
		ts.contextWindowOverride.Store(int64(number))
	}
	return nil
}

// selfSessionTurn returns the active turn and loop needed for a session-scoped
// write. Turns without a session key share the empty entry, so they are refused.
func selfSessionTurn(ctx context.Context, action string) (*turnState, *AgentLoop, error) {
	ts := turnStateFromContext(ctx)
	al := AgentLoopFromContext(ctx)
	if ts == nil || ts.agent == nil || al == nil {
		return nil, nil, fmt.Errorf("session %s require an active agent turn", action)
	}
	if strings.TrimSpace(ts.sessionKey) == "" {
		return nil, nil, fmt.Errorf("session %s require a session", action)
	}
	return ts, al, nil
}

func selfModelPresets(cfg *config.Config) []string {
	presets := make([]string, 0, len(cfg.ModelList))
	for _, model := range cfg.ModelList {
		if model != nil && strings.TrimSpace(model.ModelName) != "" {
			presets = append(presets, model.ModelName)
		}
	}
	return presets
}

func selfInt(value any) (int, error) {
	switch value := value.(type) {
	case int:
		return value, nil
	case float64:
		if math.Trunc(value) == value && value >= float64(math.MinInt) && value <= float64(math.MaxInt) {
			return int(value), nil
		}
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return int(parsed), nil
		}
	}
	return 0, fmt.Errorf("expected a whole number")
}
