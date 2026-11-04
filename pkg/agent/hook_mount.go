package agent

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

type hookRuntime struct {
	initOnce sync.Once
	// applyMu serializes lazy initialization and reload-time replacement so
	// the two paths cannot race each other.
	applyMu sync.Mutex
	mu      sync.Mutex
	initErr error
	mounted []string
}

func (r *hookRuntime) setInitErr(err error) {
	r.mu.Lock()
	r.initErr = err
	r.mu.Unlock()
}

func (r *hookRuntime) getInitErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.initErr
}

func (r *hookRuntime) setMounted(names []string) {
	r.mu.Lock()
	r.mounted = append([]string(nil), names...)
	r.mu.Unlock()
}

func (r *hookRuntime) mountedNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.mounted...)
}

// apply mounts regs and unmounts configured hooks that are no longer present.
// New hooks are mounted before stale ones are removed, so replacing a gate
// never leaves the manager without an approval hook.
func (r *hookRuntime) apply(al *AgentLoop, regs []HookRegistration) error {
	if err := validateHookRegistrations(regs); err != nil {
		return err
	}
	if al == nil {
		if len(regs) != 0 {
			return fmt.Errorf("agent loop is nil")
		}
		r.setMounted(nil)
		r.setInitErr(nil)
		return nil
	}
	if len(regs) > 0 && al.hooks == nil {
		return fmt.Errorf("hook manager is not initialized")
	}

	newNames := make([]string, 0, len(regs))
	for _, reg := range regs {
		if err := al.MountHook(reg); err != nil {
			return fmt.Errorf("mount hook %q: %w", reg.Name, err)
		}
		newNames = append(newNames, reg.Name)
	}

	for _, name := range r.mountedNames() {
		if !slices.Contains(newNames, name) {
			al.UnmountHook(name)
		}
	}

	r.setMounted(newNames)
	r.setInitErr(nil)
	return nil
}

func validateHookRegistrations(regs []HookRegistration) error {
	seen := make(map[string]struct{}, len(regs))
	for _, reg := range regs {
		if reg.Name == "" {
			return fmt.Errorf("hook name is required")
		}
		if reg.Hook == nil {
			return fmt.Errorf("hook %q is nil", reg.Name)
		}
		if _, exists := seen[reg.Name]; exists {
			return fmt.Errorf("hook %q is duplicated", reg.Name)
		}
		seen[reg.Name] = struct{}{}
	}
	return nil
}

// replace applies a pre-built hook set and marks initialization as complete.
// If the set cannot be applied, the previous hooks remain mounted.
func (r *hookRuntime) replace(al *AgentLoop, regs []HookRegistration) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()

	if err := r.apply(al, regs); err != nil {
		r.setInitErr(err)
		return err
	}
	r.initOnce.Do(func() {})
	return nil
}

// BuiltinHookFactory constructs an in-process hook from config.
type BuiltinHookFactory func(ctx context.Context, spec config.BuiltinHookConfig) (any, error)

var (
	builtinHookRegistryMu sync.RWMutex
	builtinHookRegistry   = map[string]BuiltinHookFactory{}
)

// RegisterBuiltinHook registers a named in-process hook factory for config-driven mounting.
func RegisterBuiltinHook(name string, factory BuiltinHookFactory) error {
	if name == "" {
		return fmt.Errorf("builtin hook name is required")
	}
	if factory == nil {
		return fmt.Errorf("builtin hook %q factory is nil", name)
	}

	builtinHookRegistryMu.Lock()
	defer builtinHookRegistryMu.Unlock()

	if _, exists := builtinHookRegistry[name]; exists {
		return fmt.Errorf("builtin hook %q is already registered", name)
	}
	builtinHookRegistry[name] = factory
	return nil
}

func unregisterBuiltinHook(name string) {
	if name == "" {
		return
	}
	builtinHookRegistryMu.Lock()
	delete(builtinHookRegistry, name)
	builtinHookRegistryMu.Unlock()
}

func lookupBuiltinHook(name string) (BuiltinHookFactory, bool) {
	builtinHookRegistryMu.RLock()
	defer builtinHookRegistryMu.RUnlock()

	factory, ok := builtinHookRegistry[name]
	return factory, ok
}

