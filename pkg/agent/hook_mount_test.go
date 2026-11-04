package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

type builtinAutoHookConfig struct {
	Model  string `json:"model"`
	Suffix string `json:"suffix"`
}

type builtinAutoHook struct {
	model  string
	suffix string
}

func (h *builtinAutoHook) BeforeLLM(
	ctx context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	next := req.Clone()
	next.Model = h.model
	return next, HookDecision{Action: HookActionModify}, nil
}

func (h *builtinAutoHook) AfterLLM(
	ctx context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	next := resp.Clone()
	if next.Response != nil {
		next.Response.Content += h.suffix
	}
	return next, HookDecision{Action: HookActionModify}, nil
}

func newHookLoopConfig(workspace string, hooks config.HooksConfig) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Hooks: hooks,
	}
}

func newReloadableHookLoop(
	t *testing.T,
	provider *llmHookTestProvider,
	hooks config.HooksConfig,
) (*AgentLoop, *config.Config) {
	t.Helper()

	cfg := newHookLoopConfig(t.TempDir(), hooks)
	return NewAgentLoop(cfg, bus.NewMessageBus(), provider), cfg
}

func newConfiguredHookLoop(t *testing.T, provider *llmHookTestProvider, hooks config.HooksConfig) *AgentLoop {
	t.Helper()

	al, _ := newReloadableHookLoop(t, provider, hooks)
	return al
}

