// Package plugins hosts Agent Plugins 1.0.0 packages without loading native code
// into the PicoClaw process. Portable capabilities are skills and MCP servers.
package plugins

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/skills"
)

const (
	ManifestSchema     = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
	MCPSchema          = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"
	ExtensionNamespace = "com.sipeed.picoclaw"
)

type PluginManifest struct {
	Schema      string                     `json:"$schema"`
	Name        string                     `json:"name"`
	Version     string                     `json:"version,omitempty"`
	Description string                     `json:"description,omitempty"`
	Author      *Author                    `json:"author,omitempty"`
	Homepage    string                     `json:"homepage,omitempty"`
	Repository  string                     `json:"repository,omitempty"`
	License     string                     `json:"license,omitempty"`
	Keywords    []string                   `json:"keywords,omitempty"`
	Extensions  map[string]json.RawMessage `json:"extensions,omitempty"`
}

type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type State string

const (
	Discovered State = "discovered"
	Loading    State = "loading"
	Ready      State = "ready"
	Degraded   State = "degraded"
	Failed     State = "failed"
	Closed     State = "closed"
)

type Diagnostic struct {
	Plugin    string `json:"plugin"`
	Component string `json:"component"`
	Message   string `json:"message"`
}

type Status struct {
	ID          string       `json:"id"`
	Root        string       `json:"root"`
	Name        string       `json:"name,omitempty"`
	State       State        `json:"state"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// PluginContext is host-owned. No global configuration or message bus is exposed.
type PluginContext struct {
	Context     context.Context
	ID          string
	Root        string
	DataDir     string
	Logger      *slog.Logger
	Config      map[string]string
	AllowHooks  bool
	InitTimeout time.Duration
	CallTimeout time.Duration
}

type Plugin interface {
	Manifest() PluginManifest
	Initialize(context PluginContext) error
	Close() error
}

// Capabilities is an immutable snapshot passed to the host after discovery.
type Capabilities struct {
	ID     string
	Root   string
	Entry  config.PluginEntryConfig
	Tools  []*PluginTool
	Skills []skills.PluginSkill
	Hooks  []Hook
}

type Hook struct {
	Name      string
	Command   []string
	Dir       string
	Env       []string
	Observe   []string
	Intercept []string
	Config    map[string]string
}
