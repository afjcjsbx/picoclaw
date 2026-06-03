package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestDiscoverStateAndSourceOverride(t *testing.T) {
	bundled := t.TempDir()
	user := t.TempDir()

	writePluginManifest(t, bundled, "weather", `
name: weather
description: bundled weather
`)
	writePluginManifest(t, user, "weather", `
name: weather
description: user weather
`)

	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"weather"},
		},
	}
	result := Discover(DiscoveryOptions{
		Config:     cfg,
		BundledDir: bundled,
		UserDir:    user,
	})

	if len(result.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(result.Plugins))
	}
	plugin := result.Plugins[0]
	if plugin.Source != SourceUser {
		t.Fatalf("source = %q, want %q", plugin.Source, SourceUser)
	}
	if plugin.Manifest.Description != "user weather" {
		t.Fatalf("description = %q, want user weather", plugin.Manifest.Description)
	}
	if plugin.State != StateEnabled {
		t.Fatalf("state = %q, want %q", plugin.State, StateEnabled)
	}
}

func TestRuntimeApplyToConfig(t *testing.T) {
	root := t.TempDir()
	pluginDir := writePluginManifest(t, root, "demo", `
name: demo
description: Demo plugin
hooks:
  processes:
    audit:
      command: ["bin/hook-helper"]
      intercept: ["before_tool"]
mcp_servers:
  tools:
    command: "bin/mcp-helper"
    args: ["serve"]
tools:
  - name: demo_echo
    description: Echo from plugin
    command: ["python3", "echo-helper.py"]
    parameters:
      type: object
commands:
  - name: demostatus
    description: Demo status
    command: ["status-helper"]
skills:
  - name: workflow
    path: skills/workflow/SKILL.md
`)
	skillDir := filepath.Join(pluginDir, "skills", "workflow")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll skillDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Workflow\n\nUse this workflow."), 0o644); err != nil {
		t.Fatalf("WriteFile skill: %v", err)
	}

	cfg := &config.Config{
		Plugins: config.PluginsConfig{
			Enabled: []string{"demo"},
		},
	}
	rt := NewRuntime(Discover(DiscoveryOptions{
		Config:     cfg,
		BundledDir: root,
	}))
	runtimeCfg := rt.ConfigWithPlugins(cfg)
	if cfg.Hooks.Processes != nil {
		t.Fatal("source config should not receive plugin process hooks")
	}
	if cfg.Tools.MCP.Servers != nil {
		t.Fatal("source config should not receive plugin MCP servers")
	}
	cfg = runtimeCfg

	hook, ok := cfg.Hooks.Processes["demo/audit"]
	if !ok {
		t.Fatal("expected demo/audit hook")
	}
	if hook.Command[0] != filepath.Join(pluginDir, "bin", "hook-helper") {
		t.Fatalf("hook command = %q", hook.Command[0])
	}
	if hook.Env["PICOCLAW_PLUGIN_NAME"] != "demo" {
		t.Fatalf("hook env missing plugin name: %#v", hook.Env)
	}

	server, ok := cfg.Tools.MCP.Servers["demo/tools"]
	if !ok {
		t.Fatal("expected demo/tools MCP server")
	}
	if server.Command != filepath.Join(pluginDir, "bin", "mcp-helper") {
		t.Fatalf("server command = %q", server.Command)
	}
	if !cfg.Tools.MCP.Enabled {
		t.Fatal("MCP should be enabled when an enabled plugin contributes a server")
	}

	pluginTools := rt.ToolInstances()
	if len(pluginTools) != 1 || pluginTools[0].Name() != "demo_echo" {
		t.Fatalf("unexpected tools: %#v", pluginTools)
	}
	commandTool, ok := pluginTools[0].(*CommandTool)
	if !ok {
		t.Fatalf("tool type = %T, want *CommandTool", pluginTools[0])
	}
	if got := resolveCommand(pluginDir, commandTool.spec.Command)[0]; got != "python3" {
		t.Fatalf("PATH executable resolved to %q, want python3", got)
	}
	if defs := rt.CommandDefinitions(); len(defs) != 2 {
		t.Fatalf("command definitions len = %d, want 2", len(defs))
	}
	if skills := rt.SkillInfos(); len(skills) != 1 || skills[0].Name != "demo:workflow" {
		t.Fatalf("unexpected skills: %#v", skills)
	}
}

func TestCommandToolExecuteJSONResult(t *testing.T) {
	pluginDir := t.TempDir()
	plugin := Plugin{
		Key: "demo",
		Dir: pluginDir,
		Manifest: Manifest{
			Name: "demo",
		},
	}
	tool := NewCommandTool(plugin, ToolManifest{
		Name:        "demo_echo",
		Description: "Echo",
		Command:     []string{os.Args[0], "-test.run=TestPluginCommandHelper", "--"},
		Env: map[string]string{
			"PICOCLAW_PLUGIN_HELPER": "tool",
		},
	})

	result := tool.Execute(context.Background(), map[string]any{"message": "hi"})
	if result.IsError {
		t.Fatalf("tool returned error: %#v", result)
	}
	if result.ForLLM != "echo: hi" {
		t.Fatalf("ForLLM = %q, want echo: hi", result.ForLLM)
	}
}

func TestPluginCommandHelper(t *testing.T) {
	if os.Getenv("PICOCLAW_PLUGIN_HELPER") == "" {
		return
	}

	var payload execPayload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Fprintf(os.Stdout, `{"for_llm":"decode failed: %s","is_error":true}`, err)
		os.Exit(0)
	}
	message, _ := payload.Arguments["message"].(string)
	fmt.Fprintf(os.Stdout, `{"for_llm":"echo: %s","silent":true}`, strings.TrimSpace(message))
	os.Exit(0)
}

func writePluginManifest(t *testing.T, root, name, manifest string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll plugin dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestYAML), []byte(strings.TrimSpace(manifest)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
	return dir
}
