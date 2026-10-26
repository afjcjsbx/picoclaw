package plugins

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Report is the static view of one package: what the host would discover,
// without starting any process or connecting to any MCP server.
type Report struct {
	Root     string // canonical package root
	Manifest PluginManifest
	Skills   []string // names the entry publishes, <id>:<skill>
	Servers  []string // MCP server names, after entry overrides
	Hooks    []string // published names, <id>:<hook>
	// Diagnostics are the same non-fatal findings the host reports at load time.
	Diagnostics []Diagnostic
}

// Validate checks the package at path the way Manager would load it, minus
// every side effect. The error is fatal only: an unreadable root or a manifest
// the host would reject. Component problems land in Report.Diagnostics.
//
// Hooks are always checked, regardless of entry.AllowHooks, so authors can lint
// them before granting the permission. id defaults to the manifest name.
//
// The component walk mirrors packagePlugin.Initialize; keep the two in step.
func Validate(id, path string, entry config.PluginEntryConfig) (Report, error) {
	var r Report
	root, err := canonicalRoot(path)
	if err != nil {
		return r, err
	}
	r.Root = root
	data, err := ReadPackageFile(root, "plugin.json")
	if err != nil {
		return r, fmt.Errorf("plugin.json: %w", err)
	}
	manifest, diagnostics, err := ParseManifest(data)
	r.Manifest = manifest
	if id == "" {
		id = manifest.Name
	}
	add := func(found ...Diagnostic) {
		for _, d := range found {
			d.Plugin = id
			r.Diagnostics = append(r.Diagnostics, d)
		}
	}
	report := func(component string, err error) { add(Diagnostic{Component: component, Message: err.Error()}) }
	add(diagnostics...)
	if err != nil {
		return r, err
	}

	skillEntries, found := discoverSkills(id, root)
	add(found...)
	for _, skill := range skillEntries {
		// Every skill above was linted; the entry may still publish only some.
		if entry.SkillNames == nil || slices.Contains(entry.SkillNames, strings.TrimPrefix(skill.Info.Name, id+":")) {
			r.Skills = append(r.Skills, skill.Info.Name)
		}
	}

	// Process hooks need a data directory to resolve ${PLUGIN_DATA}; a throwaway
	// one keeps validation from creating the real installation's directory.
	scratch, err := os.MkdirTemp("", "picoclaw-validate-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(scratch)
	if scratch, err = canonicalRoot(scratch); err != nil {
		return r, err
	}
	hooks, found := discoverHooks(manifest, PluginContext{
		ID: id, Root: root, DataDir: scratch, Config: entry.Config, AllowHooks: true,
	})
	add(found...)

	servers := map[string]ServerSpec{}
	if componentExists(root, "mcp.json") {
		data, err := ReadPackageFile(root, "mcp.json")
		if err != nil {
			report("mcp", err)
			return r, nil
		}
		parsed, found, err := ParseMCP(data)
		add(found...)
		if err != nil {
			report("mcp", err)
			return r, nil
		}
		servers = parsed
	}
	for _, name := range sortedKeys(entry.MCPOverrides) {
		spec, err := parseServerOverride(entry.MCPOverrides[name])
		if err != nil {
			report("mcp:"+name, err)
			delete(servers, name)
			continue
		}
		servers[name] = spec
	}
	r.Servers = sortedKeys(servers)

	for _, hook := range hooks {
		if hook.MCP != nil {
			if _, ok := servers[hook.MCP.Server]; !ok {
				report("hook:"+hook.Name, fmt.Errorf("MCP server %q is not configured", hook.MCP.Server))
				continue
			}
		}
		r.Hooks = append(r.Hooks, hook.Name)
	}
	return r, nil
}
