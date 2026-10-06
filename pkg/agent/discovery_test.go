package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestAgentRegistry_ListAgentsBuildsStructuredDescriptors(t *testing.T) {
	mainWorkspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
name: Main Frontmatter Name
description: Structured main agent
---
# Agent

Handle general requests.
`,
	})
	defer cleanupWorkspace(t, mainWorkspace)

	supportWorkspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
name: Support Frontmatter Name
description: Support frontmatter description
---
# Agent

Handle support tickets carefully.
`,
	})
	defer cleanupWorkspace(t, supportWorkspace)

	cfg := testCfg([]config.AgentConfig{
		{ID: "main", Default: true, Name: "Configured Main", Workspace: mainWorkspace},
		{ID: "support", Workspace: supportWorkspace},
	})

	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})

	descriptors := registry.ListAgents(mainWorkspace)
	if len(descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(descriptors))
	}

	if descriptors[0].ID != "main" {
		t.Fatalf("expected current workspace agent first, got %q", descriptors[0].ID)
	}
	if descriptors[0].Name != "Main Frontmatter Name" {
		t.Fatalf("expected frontmatter name to drive discovery, got %q", descriptors[0].Name)
	}
	if descriptors[0].Description != "Structured main agent" {
		t.Fatalf("expected frontmatter description, got %q", descriptors[0].Description)
	}

	support, ok := registry.GetAgentDescriptor("support")
	if !ok || support == nil {
		t.Fatal("expected support descriptor lookup to succeed")
	}
	if support.Name != "Support Frontmatter Name" {
		t.Fatalf("expected support frontmatter name, got %q", support.Name)
	}
	if support.Description != "Support frontmatter description" {
		t.Fatalf("expected support frontmatter description, got %q", support.Description)
	}
}

func TestAgentRegistry_ListAgentsFallsBackToFirstNonEmptyAgentLine(t *testing.T) {
	workspace := setupWorkspace(t, map[string]string{
		"AGENT.md": `---
name: Research Agent
---


First useful line.
Second line.
`,
	})
	defer cleanupWorkspace(t, workspace)

	cfg := testCfg([]config.AgentConfig{
		{ID: "research", Default: true, Workspace: workspace},
	})

	registry := NewAgentRegistry(cfg, &mockRegistryProvider{})
	descriptor, ok := registry.GetAgentDescriptor("research")
	if !ok || descriptor == nil {
		t.Fatal("expected research descriptor lookup to succeed")
	}
	if descriptor.Description != "First useful line." {
		t.Fatalf("descriptor.Description = %q, want %q", descriptor.Description, "First useful line.")
	}
}
