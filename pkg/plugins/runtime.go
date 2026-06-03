package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type Runtime struct {
	discovery DiscoveryResult
	plugins   []Plugin
}

func LoadRuntime(cfg *config.Config) *Runtime {
	result := Discover(DefaultDiscoveryOptions(cfg))
	return NewRuntime(result)
}

func NewRuntime(result DiscoveryResult) *Runtime {
	return &Runtime{
		discovery: result,
		plugins:   result.EnabledPlugins(),
	}
}

func (r *Runtime) Discovery() DiscoveryResult {
	if r == nil {
		return DiscoveryResult{}
	}
	return r.discovery
}

func (r *Runtime) EnabledPlugins() []Plugin {
	if r == nil {
		return nil
	}
	out := make([]Plugin, len(r.plugins))
	copy(out, r.plugins)
	return out
}

func (r *Runtime) ApplyToConfig(cfg *config.Config) {
	if r == nil || cfg == nil {
		return
	}

	for _, plugin := range r.plugins {
		for hookName, hook := range plugin.Manifest.Hooks.Processes {
			spec := processHookConfigFromManifest(hook)
			if !spec.Enabled {
				continue
			}
			spec.Dir = resolvePluginPath(plugin.Dir, spec.Dir)
			spec.Command = resolveCommand(plugin.Dir, spec.Command)
			spec.Env = mergeEnv(pluginEnv(plugin), spec.Env)
			name := scopedName(plugin, hookName)

			if cfg.Hooks.Processes == nil {
				cfg.Hooks.Processes = make(map[string]config.ProcessHookConfig)
			}
			cfg.Hooks.Processes[name] = spec
		}

		for serverName, server := range plugin.Manifest.MCPServers {
			spec := mcpServerConfigFromManifest(server)
			if !spec.Enabled {
				continue
			}
			spec.Command = resolveExecutable(plugin.Dir, spec.Command)
			spec.EnvFile = resolvePluginPath(plugin.Dir, spec.EnvFile)
			spec.Env = mergeEnv(pluginEnv(plugin), spec.Env)
			name := scopedName(plugin, serverName)

			cfg.Tools.MCP.Enabled = true
			if cfg.Tools.MCP.Servers == nil {
				cfg.Tools.MCP.Servers = make(map[string]config.MCPServerConfig)
			}
			cfg.Tools.MCP.Servers[name] = spec
		}
	}
}

func (r *Runtime) ConfigWithPlugins(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	clone.Hooks = cfg.Hooks
	if cfg.Hooks.Builtins != nil {
		clone.Hooks.Builtins = make(map[string]config.BuiltinHookConfig, len(cfg.Hooks.Builtins))
		for name, spec := range cfg.Hooks.Builtins {
			clone.Hooks.Builtins[name] = spec
		}
	}
	if cfg.Hooks.Processes != nil {
		clone.Hooks.Processes = make(map[string]config.ProcessHookConfig, len(cfg.Hooks.Processes))
		for name, spec := range cfg.Hooks.Processes {
			clone.Hooks.Processes[name] = spec
		}
	}
	clone.Tools = cfg.Tools
	clone.Tools.MCP = cfg.Tools.MCP
	if cfg.Tools.MCP.Servers != nil {
		clone.Tools.MCP.Servers = make(map[string]config.MCPServerConfig, len(cfg.Tools.MCP.Servers))
		for name, spec := range cfg.Tools.MCP.Servers {
			clone.Tools.MCP.Servers[name] = spec
		}
	}
	r.ApplyToConfig(&clone)
	return &clone
}

func (r *Runtime) ToolInstances() []tools.Tool {
	if r == nil {
		return nil
	}
	var out []tools.Tool
	for _, plugin := range r.plugins {
		for _, spec := range plugin.Manifest.Tools {
			if strings.TrimSpace(spec.Name) == "" || len(spec.Command) == 0 {
				continue
			}
			out = append(out, NewCommandTool(plugin, spec))
		}
	}
	return out
}

