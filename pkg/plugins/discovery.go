package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sipeed/picoclaw/pkg/config"
)

type DiscoveryRoot struct {
	Dir    string
	Source SourceKind
}

type DiscoveryOptions struct {
	Config      *config.Config
	BundledDir  string
	UserDir     string
	ProjectDirs []string
	ExtraDirs   []string
}

type Plugin struct {
	Key          string
	ManifestPath string
	Dir          string
	Source       SourceKind
	State        State
	Manifest     Manifest
	LoadError    error
}

type DiscoveryResult struct {
	Plugins  []Plugin
	Warnings []error
}

func DefaultDiscoveryOptions(cfg *config.Config) DiscoveryOptions {
	wd, _ := os.Getwd()
	bundledDir := strings.TrimSpace(os.Getenv(config.EnvBuiltinPlugins))
	if bundledDir == "" {
		bundledDir = filepath.Join(wd, "plugins")
	}

	userDir := filepath.Join(config.GetHome(), "plugins")
	workspace := ""
	if cfg != nil {
		workspace = cfg.WorkspacePath()
	}

	var projectDirs []string
	if projectPluginsEnabled(cfg) {
		if workspace != "" {
			projectDirs = append(projectDirs, filepath.Join(workspace, ".picoclaw", "plugins"))
		}
		if wd != "" {
			projectDirs = append(projectDirs, filepath.Join(wd, ".picoclaw", "plugins"))
		}
		projectDirs = uniqueCleanPaths(projectDirs)
	}

	extraDirs := append([]string(nil), cfgPluginDirectories(cfg)...)
	if envDirs := strings.TrimSpace(os.Getenv(config.EnvPluginDirs)); envDirs != "" {
		extraDirs = append(extraDirs, filepath.SplitList(envDirs)...)
	}

	return DiscoveryOptions{
		Config:      cfg,
		BundledDir:  bundledDir,
		UserDir:     userDir,
		ProjectDirs: projectDirs,
		ExtraDirs:   uniqueCleanPaths(extraDirs),
	}
}

func Discover(opts DiscoveryOptions) DiscoveryResult {
	roots := discoveryRoots(opts)
	byKey := make(map[string]Plugin)
	order := make([]string, 0)
	var warnings []error

	for _, root := range roots {
		plugins, errs := discoverRoot(root)
		warnings = append(warnings, errs...)
		for _, plugin := range plugins {
			plugin.State = resolveState(plugin, opts.Config)
			if _, exists := byKey[plugin.Key]; !exists {
				order = append(order, plugin.Key)
			}
			byKey[plugin.Key] = plugin
		}
	}

	out := make([]Plugin, 0, len(byKey))
	for _, key := range order {
		if plugin, ok := byKey[key]; ok {
			out = append(out, plugin)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return stateRank(out[i].State) < stateRank(out[j].State)
		}
		return out[i].Key < out[j].Key
	})

	return DiscoveryResult{Plugins: out, Warnings: warnings}
}

func (r DiscoveryResult) EnabledPlugins() []Plugin {
	out := make([]Plugin, 0, len(r.Plugins))
	for _, plugin := range r.Plugins {
		if plugin.State == StateEnabled && plugin.LoadError == nil {
			out = append(out, plugin)
		}
	}
	return out
}

func (r DiscoveryResult) Find(name string) (Plugin, bool) {
	key := normalizePluginLookup(name)
	for _, plugin := range r.Plugins {
		if normalizePluginLookup(plugin.Key) == key ||
			normalizePluginLookup(plugin.Manifest.Name) == key {
			return plugin, true
		}
	}
	return Plugin{}, false
}

func discoveryRoots(opts DiscoveryOptions) []DiscoveryRoot {
	var roots []DiscoveryRoot
	if strings.TrimSpace(opts.BundledDir) != "" {
		roots = append(roots, DiscoveryRoot{Dir: opts.BundledDir, Source: SourceBundled})
	}
	if strings.TrimSpace(opts.UserDir) != "" {
		roots = append(roots, DiscoveryRoot{Dir: opts.UserDir, Source: SourceUser})
	}
	for _, dir := range opts.ProjectDirs {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, DiscoveryRoot{Dir: dir, Source: SourceProject})
		}
	}
	for _, dir := range opts.ExtraDirs {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, DiscoveryRoot{Dir: dir, Source: SourceCustom})
		}
	}
	return roots
}

