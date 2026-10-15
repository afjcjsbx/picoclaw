// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/afjcjsbx/picoclaw/pkg/bus"
	"github.com/afjcjsbx/picoclaw/pkg/config"
	"github.com/afjcjsbx/picoclaw/pkg/mcp"
	"github.com/afjcjsbx/picoclaw/pkg/providers"
	agenttools "github.com/afjcjsbx/picoclaw/pkg/tools"
)

func boolPtr(b bool) *bool { return &b }

func TestMCPRuntimeResetClearsState(t *testing.T) {
	var rt mcpRuntime
	manager := mcp.NewManager()
	rt.setManager(manager)
	rt.setInitErr(errors.New("stale init error"))
	rt.initOnce.Do(func() {})

	got := rt.reset()
	if got != manager {
		t.Fatalf("reset() manager = %p, want %p", got, manager)
	}
	if rt.hasManager() {
		t.Fatal("expected manager to be cleared after reset")
	}
	if err := rt.getInitErr(); err != nil {
		t.Fatalf("getInitErr() = %v, want nil", err)
	}

	reran := false
	rt.initOnce.Do(func() { reran = true })
	if !reran {
		t.Fatal("expected initOnce to be reset")
	}
}

func TestReloadProviderAndConfig_ResetsMCPRuntime(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()

	manager := mcp.NewManager()
	al.mcp.setManager(manager)
	al.mcp.setInitErr(errors.New("stale init error"))
	al.mcp.initOnce.Do(func() {})

	if !al.mcp.hasManager() {
		t.Fatal("expected MCP manager to exist before reload")
	}

	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, cfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig() error = %v", err)
	}

	if al.mcp.hasManager() {
		t.Fatal("expected MCP manager to be cleared when reloaded config has MCP disabled")
	}
	if err := al.mcp.getInitErr(); err != nil {
		t.Fatalf("getInitErr() = %v, want nil", err)
	}

	reran := false
	al.mcp.initOnce.Do(func() { reran = true })
	if !reran {
		t.Fatal("expected MCP initOnce to be reset after reload")
	}
}

func TestServerIsDeferred(t *testing.T) {
	tests := []struct {
		name             string
		discoveryEnabled bool
		serverDeferred   *bool
		want             bool
	}{
		// --- global false always wins: per-server deferred is ignored ---
		{
			name:             "global false: per-server deferred=true is ignored",
			discoveryEnabled: false,
			serverDeferred:   boolPtr(true),
			want:             false,
		},
		{
			name:             "global false: per-server deferred=false stays false",
			discoveryEnabled: false,
			serverDeferred:   boolPtr(false),
			want:             false,
		},
		// --- global true: per-server override applies ---
		{
			name:             "global true: per-server deferred=false opts out",
			discoveryEnabled: true,
			serverDeferred:   boolPtr(false),
			want:             false,
		},
		{
			name:             "global true: per-server deferred=true stays true",
			discoveryEnabled: true,
			serverDeferred:   boolPtr(true),
			want:             true,
		},
		// --- no per-server override: fall back to global ---
		{
			name:             "no per-server field, global discovery enabled",
			discoveryEnabled: true,
			serverDeferred:   nil,
			want:             true,
		},
		{
			name:             "no per-server field, global discovery disabled",
			discoveryEnabled: false,
			serverDeferred:   nil,
			want:             false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverCfg := config.MCPServerConfig{Deferred: tt.serverDeferred}
			got := serverIsDeferred(tt.discoveryEnabled, serverCfg)
			if got != tt.want {
				t.Errorf("serverIsDeferred(discoveryEnabled=%v, deferred=%v) = %v, want %v",
					tt.discoveryEnabled, tt.serverDeferred, got, tt.want)
			}
		})
	}
}