func configureHookManagerFromConfig(hm *HookManager, cfg *config.Config) {
	if hm == nil || cfg == nil {
		return
	}
	hm.ConfigureTimeouts(
		hookTimeoutFromMS(cfg.Hooks.Defaults.ObserverTimeoutMS),
		hookTimeoutFromMS(cfg.Hooks.Defaults.InterceptorTimeoutMS),
		hookTimeoutFromMS(cfg.Hooks.Defaults.ApprovalTimeoutMS),
	)
}

func hookTimeoutFromMS(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (al *AgentLoop) ensureHooksInitialized(ctx context.Context) error {
	if al == nil || al.cfg == nil || al.hooks == nil {
		return nil
	}

	al.hookRuntime.applyMu.Lock()
	defer al.hookRuntime.applyMu.Unlock()

	al.hookRuntime.initOnce.Do(func() {
		al.hookRuntime.setInitErr(al.loadConfiguredHooks(ctx))
	})

	return al.hookRuntime.getInitErr()
}

func (al *AgentLoop) loadConfiguredHooks(ctx context.Context) error {
	if al == nil || al.cfg == nil {
		return nil
	}

	regs, err := al.buildConfiguredHooks(ctx, al.cfg)
	if err != nil {
		return err
	}
	return al.hookRuntime.apply(al, regs)
}

// buildConfiguredHooks constructs the configured hooks without mounting them.
// A caller can therefore fail a reload before changing any live state.
func (al *AgentLoop) buildConfiguredHooks(
	ctx context.Context,
	cfg *config.Config,
) ([]HookRegistration, error) {
	if cfg == nil || !cfg.Hooks.Enabled {
		return nil, nil
	}

	var regs []HookRegistration
	for _, name := range enabledBuiltinHookNames(cfg.Hooks.Builtins) {
		spec := cfg.Hooks.Builtins[name]
		factory, ok := lookupBuiltinHook(name)
		if !ok {
			closeHookRegistrations(regs)
			return nil, fmt.Errorf("builtin hook %q is not registered", name)
		}

		hook, factoryErr := factory(ctx, spec)
		if factoryErr != nil {
			closeHookRegistrations(regs)
			return nil, fmt.Errorf("build builtin hook %q: %w", name, factoryErr)
		}
		regs = append(regs, HookRegistration{
			Name:     name,
			Priority: spec.Priority,
			Source:   HookSourceInProcess,
			Hook:     hook,
		})
	}

	for _, name := range enabledProcessHookNames(cfg.Hooks.Processes) {
		spec := cfg.Hooks.Processes[name]
		opts, buildErr := processHookOptionsFromConfig(spec)
		if buildErr != nil {
			closeHookRegistrations(regs)
			return nil, fmt.Errorf("configure process hook %q: %w", name, buildErr)
		}

		processHook, buildErr := NewProcessHook(ctx, name, opts)
		if buildErr != nil {
			closeHookRegistrations(regs)
			return nil, fmt.Errorf("start process hook %q: %w", name, buildErr)
		}
		regs = append(regs, HookRegistration{
			Name:     name,
			Priority: spec.Priority,
			Source:   HookSourceProcess,
			Hook:     processHook,
		})
	}

	return regs, nil
}

func closeHookRegistrations(regs []HookRegistration) {
	for _, reg := range regs {
		closeHookIfPossible(reg.Hook)
	}
}

func enabledBuiltinHookNames(specs map[string]config.BuiltinHookConfig) []string {
	if len(specs) == 0 {
		return nil
	}

	names := make([]string, 0, len(specs))
	for name, spec := range specs {
		if spec.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func enabledProcessHookNames(specs map[string]config.ProcessHookConfig) []string {
	if len(specs) == 0 {
		return nil
	}

	names := make([]string, 0, len(specs))
	for name, spec := range specs {
		if spec.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func processHookOptionsFromConfig(spec config.ProcessHookConfig) (ProcessHookOptions, error) {
	transport := spec.Transport
	if transport == "" {
		transport = "stdio"
	}
	if transport != "stdio" {
		return ProcessHookOptions{}, fmt.Errorf("unsupported transport %q", transport)
	}
	if len(spec.Command) == 0 {
		return ProcessHookOptions{}, fmt.Errorf("command is required")
	}

	opts := ProcessHookOptions{
		Command: append([]string(nil), spec.Command...),
		Dir:     spec.Dir,
		Env:     processHookEnvFromMap(spec.Env),
	}

	observeKinds, observeEnabled, err := processHookObserveKindsFromConfig(spec.Observe)
	if err != nil {
		return ProcessHookOptions{}, err
	}
	opts.Observe = observeEnabled
	opts.ObserveKinds = observeKinds
	opts.CompletedOnly = slices.Contains(spec.Observe, "turn_completed")

	for _, intercept := range spec.Intercept {
		switch intercept {
		case "before_llm", "after_llm":
			opts.InterceptLLM = true
		case "before_tool", "after_tool":
			opts.InterceptTool = true
		case "approve_tool":
			opts.ApproveTool = true
		case "":
			continue
		default:
			return ProcessHookOptions{}, fmt.Errorf("unsupported intercept %q", intercept)
		}
	}

	if !opts.Observe && !opts.InterceptLLM && !opts.InterceptTool && !opts.ApproveTool {
		return ProcessHookOptions{}, fmt.Errorf("no hook modes enabled")
	}

	return opts, nil
}

func processHookEnvFromMap(envMap map[string]string) []string {
	if len(envMap) == 0 {
		return nil
	}

	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func processHookObserveKindsFromConfig(observe []string) ([]string, bool, error) {
	if len(observe) == 0 {
		return nil, false, nil
	}

	validKinds := validHookEventKinds()
	normalized := make([]string, 0, len(observe))
	for _, kind := range observe {
		switch kind {
		case "", "*", "all":
			return nil, true, nil
		case "post_compaction":
			normalized = append(normalized,
				runtimeevents.KindAgentContextCompress.String(),
				runtimeevents.KindAgentSessionSummarize.String(),
			)
		default:
			normalizedKind, ok := validKinds[kind]
			if !ok {
				return nil, false, fmt.Errorf("unsupported observe event %q", kind)
			}
			normalized = append(normalized, normalizedKind)
		}
	}

	if len(normalized) == 0 {
		return nil, false, nil
	}
	return normalized, true, nil
}

func validHookEventKinds() map[string]string {
	runtimeKinds := runtimeevents.KnownKinds()
	kinds := make(map[string]string, len(runtimeKinds)*2)
	for _, kind := range runtimeKinds {
		kinds[kind.String()] = kind.String()
	}
	kinds["turn_start"] = runtimeevents.KindAgentTurnStart.String()
	kinds["turn_end"] = runtimeevents.KindAgentTurnEnd.String()
	kinds["turn_completed"] = runtimeevents.KindAgentTurnEnd.String()
	kinds["llm_request"] = runtimeevents.KindAgentLLMRequest.String()
	kinds["llm_delta"] = runtimeevents.KindAgentLLMDelta.String()
	kinds["llm_response"] = runtimeevents.KindAgentLLMResponse.String()
	kinds["llm_retry"] = runtimeevents.KindAgentLLMRetry.String()
	kinds["context_compress"] = runtimeevents.KindAgentContextCompress.String()
	kinds["session_summarize"] = runtimeevents.KindAgentSessionSummarize.String()
	kinds["tool_exec_start"] = runtimeevents.KindAgentToolExecStart.String()
	kinds["tool_exec_end"] = runtimeevents.KindAgentToolExecEnd.String()
	kinds["tool_exec_skipped"] = runtimeevents.KindAgentToolExecSkipped.String()
	kinds["steering_injected"] = runtimeevents.KindAgentSteeringInjected.String()
	kinds["follow_up_queued"] = runtimeevents.KindAgentFollowUpQueued.String()
	kinds["interrupt_received"] = runtimeevents.KindAgentInterruptReceived.String()
	kinds["subturn_spawn"] = runtimeevents.KindAgentSubTurnSpawn.String()
	kinds["subturn_end"] = runtimeevents.KindAgentSubTurnEnd.String()
	kinds["subturn_result_delivered"] = runtimeevents.KindAgentSubTurnResultDelivered.String()
	kinds["subturn_orphan"] = runtimeevents.KindAgentSubTurnOrphan.String()
	kinds["error"] = runtimeevents.KindAgentError.String()
	return kinds
}
