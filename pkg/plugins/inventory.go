package plugins

import (
	"path/filepath"
	"sort"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Installed describes one plugin the host knows about: a configured entry, or a
// package found under a plugin root that no entry activates.
type Installed struct {
	ID          string
	Name        string // manifest name, known once the plugin has been loaded
	Root        string
	State       string // runtime State, or "disabled", "pending" or "not enabled"
	Diagnostics int
}

// Inventory combines configured entries, packages on disk and runtime statuses.
// statuses comes from Manager.Statuses and may be nil when the host has not
// started. Inventory never enables a package.
func Inventory(cfg config.PluginsConfig, workspace string, statuses []Status) []Installed {
	candidates, _ := scanPackages(cfg, workspace)
	byID := map[string]*Installed{}
	claimed := map[string]bool{}

	for _, id := range sortedKeys(cfg.Entries) {
		entry := cfg.Entries[id]
		item := &Installed{ID: id, State: "disabled", Root: entryRoot(id, entry, workspace, candidates)}
		if entry.Enabled && cfg.Enabled {
			item.State = "pending"
		}
		byID[id] = item
		if item.Root != "" {
			claimed[item.Root] = true
		}
	}

	for _, status := range statuses {
		if status.ID == "" {
			continue
		}
		item, ok := byID[status.ID]
		if !ok {
			item = &Installed{ID: status.ID}
			byID[status.ID] = item
		}
		item.Name = status.Name
		if status.State != "" {
			item.State = string(status.State)
		}
		item.Diagnostics = len(status.Diagnostics)
		if status.Root != "" {
			item.Root = status.Root
			claimed[status.Root] = true
		}
	}

	var result []Installed
	for _, id := range sortedKeys(candidates) {
		if _, configured := byID[id]; configured {
			continue
		}
		for _, root := range candidates[id] {
			if claimed[root] {
				continue
			}
			result = append(result, Installed{ID: id, Root: root, State: "not enabled"})
		}
	}
	for _, item := range byID {
		result = append(result, *item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		return result[i].Root < result[j].Root
	})
	return result
}

// entryRoot resolves an entry's package directory without validating it; an
// unresolvable explicit path is reported as written.
func entryRoot(id string, entry config.PluginEntryConfig, workspace string, candidates map[string][]string) string {
	if entry.Path != "" {
		path := entry.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		if root, err := canonicalRoot(path); err == nil {
			return root
		}
		return filepath.Clean(path)
	}
	if roots := candidates[id]; len(roots) == 1 {
		return roots[0]
	}
	return ""
}