func discoverRoot(root DiscoveryRoot) ([]Plugin, []error) {
	dir := cleanPath(root.Dir)
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("scan plugin root %s: %w", dir, err)}
	}

	var out []Plugin
	var warnings []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childDir := filepath.Join(dir, entry.Name())
		if manifestPath := firstManifestPath(childDir); manifestPath != "" {
			plugin := loadPlugin(entry.Name(), childDir, manifestPath, root.Source)
			out = append(out, plugin)
			continue
		}

		nestedEntries, err := os.ReadDir(childDir)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("scan plugin category %s: %w", childDir, err))
			continue
		}
		for _, nested := range nestedEntries {
			if !nested.IsDir() {
				continue
			}
			nestedDir := filepath.Join(childDir, nested.Name())
			manifestPath := firstManifestPath(nestedDir)
			if manifestPath == "" {
				continue
			}
			key := entry.Name() + "/" + nested.Name()
			plugin := loadPlugin(key, nestedDir, manifestPath, root.Source)
			out = append(out, plugin)
		}
	}

	return out, warnings
}

func firstManifestPath(dir string) string {
	for _, name := range []string{ManifestYAML, ManifestYML, ManifestJSON} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func loadPlugin(key, dir, manifestPath string, source SourceKind) Plugin {
	plugin := Plugin{
		Key:          key,
		ManifestPath: manifestPath,
		Dir:          dir,
		Source:       source,
	}

	if err := validatePluginKey(key); err != nil {
		plugin.LoadError = err
		return plugin
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		plugin.LoadError = fmt.Errorf("read manifest: %w", err)
		return plugin
	}

	var manifest Manifest
	switch strings.ToLower(filepath.Ext(manifestPath)) {
	case ".json":
		err = json.Unmarshal(data, &manifest)
	default:
		err = yaml.Unmarshal(data, &manifest)
	}
	if err != nil {
		plugin.LoadError = fmt.Errorf("parse manifest: %w", err)
		return plugin
	}

	manifest.normalize(filepath.Base(dir))
	if err := validatePluginName(manifest.Name); err != nil {
		plugin.LoadError = err
		return plugin
	}
	plugin.Manifest = manifest
	return plugin
}

func resolveState(plugin Plugin, cfg *config.Config) State {
	if plugin.LoadError != nil {
		return StateDisabled
	}
	if stringSetContains(cfgDisabledPlugins(cfg), plugin.Key, plugin.Manifest.Name) {
		return StateDisabled
	}
	if stringSetContains(cfgEnabledPlugins(cfg), plugin.Key, plugin.Manifest.Name) {
		return StateEnabled
	}
	if plugin.Source == SourceBundled && plugin.Manifest.AutoEnable {
		return StateEnabled
	}
	return StateNotEnabled
}

func stateRank(state State) int {
	switch state {
	case StateEnabled:
		return 0
	case StateDisabled:
		return 2
	default:
		return 1
	}
}

func projectPluginsEnabled(cfg *config.Config) bool {
	if strings.TrimSpace(os.Getenv(config.EnvEnableProjectPlugins)) != "" {
		return parseBoolEnv(os.Getenv(config.EnvEnableProjectPlugins))
	}
	return cfg != nil && cfg.Plugins.ProjectEnabled
}

func cfgPluginDirectories(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.Plugins.Directories
}

func cfgEnabledPlugins(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.Plugins.Enabled
}

func cfgDisabledPlugins(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.Plugins.Disabled
}

func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on", "enabled":
		return true
	default:
		return false
	}
}

func stringSetContains(values []string, candidates ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = normalizePluginLookup(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	for _, candidate := range candidates {
		if _, ok := set[normalizePluginLookup(candidate)]; ok {
			return true
		}
	}
	return false
}

func normalizePluginLookup(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func cleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return filepath.Clean(path)
}

func uniqueCleanPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := cleanPath(path)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