func TestAgentLoop_ProcessDirectWithChannel_AutoMountsBuiltinHook(t *testing.T) {
	const hookName = "test-auto-builtin-hook"

	if err := RegisterBuiltinHook(hookName, func(
		ctx context.Context,
		spec config.BuiltinHookConfig,
	) (any, error) {
		var hookCfg builtinAutoHookConfig
		if len(spec.Config) > 0 {
			if err := json.Unmarshal(spec.Config, &hookCfg); err != nil {
				return nil, err
			}
		}
		return &builtinAutoHook{
			model:  hookCfg.Model,
			suffix: hookCfg.Suffix,
		}, nil
	}); err != nil {
		t.Fatalf("RegisterBuiltinHook failed: %v", err)
	}
	t.Cleanup(func() {
		unregisterBuiltinHook(hookName)
	})

	rawCfg, err := json.Marshal(builtinAutoHookConfig{
		Model:  "builtin-model",
		Suffix: "|builtin",
	})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	provider := &llmHookTestProvider{}
	al := newConfiguredHookLoop(t, provider, config.HooksConfig{
		Enabled: true,
		Builtins: map[string]config.BuiltinHookConfig{
			hookName: {
				Enabled: true,
				Config:  rawCfg,
			},
		},
	})
	defer al.Close()

	resp, err := al.ProcessDirectWithChannel(context.Background(), "hello", "session-1", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if resp != "provider content|builtin" {
		t.Fatalf("expected builtin-hooked content, got %q", resp)
	}

	provider.mu.Lock()
	lastModel := provider.lastModel
	provider.mu.Unlock()
	if lastModel != "builtin-model" {
		t.Fatalf("expected builtin model, got %q", lastModel)
	}
}

func TestAgentLoop_ProcessDirectWithChannel_AutoMountsProcessHook(t *testing.T) {
	provider := &llmHookTestProvider{}
	eventLog := filepath.Join(t.TempDir(), "events.log")

	al := newConfiguredHookLoop(t, provider, config.HooksConfig{
		Enabled: true,
		Processes: map[string]config.ProcessHookConfig{
			"ipc-auto": {
				Enabled: true,
				Command: processHookHelperCommand(),
				Env: map[string]string{
					"PICOCLAW_HOOK_HELPER":    "1",
					"PICOCLAW_HOOK_MODE":      "rewrite",
					"PICOCLAW_HOOK_EVENT_LOG": eventLog,
				},
				Observe:   []string{"turn_end"},
				Intercept: []string{"before_llm", "after_llm"},
			},
		},
	})
	defer al.Close()

	resp, err := al.ProcessDirectWithChannel(context.Background(), "hello", "session-1", "cli", "direct")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if resp != "provider content|ipc" {
		t.Fatalf("expected process-hooked content, got %q", resp)
	}

	provider.mu.Lock()
	lastModel := provider.lastModel
	provider.mu.Unlock()
	if lastModel != "process-model" {
		t.Fatalf("expected process model, got %q", lastModel)
	}

	waitForFileContains(t, eventLog, "agent.turn.end")
}

func TestProcessHookObserveKindsFromConfigAcceptsRuntimeNames(t *testing.T) {
	kinds, enabled, err := processHookObserveKindsFromConfig([]string{
		"tool_exec_start",
		"agent.tool.exec_end",
		"post_compaction",
		"gateway.ready",
		"mcp.server.failed",
	})
	if err != nil {
		t.Fatalf("processHookObserveKindsFromConfig failed: %v", err)
	}
	if !enabled {
		t.Fatal("expected observe to be enabled")
	}

	want := []string{
		"agent.tool.exec_start",
		"agent.tool.exec_end",
		"agent.context.compress",
		"agent.session.summarize",
		"gateway.ready",
		"mcp.server.failed",
	}
	if !slices.Equal(kinds, want) {
		t.Fatalf("observe kinds = %v, want %v", kinds, want)
	}
}

func TestProcessHookOptionsFromConfigMarksCompletedOnly(t *testing.T) {
	opts, err := processHookOptionsFromConfig(config.ProcessHookConfig{
		Enabled: true,
		Command: []string{"echo"},
		Observe: []string{"turn_completed"},
	})
	if err != nil {
		t.Fatalf("processHookOptionsFromConfig failed: %v", err)
	}
	if !opts.Observe {
		t.Fatal("expected observe enabled")
	}
	if !opts.CompletedOnly {
		t.Fatal("expected CompletedOnly for turn_completed alias")
	}

	raw, err := processHookOptionsFromConfig(config.ProcessHookConfig{
		Enabled: true,
		Command: []string{"echo"},
		Observe: []string{"agent.turn.end"},
	})
	if err != nil {
		t.Fatalf("processHookOptionsFromConfig failed: %v", err)
	}
	if raw.CompletedOnly {
		t.Fatal("did not expect CompletedOnly for raw agent.turn.end")
	}
}

func TestAgentLoop_ProcessDirectWithChannel_InvalidConfiguredHookFails(t *testing.T) {
	provider := &llmHookTestProvider{}
	al := newConfiguredHookLoop(t, provider, config.HooksConfig{
		Enabled: true,
		Processes: map[string]config.ProcessHookConfig{
			"bad-hook": {
				Enabled:   true,
				Command:   processHookHelperCommand(),
				Intercept: []string{"not_supported"},
			},
		},
	})
	defer al.Close()

	_, err := al.ProcessDirectWithChannel(context.Background(), "hello", "session-1", "cli", "direct")
	if err == nil {
		t.Fatal("expected invalid configured hook error")
	}
}

func registerDenyApprovalBuiltin(t *testing.T, name string) {
	t.Helper()

	if err := RegisterBuiltinHook(name, func(
		context.Context,
		config.BuiltinHookConfig,
	) (any, error) {
		return &denyApprovalHook{}, nil
	}); err != nil {
		t.Fatalf("RegisterBuiltinHook(%q) failed: %v", name, err)
	}
	t.Cleanup(func() { unregisterBuiltinHook(name) })
}

func TestReloadProviderAndConfigKeepsApprovalHookWhenReinitializationFails(t *testing.T) {
	const hookName = "test-reload-approval-gate"

	registerDenyApprovalBuiltin(t, hookName)

	provider := &llmHookTestProvider{}
	al, cfg := newReloadableHookLoop(t, provider, config.HooksConfig{
		Enabled: true,
		Builtins: map[string]config.BuiltinHookConfig{
			hookName: {Enabled: true},
		},
	})
	defer al.Close()

	if err := al.ensureHooksInitialized(context.Background()); err != nil {
		t.Fatalf("ensureHooksInitialized failed: %v", err)
	}
	if decision := al.hooks.ApproveTool(context.Background(), &ToolApprovalRequest{Tool: "exec"}); decision.Approved {
		t.Fatal("baseline: approval hook should deny exec")
	}

	// Make the configured hook unavailable, then reload.
	unregisterBuiltinHook(hookName)

	if err := al.ReloadProviderAndConfig(context.Background(), provider, cfg); err == nil {
		t.Fatal("expected reload to fail when the approval hook cannot be rebuilt")
	}

	if decision := al.hooks.ApproveTool(context.Background(), &ToolApprovalRequest{Tool: "exec"}); decision.Approved {
		t.Fatal("approval gate disappeared after a failed reload")
	}
	if got := al.hookRuntime.mountedNames(); !slices.Equal(got, []string{hookName}) {
		t.Fatalf("mounted hooks = %v, want %q", got, hookName)
	}
}

func TestReloadProviderAndConfigReplacesConfiguredHooks(t *testing.T) {
	const oldHookName = "test-reload-approval-gate-old"
	const newHookName = "test-reload-approval-gate-new"

	registerDenyApprovalBuiltin(t, oldHookName)
	registerDenyApprovalBuiltin(t, newHookName)

	provider := &llmHookTestProvider{}
	al, cfg := newReloadableHookLoop(t, provider, config.HooksConfig{
		Enabled: true,
		Builtins: map[string]config.BuiltinHookConfig{
			oldHookName: {Enabled: true},
		},
	})
	defer al.Close()

	if err := al.ensureHooksInitialized(context.Background()); err != nil {
		t.Fatalf("ensureHooksInitialized failed: %v", err)
	}

	reloadedCfg := newHookLoopConfig(cfg.Agents.Defaults.Workspace, config.HooksConfig{
		Enabled: true,
		Builtins: map[string]config.BuiltinHookConfig{
			newHookName: {Enabled: true},
		},
	})
	if err := al.ReloadProviderAndConfig(context.Background(), provider, reloadedCfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig failed: %v", err)
	}

	if got := al.hookRuntime.mountedNames(); !slices.Equal(got, []string{newHookName}) {
		t.Fatalf("mounted hooks = %v, want %q", got, newHookName)
	}
	hooks := al.hooks.snapshotHooks()
	if len(hooks) != 1 || hooks[0].Name != newHookName {
		t.Fatalf("mounted hook registrations = %+v, want only %q", hooks, newHookName)
	}
	if decision := al.hooks.ApproveTool(context.Background(), &ToolApprovalRequest{Tool: "exec"}); decision.Approved {
		t.Fatal("replacement approval hook should deny exec")
	}
}
