package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/plugins"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type pluginRuntime struct {
	mu      sync.Mutex
	binding *pluginBinding
	closed  bool
}

type pluginRegistration struct {
	registry *tools.ToolRegistry
	tool     tools.Tool
}
type pluginSkillRegistration struct {
	builder *ContextBuilder
	id      string
}
type pluginBinding struct {
	manager   *plugins.Manager
	mu        sync.Mutex
	tools     []pluginRegistration
	skills    []pluginSkillRegistration
	hooks     []string
	schedules map[string][]plugins.Schedule
}

func pluginAllowsAgent(entry config.PluginEntryConfig, id string) bool {
	return entry.Agents == nil || slices.Contains(entry.Agents, id)
}

// pluginScheduleAgent chooses the agent that runs a plugin's schedules.
//
// A nil Agents list leaves routing to the default channel route. An explicit
// list is a permission boundary: schedules run only when one of the listed
// agents can be selected unambiguously. With multiple allowed agents the
// default agent is used when it is in the list; otherwise the host cannot pick
// a safe target and the declarations are not installed.
func pluginScheduleAgent(entry config.PluginEntryConfig, registry *AgentRegistry) (string, bool) {
	if registry == nil {
		return "", false
	}
	if entry.Agents == nil {
		return "", true
	}
	allowed := make([]string, 0, len(entry.Agents))
	seen := make(map[string]struct{}, len(entry.Agents))
	for _, id := range entry.Agents {
		agent, ok := registry.GetAgent(id)
		if !ok {
			continue
		}
		if _, duplicate := seen[agent.ID]; duplicate {
			continue
		}
		seen[agent.ID] = struct{}{}
		allowed = append(allowed, agent.ID)
	}
	switch len(allowed) {
	case 0:
		return "", false
	case 1:
		return allowed[0], true
	}
	if fallback := registry.GetDefaultAgent(); fallback != nil {
		if _, ok := seen[fallback.ID]; ok {
			return fallback.ID, true
		}
	}
	return "", false
}

// pluginRelativePath rewrites an absolute skill path to a plugin-root-relative,
// slash-separated path. It returns "" when the path is empty, is not under the
// root, or does not yield a clean relative path, so a host path is never
// exposed to the catalog.
func pluginRelativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}

func pluginToolDeferred(discovery config.ToolDiscoveryConfig, override *bool) bool {
	if !discovery.Enabled || (!discovery.UseBM25 && !discovery.UseRegex) {
		return false
	}
	if override != nil {
		return *override
	}
	return true
}

func (al *AgentLoop) startPlugins() *plugins.Manager {
	al.plugins.mu.Lock()
	defer al.plugins.mu.Unlock()
	if al.plugins.closed {
		return nil
	}
	if al.plugins.binding != nil {
		return al.plugins.binding.manager
	}
	al.mu.RLock()
	cfg, registry := al.cfg, al.registry
	al.mu.RUnlock()
	if !cfg.Plugins.Enabled {
		return nil
	}
	binding := &pluginBinding{schedules: map[string][]plugins.Schedule{}}
	binding.manager = plugins.NewManager(
		cfg.Plugins,
		cfg.WorkspacePath(),
		func(ctx context.Context, capabilities plugins.Capabilities) []plugins.Diagnostic {
			return al.publishPlugin(ctx, binding, registry, cfg, capabilities)
		},
	)
	al.plugins.binding = binding
	binding.manager.Start()
	return binding.manager
}

func (al *AgentLoop) waitPlugins(ctx context.Context) {
	manager := al.startPlugins()
	if manager == nil {
		return
	}
	al.mu.RLock()
	timeout := al.cfg.Plugins.StartupWait()
	al.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := manager.Wait(ctx); err != nil {
		logger.DebugCF(
			"plugins",
			"Plugin loading continues in background",
			map[string]any{"reason": err.Error()},
		)
	}
}

// WaitPlugins waits for the current plugin discovery pass. The returned error
// is nil only when every configured plugin finished loading, so callers can
// decide whether it is safe to prune declarations that were not observed.
func (al *AgentLoop) WaitPlugins(ctx context.Context) error {
	manager := al.startPlugins()
	if manager == nil {
		return nil
	}
	al.mu.RLock()
	timeout := al.cfg.Plugins.StartupWait()
	al.mu.RUnlock()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return manager.Wait(waitCtx)
}

// WaitPluginsReady waits for the current plugin discovery pass without
// applying the startup wait. Callers control the deadline through ctx.
func (al *AgentLoop) WaitPluginsReady(ctx context.Context) error {
	manager := al.startPlugins()
	if manager == nil {
		return nil
	}
	return manager.Wait(ctx)
}

