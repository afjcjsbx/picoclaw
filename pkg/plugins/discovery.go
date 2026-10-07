package plugins

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/config"
)

type installation struct {
	id, root string
	entry    config.PluginEntryConfig
	err      error
}

func discover(cfg config.PluginsConfig, workspace string) ([]installation, []Diagnostic) {
	candidates := map[string][]string{}
	seenRoots := map[string]bool{}
	var diagnostics []Diagnostic
	for _, directory := range cfg.Roots(workspace) {
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Component: "discovery", Message: err.Error()})
			continue
		}
		for _, entry := range entries {
			root, err := canonicalRoot(filepath.Join(directory, entry.Name()))
			if err != nil || seenRoots[root] {
				continue
			}
			seenRoots[root] = true
			data, err := ReadPackageFile(root, "plugin.json")
			if err != nil {
				continue
			}
			manifest, _, err := ParseManifest(data)
			if err != nil {
				// An enabled entry matching the directory still receives a useful
				// manifest failure instead of a misleading "not found" report.
				candidates[entry.Name()] = append(candidates[entry.Name()], root)
				continue
			}
			candidates[manifest.Name] = append(candidates[manifest.Name], root)
		}
	}
	var result []installation
	used := map[string]bool{}
	for _, id := range sortedKeys(cfg.Entries) {
		entry := cfg.Entries[id]
		if !entry.Enabled {
			continue
		}
		item := installation{id: id, entry: entry}
		switch {
		case !ValidName(id):
			item.err = fmt.Errorf("invalid installation id %q", id)
		case entry.Path != "":
			path := entry.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(workspace, path)
			}
			item.root, item.err = canonicalRoot(path)
		case len(candidates[id]) == 1:
			item.root = candidates[id][0]
		case len(candidates[id]) > 1:
			item.err = fmt.Errorf("ambiguous plugin %q: configure an explicit path", id)
		default:
			item.err = fmt.Errorf("plugin %q was not found", id)
		}
		if item.err == nil {
			if used[item.root] {
				item.err = fmt.Errorf("plugin root is already enabled under another id")
			}
			used[item.root] = true
		}
		result = append(result, item)
	}
	return result, diagnostics
}
