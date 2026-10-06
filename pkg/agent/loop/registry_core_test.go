package loop

import (
	"slices"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestNewAgentLoop_AgentToolAllowlistFiltersRuntimeTools(t *testing.T) {
	mainWorkspace := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nMain agent.\n",
	})
	defer cleanupWorkspace(t, mainWorkspace)

	researchWorkspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
tools: [read_file, write_file, web_search, web_fetch, message]
skills: [deep-research]
---
# Agent

Research agent.
`,
	})
	defer cleanupWorkspace(t, researchWorkspace)

	cfg := testCfg([]config.AgentConfig{
		{ID: "main", Default: true, Workspace: mainWorkspace},
		{
			ID:        "research",
			Workspace: researchWorkspace,
		},
	})
	cfg.Agents.Defaults.Workspace = mainWorkspace
	cfg.Tools.ReadFile.Enabled = true
	cfg.Tools.WriteFile.Enabled = true
	cfg.Tools.ListDir.Enabled = true
	cfg.Tools.Exec.Enabled = true
	cfg.Tools.Message.Enabled = true
	cfg.Tools.Web.Enabled = true
	cfg.Tools.Web.DuckDuckGo.Enabled = true
	cfg.Tools.WebFetch.Enabled = true
	cfg.Tools.Spawn.Enabled = true
	cfg.Tools.Subagent.Enabled = true

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockRegistryProvider{})
	defer al.Close()

	research, ok := al.GetRegistry().GetAgent("research")
	if !ok || research == nil {
		t.Fatal("expected research agent")
	}

	got := research.Tools.List()
	want := []string{"message", "read_file", "web_fetch", "web_search", "write_file"}
	if !slices.Equal(got, want) {
		t.Fatalf("research tools = %v, want %v", got, want)
	}

	for _, blocked := range []string{"exec", "list_dir", "spawn", "subagent"} {
		if _, ok := research.Tools.Get(blocked); ok {
			t.Fatalf("expected %q to be blocked by allowlist", blocked)
		}
	}
}

func TestNewAgentLoop_AgentToolAllowlistRequiresExactRuntimeToolNames(t *testing.T) {
	mainWorkspace := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nMain agent.\n",
	})
	defer cleanupWorkspace(t, mainWorkspace)

	researchWorkspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
tools: [web]
---
# Agent

Research agent.
`,
	})
	defer cleanupWorkspace(t, researchWorkspace)

	cfg := testCfg([]config.AgentConfig{
		{ID: "main", Default: true, Workspace: mainWorkspace},
		{
			ID:        "research",
			Workspace: researchWorkspace,
		},
	})
	cfg.Agents.Defaults.Workspace = mainWorkspace
	cfg.Tools.Web.Enabled = true
	cfg.Tools.Web.DuckDuckGo.Enabled = true

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockRegistryProvider{})
	defer al.Close()

	research, ok := al.GetRegistry().GetAgent("research")
	if !ok || research == nil {
		t.Fatal("expected research agent")
	}

	if _, ok := research.Tools.Get("web_search"); ok {
		t.Fatal("web_search should not be registered when allowlist contains only web")
	}
	if slices.Contains(research.Tools.List(), "web_search") {
		t.Fatalf("research tools = %v, expected web_search to be absent", research.Tools.List())
	}
}
