package config

import (
	"path/filepath"
	"time"
)

// PluginsConfig controls installed Agent Plugins. Discovery never grants permission
// to execute: an entry must also be enabled in Entries.
type PluginsConfig struct {
	Enabled       bool                         `json:"enabled"`
	Directories   []string                     `json:"directories,omitempty"`
	DataDir       string                       `json:"data_dir,omitempty"`
	Concurrency   int                          `json:"concurrency,omitempty"`
	InitTimeoutMS int                          `json:"init_timeout_ms,omitempty"`
	CallTimeoutMS int                          `json:"call_timeout_ms,omitempty"`
	StartupWaitMS int                          `json:"startup_wait_ms,omitempty"`
	Entries       map[string]PluginEntryConfig `json:"entries,omitempty"`
}

type PluginEntryConfig struct {
	Enabled    bool   `json:"enabled"`
	Path       string `json:"path,omitempty"`
	AllowHooks bool   `json:"allow_hooks,omitempty"`
	// Agents nil grants access to all agents; an explicit empty list grants none.
	Agents []string          `json:"agents"`
	Config map[string]string `json:"config,omitempty"`
}

func (c PluginsConfig) Roots(workspace string) []string {
	if len(c.Directories) == 0 {
		return []string{filepath.Join(workspace, "plugins"), filepath.Join(GetHome(), "plugins")}
	}
	result := make([]string, 0, len(c.Directories))
	for _, path := range c.Directories {
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, path)
		}
		result = append(result, filepath.Clean(path))
	}
	return result
}

func (c PluginsConfig) DataRoot() string {
	if c.DataDir != "" {
		return c.DataDir
	}
	return filepath.Join(GetHome(), "plugin-data")
}

func (c PluginsConfig) Parallelism() int {
	if c.Concurrency > 0 && c.Concurrency <= 32 {
		return c.Concurrency
	}
	return 4
}

func pluginDuration(ms int, fallback time.Duration) time.Duration {
	if ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

func (c PluginsConfig) InitTimeout() time.Duration {
	return pluginDuration(c.InitTimeoutMS, 15*time.Second)
}
func (c PluginsConfig) CallTimeout() time.Duration {
	return pluginDuration(c.CallTimeoutMS, 60*time.Second)
}
func (c PluginsConfig) StartupWait() time.Duration {
	return pluginDuration(c.StartupWaitMS, 15*time.Second)
}
