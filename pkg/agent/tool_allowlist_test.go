package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	agenttools "github.com/sipeed/picoclaw/pkg/tools"
)

type allowlistTestTool struct {
	name string
}

func (t *allowlistTestTool) Name() string { return t.name }

func (t *allowlistTestTool) Description() string { return "test tool" }

func (t *allowlistTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}

func (t *allowlistTestTool) Execute(
	_ context.Context,
	_ map[string]any,
) *agenttools.ToolResult {
	return agenttools.NewToolResult("ok")
}

func TestCronTurnExcludesCronTool(t *testing.T) {
	registry := agenttools.NewToolRegistry()
	registry.Register(&allowlistTestTool{name: "cron"})
	registry.Register(&allowlistTestTool{name: "read_file"})
	agent := &AgentInstance{ID: "test", Tools: registry}

	for _, tc := range []struct {
		name       string
		sessionKey string
		senderID   string
		wantCron   bool
	}{
		{"scheduled turn", "agent:cron-job-123", "cron", false},
		{"scheduled subturn", "agent:subturn-123", "cron", false},
		{"scheduled session without sender", "agent:cron-job-456", "", false},
		{"ordinary turn", "agent:default:chat", "direct", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTurnState(agent, processOptions{Dispatch: DispatchRequest{
				SessionKey:     tc.sessionKey,
				InboundContext: &bus.InboundContext{SenderID: tc.senderID},
			}}, turnEventScope{})

			_, hasCron := ts.agent.Tools.Get("cron")
			if hasCron != tc.wantCron {
				t.Fatalf("cron tool available = %v, want %v", hasCron, tc.wantCron)
			}
			if _, ok := ts.agent.Tools.Get("read_file"); !ok {
				t.Fatal("unrelated tool was removed")
			}
			for _, def := range ts.agent.Tools.ToProviderDefs() {
				if def.Function.Name == "cron" && !tc.wantCron {
					t.Fatal("cron was offered to the model")
				}
			}
			if _, ok := agent.Tools.Get("cron"); !ok {
				t.Fatal("shared agent registry was modified")
			}
		})
	}
}

func TestUnknownAgentToolNames(t *testing.T) {
	workspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
tools: [read_file, web_serach, mcp_github_search]
---
# Agent
`,
	})
	defer cleanupWorkspace(t, workspace)

	registry := agenttools.NewToolRegistry()
	registry.Register(&allowlistTestTool{name: "read_file"})
	registry.Register(&allowlistTestTool{name: "web_search"})

	unknown := unknownAgentToolNames(registry, loadAgentDefinition(workspace))
	if len(unknown) != 1 || unknown[0] != "web_serach" {
		t.Fatalf("unknownAgentToolNames() = %v, want [web_serach]", unknown)
	}
}

func TestUnknownAgentToolNamesUsesRegisteredRuntimeTools(t *testing.T) {
	workspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
tools: [serial, reaction, send_tts, load_image, delegate, made_up]
---
# Agent
`,
	})
	defer cleanupWorkspace(t, workspace)

	registry := agenttools.NewToolRegistry()
	for _, name := range []string{"serial", "reaction", "send_tts", "load_image", "delegate"} {
		registry.Register(&allowlistTestTool{name: name})
	}

	unknown := unknownAgentToolNames(registry, loadAgentDefinition(workspace))
	if len(unknown) != 1 || unknown[0] != "made_up" {
		t.Fatalf("unknownAgentToolNames() = %v, want [made_up]", unknown)
	}
}

func TestResolveAgentToolAllowlistDistinguishesMissingAndEmptyToolsField(t *testing.T) {
	tests := []struct {
		name      string
		agentMD   string
		wantNil   bool
		wantEmpty bool
	}{
		{
			name: "missing tools field allows all tools",
			agentMD: `---
name: pico
---
# Agent
`,
			wantNil: true,
		},
		{
			name: "explicit empty tools list blocks all tools",
			agentMD: `---
tools: []
---
# Agent
`,
			wantEmpty: true,
		},
		{
			name: "blank tools field blocks all tools",
			agentMD: `---
tools:
---
# Agent
`,
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workspace := setupWorkspace(t, map[string]string{
				"AGENT.md": tt.agentMD,
			})
			defer cleanupWorkspace(t, workspace)

			allowlist := resolveAgentToolAllowlist(loadAgentDefinition(workspace))

			if tt.wantNil {
				if allowlist != nil {
					t.Fatalf("resolveAgentToolAllowlist() = %v, want nil", allowlist)
				}
				return
			}

			if allowlist == nil {
				t.Fatal("resolveAgentToolAllowlist() = nil, want explicit empty allowlist")
			}
			if len(allowlist) != 0 {
				t.Fatalf("resolveAgentToolAllowlist() = %v, want empty allowlist", allowlist)
			}
		})
	}
}

func TestUnknownAgentMCPServerNames(t *testing.T) {
	workspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
mcpServers: [github, githb]
---
# Agent
`,
	})
	defer cleanupWorkspace(t, workspace)

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"github": {Enabled: true},
				},
			},
		},
	}

	unknown := unknownAgentMCPServerNames(cfg, loadAgentDefinition(workspace))
	if len(unknown) != 1 || unknown[0] != "githb" {
		t.Fatalf("unknownAgentMCPServerNames() = %v, want [githb]", unknown)
	}
}

func TestUnknownAgentMCPServerNamesMatchesConfigCaseInsensitively(t *testing.T) {
	workspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
mcpServers: [github, FileSystem, slak]
---
# Agent
`,
	})
	defer cleanupWorkspace(t, workspace)

	cfg := &config.Config{
		Tools: config.ToolsConfig{
			MCP: config.MCPConfig{
				Servers: map[string]config.MCPServerConfig{
					"GitHub":     {Enabled: true},
					"filesystem": {Enabled: true},
				},
			},
		},
	}

	unknown := unknownAgentMCPServerNames(cfg, loadAgentDefinition(workspace))
	if len(unknown) != 1 || unknown[0] != "slak" {
		t.Fatalf("unknownAgentMCPServerNames() = %v, want [slak]", unknown)
	}
}
