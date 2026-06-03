package plugins

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

const (
	ManifestYAML = "plugin.yaml"
	ManifestYML  = "plugin.yml"
	ManifestJSON = "plugin.json"
)

var (
	pluginNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]+([._-][a-zA-Z0-9]+)*$`)
	pluginKeyPattern  = regexp.MustCompile(`^[a-zA-Z0-9]+([._-][a-zA-Z0-9]+)*(\/[a-zA-Z0-9]+([._-][a-zA-Z0-9]+)*)?$`)
)

type SourceKind string

const (
	SourceBundled SourceKind = "bundled"
	SourceUser    SourceKind = "user"
	SourceProject SourceKind = "project"
	SourceCustom  SourceKind = "custom"
)

type State string

const (
	StateEnabled    State = "enabled"
	StateDisabled   State = "disabled"
	StateNotEnabled State = "not enabled"
)

type Manifest struct {
	Name        string `json:"name"         yaml:"name"`
	Version     string `json:"version"      yaml:"version"`
	Description string `json:"description"  yaml:"description"`
	Kind        string `json:"kind"         yaml:"kind"`
	AutoEnable  bool   `json:"auto_enable"  yaml:"auto_enable"`

	RequiresEnv []string                     `json:"requires_env,omitempty" yaml:"requires_env,omitempty"`
	Hooks       PluginHooksManifest          `json:"hooks,omitempty"        yaml:"hooks,omitempty"`
	MCPServers  map[string]MCPServerManifest `json:"mcp_servers,omitempty"  yaml:"mcp_servers,omitempty"`
	Tools       []ToolManifest               `json:"tools,omitempty"        yaml:"tools,omitempty"`
	Commands    []CommandManifest            `json:"commands,omitempty"     yaml:"commands,omitempty"`
	Skills      []SkillManifest              `json:"skills,omitempty"       yaml:"skills,omitempty"`
}

type PluginHooksManifest struct {
	Processes map[string]ProcessHookManifest `json:"processes,omitempty" yaml:"processes,omitempty"`
}

type ProcessHookManifest struct {
	Enabled   *bool             `json:"enabled,omitempty"   yaml:"enabled,omitempty"`
	Priority  int               `json:"priority,omitempty"  yaml:"priority,omitempty"`
	Transport string            `json:"transport,omitempty" yaml:"transport,omitempty"`
	Command   []string          `json:"command,omitempty"   yaml:"command,omitempty"`
	Dir       string            `json:"dir,omitempty"       yaml:"dir,omitempty"`
	Env       map[string]string `json:"env,omitempty"       yaml:"env,omitempty"`
	Observe   []string          `json:"observe,omitempty"   yaml:"observe,omitempty"`
	Intercept []string          `json:"intercept,omitempty" yaml:"intercept,omitempty"`
}

type MCPServerManifest struct {
	Enabled  *bool             `json:"enabled,omitempty"  yaml:"enabled,omitempty"`
	Deferred *bool             `json:"deferred,omitempty" yaml:"deferred,omitempty"`
	Command  string            `json:"command,omitempty"  yaml:"command,omitempty"`
	Args     []string          `json:"args,omitempty"     yaml:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"      yaml:"env,omitempty"`
	EnvFile  string            `json:"env_file,omitempty" yaml:"env_file,omitempty"`
	Type     string            `json:"type,omitempty"     yaml:"type,omitempty"`
	URL      string            `json:"url,omitempty"      yaml:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"  yaml:"headers,omitempty"`
}

type ToolManifest struct {
	Name        string            `json:"name"                  yaml:"name"`
	Description string            `json:"description"           yaml:"description"`
	Parameters  map[string]any    `json:"parameters,omitempty"  yaml:"parameters,omitempty"`
	Command     []string          `json:"command,omitempty"     yaml:"command,omitempty"`
	Dir         string            `json:"dir,omitempty"         yaml:"dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"      yaml:"env,omitempty"`
	TimeoutMS   int               `json:"timeout_ms,omitempty"  yaml:"timeout_ms,omitempty"`
}

type CommandManifest struct {
	Name        string            `json:"name"                 yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Usage       string            `json:"usage,omitempty"       yaml:"usage,omitempty"`
	Aliases     []string          `json:"aliases,omitempty"     yaml:"aliases,omitempty"`
	Command     []string          `json:"command,omitempty"     yaml:"command,omitempty"`
	Dir         string            `json:"dir,omitempty"         yaml:"dir,omitempty"`
	Env         map[string]string `json:"env,omitempty"         yaml:"env,omitempty"`
	TimeoutMS   int               `json:"timeout_ms,omitempty"  yaml:"timeout_ms,omitempty"`
}

type SkillManifest struct {
	Name string `json:"name" yaml:"name"`
	Path string `json:"path" yaml:"path"`
}

func (m *Manifest) normalize(defaultName string) {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		m.Name = defaultName
	}
	m.Version = strings.TrimSpace(m.Version)
	m.Description = strings.TrimSpace(m.Description)
	m.Kind = strings.ToLower(strings.TrimSpace(m.Kind))
	if m.Kind == "" {
		m.Kind = "standalone"
	}
}

func validatePluginName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("plugin name is required")
	}
	if !pluginNamePattern.MatchString(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	return nil
}

func validatePluginKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("plugin key is required")
	}
	if !pluginKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid plugin key %q", key)
	}
	return nil
}

func processHookConfigFromManifest(h ProcessHookManifest) config.ProcessHookConfig {
	enabled := true
	if h.Enabled != nil {
		enabled = *h.Enabled
	}
	return config.ProcessHookConfig{
		Enabled:   enabled,
		Priority:  h.Priority,
		Transport: h.Transport,
		Command:   append([]string(nil), h.Command...),
		Dir:       h.Dir,
		Env:       cloneStringMap(h.Env),
		Observe:   append([]string(nil), h.Observe...),
		Intercept: append([]string(nil), h.Intercept...),
	}
}

func mcpServerConfigFromManifest(s MCPServerManifest) config.MCPServerConfig {
	enabled := true
	if s.Enabled != nil {
		enabled = *s.Enabled
	}
	return config.MCPServerConfig{
		Enabled:  enabled,
		Deferred: s.Deferred,
		Command:  strings.TrimSpace(s.Command),
		Args:     append([]string(nil), s.Args...),
		Env:      cloneStringMap(s.Env),
		EnvFile:  strings.TrimSpace(s.EnvFile),
		Type:     strings.TrimSpace(s.Type),
		URL:      strings.TrimSpace(s.URL),
		Headers:  cloneStringMap(s.Headers),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
