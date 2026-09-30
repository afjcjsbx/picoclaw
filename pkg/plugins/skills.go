package plugins

import (
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/skills"
)

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
		if _, err := os.Lstat(path); os.IsNotExist(err) {
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