func TestRegisterMCPServerPromptContributorUsesActualRegisteredToolCount(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	agent := &AgentInstance{ContextBuilder: cb}

	registerMCPServerPromptContributor("research", agent, "github", 0, false)
	messages := cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	if prompt := messages[0].Content; strings.Contains(prompt, "MCP server `github`") {
		t.Fatalf("expected no MCP prompt when no tools were registered, got %q", prompt)
	}

	registerMCPServerPromptContributor("research", agent, "github", 2, false)
	messages = cb.BuildMessagesFromPrompt(PromptBuildRequest{CurrentMessage: "hello"})
	prompt := messages[0].Content
	if !strings.Contains(prompt, "MCP server `github` is connected") {
		t.Fatalf("expected MCP prompt for registered tools, got %q", prompt)
	}
	if !strings.Contains(prompt, "It contributes 2 tool(s)") {
		t.Fatalf("expected actual registered tool count in prompt, got %q", prompt)
	}
}

func TestToolRegistryIncludesReportsOnlyRegisteredTools(t *testing.T) {
	registry := agenttools.NewToolRegistry()
	registry.SetAllowlist([]string{"mcp_github_search"})

	registry.RegisterHidden(&allowlistTestTool{name: "mcp_github_search"})
	registry.RegisterHidden(&allowlistTestTool{name: "mcp_github_create_issue"})

	if !toolRegistryIncludes(registry, "mcp_github_search") {
		t.Fatal("expected hidden registered MCP tool to be included")
	}
	if toolRegistryIncludes(registry, "mcp_github_create_issue") {
		t.Fatal("blocked MCP tool should not be included")
	}
}

func TestFilterMCPConfigServersCaseInsensitivePreservesOriginalKeys(t *testing.T) {
	mcpCfg := config.MCPConfig{
		Servers: map[string]config.MCPServerConfig{
			"GitHub":     {Enabled: true},
			"filesystem": {Enabled: true},
			"Slack":      {Enabled: true},
		},
	}
	allowed := map[string]struct{}{
		"github":     {},
		"FILESYSTEM": {},
	}

	filtered := filterMCPConfigServers(mcpCfg, allowed)

	if len(filtered.Servers) != 2 {
		t.Fatalf("filtered.Servers = %v, want 2 entries", filtered.Servers)
	}
	if _, ok := filtered.Servers["GitHub"]; !ok {
		t.Fatal("expected original GitHub config key to be preserved")
	}
	if _, ok := filtered.Servers["filesystem"]; !ok {
		t.Fatal("expected filesystem config key to be preserved")
	}
	if _, ok := filtered.Servers["github"]; ok {
		t.Fatal("did not expect normalized github key to replace original config key")
	}
	if _, ok := filtered.Servers["Slack"]; ok {
		t.Fatal("did not expect unallowed Slack server")
	}
}

