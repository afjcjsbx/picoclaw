package agent

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
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
	manager *plugins.Manager
	mu      sync.Mutex
	tools   []pluginRegistration
	skills  []pluginSkillRegistration
	hooks   []string
}

func pluginAllowsAgent(entry config.PluginEntryConfig, id string) bool {
	return entry.Agents == nil || slices.Contains(entry.Agents, id)
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
	binding := &pluginBinding{}
	binding.manager = plugins.NewManager(cfg.Plugins, cfg.WorkspacePath(), func(ctx context.Context, cap plugins.Capabilities) []plugins.Diagnostic {
		return al.publishPlugin(ctx, binding, registry, cfg, cap)
	})
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
		logger.DebugCF("plugins", "Plugin loading continues in background", map[string]any{"reason": err.Error()})
	}
}

func (al *AgentLoop) publishPlugin(ctx context.Context, binding *pluginBinding, registry *AgentRegistry, cfg *config.Config, cap plugins.Capabilities) []plugins.Diagnostic {
	var diagnostics []plugins.Diagnostic
	report := func(component string, err error) {
		diagnostics = append(diagnostics, plugins.Diagnostic{Plugin: cap.ID, Component: component, Message: err.Error()})
	}
	if ctx.Err() != nil {
		return nil
	}
	binding.mu.Lock()
	defer binding.mu.Unlock()
	for _, id := range registry.ListAgentIDs() {
		if !pluginAllowsAgent(cap.Entry, id) {
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
		if len(cap.Skills) > 0 {
			resource := &plugins.ResourceTool{ID: cap.ID, Root: cap.Root, Lifetime: ctx}
			register(resource, false)
			entries := append([]skills.PluginSkill(nil), cap.Skills...)
			for i := range entries {
				entries[i].Body = fmt.Sprintf("Plugin root: %s\nRead bundled references with `%s` using a plugin-relative path.\n\n%s", cap.Root, resource.Name(), entries[i].Body)
			}
			agent.ContextBuilder.skillsLoader.SetPluginSkills(cap.ID, entries)
			agent.ContextBuilder.InvalidateCache()
			binding.skills = append(binding.skills, pluginSkillRegistration{agent.ContextBuilder, cap.ID})
		}
		registeredMCP := false
		for _, tool := range cap.Tools {
			// Agent mcpServers declarations use the installation-qualified server.
			if !agent.AllowsMCPServer(cap.ID + ":" + tool.Server) {
				continue
			}
			hidden := cfg.Tools.MCP.Discovery.Enabled && (cfg.Tools.MCP.Discovery.UseBM25 || cfg.Tools.MCP.Discovery.UseRegex)
			if register(tool.ForAgent(agent.Workspace, cfg.Tools.MCP.GetMaxInlineTextChars(), al.runtimeEvents), hidden) {
				registeredMCP = true
			}
		}
		if registeredMCP && cfg.Tools.MCP.Discovery.Enabled {
			agent.ContextBuilder.WithToolDiscovery(cfg.Tools.MCP.Discovery.UseBM25, cfg.Tools.MCP.Discovery.UseRegex)
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
				_, _ = agent.Tools.RegisterUnique(tools.NewRegexSearchTool(agent.Tools, ttl, limit), false)
			}
			if d.UseBM25 {
				_, _ = agent.Tools.RegisterUnique(tools.NewBM25SearchTool(agent.Tools, ttl, limit), false)
			}
		}
	}
	for _, hook := range cap.Hooks {
		if ctx.Err() != nil {
			break
		}
		initCtx, cancel := context.WithTimeout(ctx, cfg.Plugins.InitTimeout())
		opts := ProcessHookOptions{Command: hook.Command, Dir: hook.Dir, Env: hook.Env, ExactEnv: true, Config: hook.Config,
			Observe: len(hook.Observe) > 0, ObserveKinds: hook.Observe,
			InterceptLLM:  slices.Contains(hook.Intercept, "before_llm") || slices.Contains(hook.Intercept, "after_llm"),
			InterceptTool: slices.Contains(hook.Intercept, "before_tool") || slices.Contains(hook.Intercept, "after_tool"),
			ApproveTool:   slices.Contains(hook.Intercept, "approve_tool")}
		process, err := NewProcessHook(initCtx, "plugin:"+hook.Name, opts)
		cancel()
		if err != nil {
			report("hook:"+hook.Name, err)
			continue
		}
		scoped := &pluginHook{ProcessHook: process, agents: cap.Entry.Agents, stages: hook.Intercept}
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