// PluginScheduleDeclarations returns the schedules published by loaded
// plugins, converted to host-owned cron declarations.
func (al *AgentLoop) PluginScheduleDeclarations() []cron.CronJobDeclaration {
	if al == nil {
		return nil
	}
	al.plugins.mu.Lock()
	defer al.plugins.mu.Unlock()
	binding := al.plugins.binding
	if binding == nil {
		return nil
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	var declarations []cron.CronJobDeclaration
	for _, schedules := range binding.schedules {
		for _, schedule := range schedules {
			declarations = append(declarations, schedule.Declaration())
		}
	}
	sort.Slice(declarations, func(i, j int) bool {
		return declarations[i].DeclarationKey < declarations[j].DeclarationKey
	})
	return declarations
}

func (al *AgentLoop) publishPlugin(
	ctx context.Context,
	binding *pluginBinding,
	registry *AgentRegistry,
	cfg *config.Config,
	capabilities plugins.Capabilities,
) []plugins.Diagnostic {
	var diagnostics []plugins.Diagnostic
	report := func(component string, err error) {
		diagnostics = append(
			diagnostics,
			plugins.Diagnostic{Plugin: capabilities.ID, Component: component, Message: err.Error()},
		)
	}
	if ctx.Err() != nil {
		return nil
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	if capabilities.Schedules != nil {
		schedules := append([]plugins.Schedule(nil), capabilities.Schedules...)
		if len(schedules) > 0 {
			agentID, ok := pluginScheduleAgent(capabilities.Entry, registry)
			if !ok {
				report("schedules", fmt.Errorf("no allowed agent can run the plugin's schedules"))
				schedules = nil
			} else {
				for i := range schedules {
					schedules[i].AgentID = agentID
				}
			}
		}
		if schedules == nil {
			delete(binding.schedules, capabilities.ID)
		} else {
			binding.schedules[capabilities.ID] = schedules
		}
	}
	mcpToolsByAgent := make(map[string]map[string]*plugins.PluginTool)
	for _, id := range registry.ListAgentIDs() {
		if !pluginAllowsAgent(capabilities.Entry, id) {
			continue
		}
		agent, ok := registry.GetAgent(id)
		if !ok {
			continue
		}
		register := func(tool tools.Tool, hidden bool) bool {
			added, err := agent.Tools.RegisterUnique(tool, hidden)
			if err != nil {
				report("tool:"+tool.Name(), err)
			}
			if added {
				binding.tools = append(binding.tools, pluginRegistration{agent.Tools, tool})
			}
			return added
		}
		if capabilities.HasResources {
			resource := &plugins.ResourceTool{
				ID:       capabilities.ID,
				Root:     capabilities.Root,
				Lifetime: ctx,
			}
			register(resource, false)
			if len(capabilities.Skills) > 0 {
				entries := append([]skills.PluginSkill(nil), capabilities.Skills...)
				for i := range entries {
					entries[i].Info.ResourceTool = resource.Name()
					entries[i].Info.Path = pluginRelativePath(
						capabilities.Root,
						entries[i].Info.Path,
					)
					entries[i].Body = fmt.Sprintf(
						"Read bundled plugin references with the `%s` tool using a path relative to the plugin root (for example `skills/greet/references/help.md`). Do not use filesystem tools on plugin files; they live outside the workspace.\n\n%s",
						resource.Name(),
						entries[i].Body,
					)
				}
				agent.ContextBuilder.skillsLoader.SetPluginSkills(capabilities.ID, entries)
				agent.ContextBuilder.InvalidateCache()
				binding.skills = append(
					binding.skills,
					pluginSkillRegistration{agent.ContextBuilder, capabilities.ID},
				)
			}
		}
		registeredMCP := false
		mcpTools := map[string]*plugins.PluginTool{}
		for _, tool := range capabilities.Tools {
			// Agent mcpServers declarations use the installation-qualified server.
			if !agent.AllowsMCPServer(capabilities.ID + ":" + tool.Server) {
				continue
			}
			hidden := pluginToolDeferred(cfg.Tools.MCP.Discovery, tool.Deferred)
			agentTool := tool.ForAgent(
				agent.Workspace,
				cfg.Tools.MCP.GetMaxInlineTextChars(),
				al.runtimeEvents,
			)
			if register(agentTool, hidden) {
				registeredMCP = true
				mcpTools[tool.Server+"\x00"+tool.RemoteName()] = agentTool
			}
		}
		mcpToolsByAgent[id] = mcpTools
		if registeredMCP && cfg.Tools.MCP.Discovery.Enabled {
			agent.ContextBuilder.WithToolDiscovery(
				cfg.Tools.MCP.Discovery.UseBM25,
				cfg.Tools.MCP.Discovery.UseRegex,
			)
			d := cfg.Tools.MCP.Discovery
			ttl, limit := d.TTL, d.MaxSearchResults
			if ttl <= 0 {
				ttl = 5
			}
			if limit <= 0 {
				limit = 5
			}
			// Search tools are shared agent infrastructure; existing ones win.
			if d.UseRegex {
				_, _ = agent.Tools.RegisterUnique(
					tools.NewRegexSearchTool(agent.Tools, ttl, limit),
					false,
				)
			}
			if d.UseBM25 {
				_, _ = agent.Tools.RegisterUnique(
					tools.NewBM25SearchTool(agent.Tools, ttl, limit),
					false,
				)
			}
		}
	}
	for _, hook := range capabilities.Hooks {
		if ctx.Err() != nil {
			break
		}
		if hook.MCP != nil {
			observeKinds, observeEnabled, err := processHookObserveKindsFromConfig(hook.Observe)
			if err != nil {
				report("hook:"+hook.Name, err)
				continue
			}
			observeAll := observeEnabled && len(observeKinds) == 0
			for _, id := range registry.ListAgentIDs() {
				if !pluginAllowsAgent(capabilities.Entry, id) {
					continue
				}
				tool := mcpToolsByAgent[id][hook.MCP.Server+"\x00"+hook.MCP.Tool]
				if tool == nil {
					report(
						"hook:"+hook.Name,
						fmt.Errorf(
							"MCP tool %q on server %q is unavailable or not allowed for agent %q",
							hook.MCP.Tool,
							hook.MCP.Server,
							id,
						),
					)
					continue
				}
				name := "plugin:" + hook.Name + ":" + id
				registration := HookRegistration{
					Name:            name,
					Source:          HookSourceInProcess,
					ObserverTimeout: pluginMCPHookTimeout,
					Hook: newPluginMCPHook(
						al,
						capabilities.ID,
						id,
						capabilities.Entry.Agents,
						hook,
						tool,
						cfg,
						observeKinds,
						observeAll,
					),
				}
				if err := al.MountHook(registration); err != nil {
					report("hook:"+hook.Name, err)
					continue
				}
				binding.hooks = append(binding.hooks, name)
			}
			continue
		}
		observeKinds, observe, err := processHookObserveKindsFromConfig(hook.Observe)
		if err != nil {
			report("hook:"+hook.Name, err)
			continue
		}
		initCtx, cancel := context.WithTimeout(ctx, cfg.Plugins.InitTimeout())
		opts := ProcessHookOptions{
			Command:       hook.Command,
			Dir:           hook.Dir,
			Env:           hook.Env,
			ExactEnv:      true,
			Config:        hook.Config,
			Observe:       observe,
			ObserveKinds:  observeKinds,
			CompletedOnly: slices.Contains(hook.Observe, "turn_completed"),
			InterceptLLM: slices.Contains(hook.Intercept, "before_llm") ||
				slices.Contains(hook.Intercept, "after_llm"),
			InterceptTool: slices.Contains(hook.Intercept, "before_tool") ||
				slices.Contains(hook.Intercept, "after_tool"),
			ApproveTool: slices.Contains(hook.Intercept, "approve_tool"),
		}
		process, err := NewProcessHook(initCtx, "plugin:"+hook.Name, opts)
		cancel()
		if err != nil {
			report("hook:"+hook.Name, err)
			continue
		}
		scoped := &pluginHook{
			ProcessHook: process,
			agents:      capabilities.Entry.Agents,
			stages:      hook.Intercept,
		}
		name := "plugin:" + hook.Name
		if err := al.MountHook(HookRegistration{Name: name, Source: HookSourceProcess, Hook: scoped}); err != nil {
			_ = process.Close()
			report("hook:"+hook.Name, err)
			continue
		}
		binding.hooks = append(binding.hooks, name)
	}
	return diagnostics
}

func (al *AgentLoop) closePlugins(final bool) {
	al.plugins.mu.Lock()
	defer al.plugins.mu.Unlock()
	if final {
		al.plugins.closed = true
	}
	binding := al.plugins.binding
	al.plugins.binding = nil
	if binding == nil {
		return
	}
	_ = binding.manager.Close() // cancels loads/calls and joins publishers
	for _, name := range binding.hooks {
		al.UnmountHook(name)
	}
	for _, entry := range binding.tools {
		entry.registry.UnregisterOwned(entry.tool)
	}
	for _, entry := range binding.skills {
		entry.builder.skillsLoader.SetPluginSkills(entry.id, nil)
		entry.builder.InvalidateCache()
	}
}

// PluginStatuses exposes readiness and component diagnostics to host UIs/tests.
func (al *AgentLoop) PluginStatuses() []plugins.Status {
	al.plugins.mu.Lock()
	defer al.plugins.mu.Unlock()
	if al.plugins.binding == nil {
		return nil
	}
	return al.plugins.binding.manager.Statuses()
}