func (r *Runtime) SkillInfos() []skills.SkillInfo {
	if r == nil {
		return nil
	}
	var out []skills.SkillInfo
	for _, plugin := range r.plugins {
		for _, spec := range plugin.Manifest.Skills {
			info, ok := skillInfoFromManifest(plugin, spec)
			if ok {
				out = append(out, info)
			}
		}
		if len(plugin.Manifest.Skills) == 0 {
			out = append(out, discoverBundledSkills(plugin)...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *Runtime) CommandDefinitions() []commands.Definition {
	defs := []commands.Definition{r.pluginsCommandDefinition()}
	if r == nil {
		return defs
	}
	for _, plugin := range r.plugins {
		for _, spec := range plugin.Manifest.Commands {
			def, ok := commandDefinitionFromManifest(plugin, spec)
			if ok {
				defs = append(defs, def)
			}
		}
	}
	return defs
}

func (r *Runtime) pluginsCommandDefinition() commands.Definition {
	return commands.Definition{
		Name:        "plugins",
		Description: "Show loaded and discovered plugins",
		Usage:       "/plugins [list|show <name>]",
		Handler: func(ctx context.Context, req commands.Request, rt *commands.Runtime) error {
			switch strings.ToLower(commandNthArg(req.Text, 1)) {
			case "", "list":
				return req.Reply(r.formatPluginList())
			case "show":
				name := commandNthArg(req.Text, 2)
				if strings.TrimSpace(name) == "" {
					return req.Reply("Usage: /plugins show <name>")
				}
				return req.Reply(r.formatPluginDetails(name))
			default:
				return req.Reply("Usage: /plugins [list|show <name>]")
			}
		},
	}
}

func (r *Runtime) formatPluginList() string {
	if r == nil || len(r.discovery.Plugins) == 0 {
		return "No plugins discovered."
	}
	lines := []string{"Plugins:"}
	for _, plugin := range r.discovery.Plugins {
		desc := strings.TrimSpace(plugin.Manifest.Description)
		if desc != "" {
			desc = " - " + desc
		}
		if plugin.LoadError != nil {
			desc = " - error: " + plugin.LoadError.Error()
		}
		lines = append(lines, fmt.Sprintf("- %s [%s, %s]%s", plugin.Key, plugin.State, plugin.Source, desc))
	}
	return strings.Join(lines, "\n")
}

func (r *Runtime) formatPluginDetails(name string) string {
	if r == nil {
		return "Plugin runtime is not initialized."
	}
	plugin, ok := r.discovery.Find(name)
	if !ok {
		return fmt.Sprintf("Plugin %q was not found.", name)
	}
	if plugin.LoadError != nil {
		return fmt.Sprintf("Plugin %s failed to load: %v", plugin.Key, plugin.LoadError)
	}
	lines := []string{
		fmt.Sprintf("Plugin: %s", plugin.Key),
		fmt.Sprintf("Name: %s", plugin.Manifest.Name),
		fmt.Sprintf("State: %s", plugin.State),
		fmt.Sprintf("Source: %s", plugin.Source),
		fmt.Sprintf("Path: %s", plugin.Dir),
	}
	if plugin.Manifest.Version != "" {
		lines = append(lines, "Version: "+plugin.Manifest.Version)
	}
	if plugin.Manifest.Description != "" {
		lines = append(lines, "Description: "+plugin.Manifest.Description)
	}
	if len(plugin.Manifest.RequiresEnv) > 0 {
		lines = append(lines, "Requires env: "+strings.Join(plugin.Manifest.RequiresEnv, ", "))
	}
	if len(plugin.Manifest.Tools) > 0 {
		lines = append(lines, fmt.Sprintf("Tools: %d", len(plugin.Manifest.Tools)))
	}
	if len(plugin.Manifest.Commands) > 0 {
		lines = append(lines, fmt.Sprintf("Commands: %d", len(plugin.Manifest.Commands)))
	}
	if len(plugin.Manifest.Hooks.Processes) > 0 {
		lines = append(lines, fmt.Sprintf("Process hooks: %d", len(plugin.Manifest.Hooks.Processes)))
	}
	if len(plugin.Manifest.MCPServers) > 0 {
		lines = append(lines, fmt.Sprintf("MCP servers: %d", len(plugin.Manifest.MCPServers)))
	}
	if skills := r.pluginSkillNames(plugin); len(skills) > 0 {
		lines = append(lines, "Skills: "+strings.Join(skills, ", "))
	}
	return strings.Join(lines, "\n")
}

func (r *Runtime) pluginSkillNames(plugin Plugin) []string {
	var out []string
	for _, info := range r.SkillInfos() {
		if strings.EqualFold(info.Source, pluginSkillSource(plugin)) {
			out = append(out, info.Name)
		}
	}
	return out
}

func MergeCommandDefinitions(base, extra []commands.Definition) []commands.Definition {
	out := make([]commands.Definition, 0, len(base)+len(extra))
	seen := make(map[string]struct{})
	add := func(def commands.Definition) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, def)
	}
	for _, def := range base {
		add(def)
	}
	for _, def := range extra {
		add(def)
	}
	return out
}

func skillInfoFromManifest(plugin Plugin, spec SkillManifest) (skills.SkillInfo, bool) {
	name := strings.TrimSpace(spec.Name)
	path := resolvePluginPath(plugin.Dir, spec.Path)
	if name == "" || path == "" {
		return skills.SkillInfo{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return skills.SkillInfo{}, false
	}
	return skills.SkillInfo{
		Name:   plugin.Manifest.Name + ":" + name,
		Path:   path,
		Source: pluginSkillSource(plugin),
	}, true
}

func discoverBundledSkills(plugin Plugin) []skills.SkillInfo {
	root := filepath.Join(plugin.Dir, "skills")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]skills.SkillInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(root, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err != nil {
			continue
		}
		out = append(out, skills.SkillInfo{
			Name:   plugin.Manifest.Name + ":" + entry.Name(),
			Path:   skillFile,
			Source: pluginSkillSource(plugin),
		})
	}
	return out
}

func pluginSkillSource(plugin Plugin) string {
	return "plugin:" + plugin.Key
}

func scopedName(plugin Plugin, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = plugin.Manifest.Name
	}
	return plugin.Key + "/" + name
}

func pluginEnv(plugin Plugin) map[string]string {
	return map[string]string{
		"PICOCLAW_PLUGIN_KEY":  plugin.Key,
		"PICOCLAW_PLUGIN_NAME": plugin.Manifest.Name,
		"PICOCLAW_PLUGIN_DIR":  plugin.Dir,
	}
}

func mergeEnv(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	out := cloneStringMap(base)
	if out == nil {
		out = make(map[string]string, len(overlay))
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func resolveCommand(pluginDir string, command []string) []string {
	if len(command) == 0 {
		return nil
	}
	out := append([]string(nil), command...)
	out[0] = resolveExecutable(pluginDir, out[0])
	return out
}

func resolveExecutable(pluginDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if shouldResolvePluginPath(value) {
		return resolvePluginPath(pluginDir, value)
	}
	return value
}

func resolvePluginPath(pluginDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return value
	}
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(value, "~/"))
		}
	}
	return filepath.Join(pluginDir, value)
}

func shouldResolvePluginPath(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, ".") {
		return true
	}
	return strings.ContainsAny(value, `/\`)
}

func commandNthArg(text string, n int) string {
	parts := strings.Fields(strings.TrimSpace(text))
	if n >= len(parts) {
		return ""
	}
	return parts[n]
}
