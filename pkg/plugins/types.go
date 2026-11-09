// Package plugins hosts Agent Plugins 1.0.0 packages without loading native code
// into the PicoClaw process. Portable capabilities are skills and MCP servers.
package plugins

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/cron"
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
	Context        context.Context
	ID             string
	Root           string
	DataDir        string
	Logger         *slog.Logger
	Config         map[string]string
	AllowHooks     bool
	AllowSchedules bool
	InitTimeout    time.Duration
	CallTimeout    time.Duration
}

type Plugin interface {
	Manifest() PluginManifest
	Initialize(context PluginContext) error
	Close() error
}

// Capabilities is an immutable snapshot passed to the host after discovery.
type Capabilities struct {
	ID        string
	Root      string
	Entry     config.PluginEntryConfig
	Tools     []*PluginTool
	Skills    []skills.PluginSkill
	Hooks     []Hook
	Schedules []Schedule
	// HasResources reports bundled, readable resources (valid skills or a
	// references directory) so the host can expose the resource tool even when
	// no skill is loaded.
	HasResources bool
}

// Precheck is a resolved wake gate declared by a plugin schedule. The plugin
// loader resolves the command and environment once; the stored job keeps the
// result.
type Precheck struct {
	Command []string
	Dir     string
	Env     map[string]string
	Timeout time.Duration
}

// Schedule is a host-managed cron job declared by a plugin. The host owns the
// stored job; the plugin owns only this declaration.
type Schedule struct {
	PluginID string
	Name     string
	Schedule cron.CronSchedule
	Message  string
	Channel  string
	To       string
	Precheck *Precheck
	AgentID  string
	Enabled  *bool
}

// DeclarationKey is stable across plugin reloads and restarts.
func (s Schedule) DeclarationKey() string {
	return "plugin:" + s.PluginID + ":" + s.Name
}

// Declaration converts the package schedule into a host cron declaration.
func (s Schedule) Declaration() cron.CronJobDeclaration {
	var precheck *cron.JobPrecheck
	if s.Precheck != nil {
		precheck = &cron.JobPrecheck{
			Command:   append([]string(nil), s.Precheck.Command...),
			Dir:       s.Precheck.Dir,
			Env:       cloneStringMap(s.Precheck.Env),
			TimeoutMS: s.Precheck.Timeout.Milliseconds(),
		}
	}
	return cron.CronJobDeclaration{
		DeclarationKey: s.DeclarationKey(),
		Name:           s.Name,
		Schedule:       s.Schedule,
		Message:        s.Message,
		Channel:        s.Channel,
		To:             s.To,
		Precheck:       precheck,
		AgentID:        s.AgentID,
		Enabled:        s.Enabled,
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

type Hook struct {
	Name      string
	Command   []string
	Dir       string
	Env       []string
	Observe   []string
	Intercept []string
	Config    map[string]string
	MCP       *MCPHookAction
}

// MCPHookAction maps declared PicoClaw hook events to a tool on the plugin's
// MCP server. Result selects how the tool output affects the active hook.
type MCPHookAction struct {
	Server    string         `json:"server"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Result    string         `json:"result,omitempty"`
}
