package plugins

import (
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/skills"
)

// SkillPackage is the set of skills one enabled plugin publishes. Root is the
// canonical package directory, while each Skill.Info.Path is absolute.
type SkillPackage struct {
	ID     string
	Name   string
	Root   string
	Skills []skills.PluginSkill
}

// PublishedSkills reports the skills enabled plugin entries publish without
// starting plugin processes or connecting to MCP servers. It mirrors the
// discovery Manager performs before publishing capabilities, including the
// entry.SkillNames filter, so host UIs can list plugin-backed skills.
func PublishedSkills(cfg config.PluginsConfig, workspace string) ([]SkillPackage, []Diagnostic) {
	if !cfg.Enabled {
		return nil, nil
	}
	installations, diagnostics := discover(cfg, workspace)
	result := make([]SkillPackage, 0, len(installations))
	for _, installation := range installations {
		if installation.err != nil {
			continue
		}
		data, err := ReadPackageFile(installation.root, "plugin.json")
		if err != nil {
			diagnostics = append(
				diagnostics,
				Diagnostic{Plugin: installation.id, Component: "manifest", Message: err.Error()},
			)
			continue
		}
		manifest, found, err := ParseManifest(data)
		for _, d := range found {
			d.Plugin = installation.id
			diagnostics = append(diagnostics, d)
		}
		if err != nil {
			diagnostics = append(
				diagnostics,
				Diagnostic{Plugin: installation.id, Component: "manifest", Message: err.Error()},
			)
			continue
		}
		entries, found := discoverSkills(installation.id, installation.root)
		diagnostics = append(diagnostics, found...)
		entries = filterPublishedSkills(installation.id, installation.entry.SkillNames, entries)
		if len(entries) == 0 {
			continue
		}
		result = append(result, SkillPackage{
			ID:     installation.id,
			Name:   manifest.Name,
			Root:   installation.root,
			Skills: entries,
		})
	}
	return result, diagnostics
}

// filterPublishedSkills applies entry.SkillNames to discovered skills. A nil
// list keeps every skill; an explicit empty list keeps none.
func filterPublishedSkills(id string, names []string, entries []skills.PluginSkill) []skills.PluginSkill {
	if names == nil {
		return entries
	}
	kept := make([]skills.PluginSkill, 0, len(entries))
	for _, skill := range entries {
		for _, name := range names {
			if skill.Info.Name == id+":"+name {
				kept = append(kept, skill)
				break
			}
		}
	}
	return kept
}

func discoverSkills(id, root string) ([]skills.PluginSkill, []Diagnostic) {
	if !componentExists(root, "skills") {
		return nil, nil
	}
	var result []skills.PluginSkill
	var diagnostics []Diagnostic
	report := func(component string, err error) {
		diagnostics = append(diagnostics, Diagnostic{Plugin: id, Component: component, Message: err.Error()})
	}
	dir, err := ResolvePath(root, "skills")
	if err != nil {
		report("skills", err)
		return result, diagnostics
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		report("skills", err)
		return result, diagnostics
	}
	for _, entry := range entries {
		path := filepath.Join(root, "skills", entry.Name(), "SKILL.md")
		// Stat the child directory to support links that remain inside the root.
		child, err := ResolvePath(root, filepath.Join("skills", entry.Name()))
		if err != nil {
			report("skill:"+entry.Name(), err)
			continue
		}
		info, err := os.Stat(child)
		if err != nil || !info.IsDir() {
			continue
		}
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			continue
		}
		content, err := ReadPackageFile(root, path)
		if err != nil {
			report("skill:"+entry.Name(), err)
			continue
		}
		skill, err := skills.ParsePluginSkill(id, path, content)
		if err != nil {
			report("skill:"+entry.Name(), err)
			continue
		}
		result = append(result, skill)
	}
	return result, diagnostics
}