func TestAgentHasDiscoverableMCPServers(t *testing.T) {
	deferredFalse := false
	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				ToolConfig: config.ToolConfig{Enabled: true},
				Discovery: config.ToolDiscoveryConfig{
					Enabled:  true,
					UseBM25:  true,
					UseRegex: false,
				},
				Servers: map[string]config.MCPServerConfig{
					"github":     {Enabled: true},
					"filesystem": {Enabled: true, Deferred: &deferredFalse},
				},
			},
		},
	}

	tests := []struct {
		name    string
		allowed map[string]struct{}
		want    bool
	}{
		{
			name: "nil allowlist includes discoverable enabled server",
			want: true,
		},
		{
			name:    "empty allowlist denies all servers",
			allowed: map[string]struct{}{},
			want:    false,
		},
		{
			name: "selected server discoverable",
			allowed: map[string]struct{}{
				"github": {},
			},
			want: true,
		},
		{
			name: "selected server opted out of discovery",
			allowed: map[string]struct{}{
				"filesystem": {},
			},
			want: false,
		},
		{
			name: "unknown allowlist server matches nothing",
			allowed: map[string]struct{}{
				"slack": {},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentHasDiscoverableMCPServers(cfg, tt.allowed); got != tt.want {
				t.Fatalf("agentHasDiscoverableMCPServers() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureMCPInitialized_LoadFailureSetsInitErr(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()

	cfg.Tools = config.ToolsConfig{
		MCP: config.MCPConfig{
			ToolConfig: config.ToolConfig{Enabled: true},
			Servers: map[string]config.MCPServerConfig{
				"broken": {
					Enabled: true,
					Command: "picoclaw-command-that-does-not-exist-for-mcp-tests",
				},
			},
		},
	}

	err := al.ensureMCPInitialized(context.Background())
	if err == nil {
		t.Fatal("ensureMCPInitialized() error = nil, want load failure")
	}
	if !strings.Contains(err.Error(), "failed to load MCP servers") {
		t.Fatalf("ensureMCPInitialized() error = %q, want wrapped load failure", err.Error())
	}

	initErr := al.mcp.getInitErr()
	if initErr == nil {
		t.Fatal("getInitErr() = nil, want cached load failure")
	}
	if !strings.Contains(initErr.Error(), "failed to load MCP servers") {
		t.Fatalf("getInitErr() = %q, want wrapped load failure", initErr.Error())
	}
	if al.mcp.getManager() != nil {
		t.Fatal("expected MCP manager to remain nil after load failure")
	}

	err = al.ensureMCPInitialized(context.Background())
	if err == nil {
		t.Fatal("second ensureMCPInitialized() error = nil, want cached load failure")
	}
	if !strings.Contains(err.Error(), "failed to load MCP servers") {
		t.Fatalf("second ensureMCPInitialized() error = %q, want wrapped load failure", err.Error())
	}
}

// discoveryProbeTool is a hidden tool used to verify discovery TTL semantics.
type discoveryProbeTool struct {
	calls int32
}

func (t *discoveryProbeTool) Name() string        { return "discovery_probe" }
func (t *discoveryProbeTool) Description() string { return "hidden discovery probe tool" }

func (t *discoveryProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (t *discoveryProbeTool) Execute(
	_ context.Context,
	_ map[string]any,
) *agenttools.ToolResult {
	atomic.AddInt32(&t.calls, 1)
	return agenttools.NewToolResult("probe executed")
}

// discoveryScriptProvider scripts one turn: search for a hidden tool, call it,
// then answer. It models an LLM that unlocks and immediately uses a tool.
type discoveryScriptProvider struct {
	calls int
}

func (p *discoveryScriptProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	switch p.calls {
	case 1:
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID:        "call_search",
			Type:      "function",
			Name:      agenttools.RegexSearchToolName,
			Arguments: map[string]any{"pattern": "discovery_probe"},
		}}}, nil
	case 2:
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID:        "call_probe",
			Type:      "function",
			Name:      "discovery_probe",
			Arguments: map[string]any{},
		}}}, nil
	default:
		return &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}, nil
	}
}

func (p *discoveryScriptProvider) GetDefaultModel() string { return "discovery-script" }

// TestDiscoveryToolTTLOneUsableWithinDiscoveryTurn verifies that a tool unlocked
// with ttl=1 is still callable in the same conversational turn, i.e. the TTL is
// not decremented between tool-loop iterations. Ticking per iteration used to
// drop ttl=1 to zero before the model could call the discovered tool.
func TestDiscoveryToolTTLOneUsableWithinDiscoveryTurn(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()

	probe := &discoveryProbeTool{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &discoveryScriptProvider{})

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}
	agent.Tools.RegisterHidden(probe)
	agent.Tools.Register(agenttools.NewRegexSearchTool(agent.Tools, 1, 5))

	if _, err := al.ProcessDirect(t.Context(), "find and run the hidden tool", "session-ttl-one"); err != nil {
		t.Fatalf("ProcessDirect error = %v", err)
	}

	if got := atomic.LoadInt32(&probe.calls); got != 1 {
		t.Fatalf(
			"hidden tool executed %d times, want 1: ttl=1 must remain callable within the discovery turn",
			got,
		)
	}
}
