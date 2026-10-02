package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/plugins"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func agentPluginFixture(t *testing.T, cfg *config.Config, url string) string {
	t.Helper()
	root := t.TempDir()
	write := func(path string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("plugin.json", map[string]any{"$schema": plugins.ManifestSchema, "name": "test-plugin"})
	if url != "" {
		write(
			"mcp.json",
			map[string]any{
				"$schema":    plugins.MCPSchema,
				"mcpServers": map[string]any{"server": map[string]string{"type": "streamable-http", "url": url}},
			},
		)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills", "greet"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "skills", "greet", "SKILL.md"),
		[]byte("---\nname: greet\ndescription: Greeting skill\n---\nSay hello."),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	cfg.Plugins = config.PluginsConfig{
		Enabled:       true,
		Directories:   []string{filepath.Join(root, "none")},
		DataDir:       t.TempDir(),
		InitTimeoutMS: 2000,
		StartupWaitMS: 3000,
		Entries:       map[string]config.PluginEntryConfig{"test-plugin": {Enabled: true, Path: root}},
	}
	return root
}

func TestAgentPluginsLoadExecuteAndReload(t *testing.T) {
	server := sdk.NewServer(&sdk.Implementation{Name: "agent-plugin", Version: "1"}, nil)
	sdk.AddTool(
		server,
		&sdk.Tool{Name: "hello", Description: "Say hello"},
		func(context.Context, *sdk.CallToolRequest, map[string]any) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "plugin says hello"}}}, nil, nil
		},
	)
	httpServer := httptest.NewServer(
		sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil),
	)
	defer httpServer.Close()
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()
	agentPluginFixture(t, cfg, httpServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := al.ProcessDirect(ctx, "hi", "plugin-test"); err != nil {
		t.Fatal(err)
	}
	agent := al.GetRegistry().GetDefaultAgent()
	var toolName string
	for _, name := range agent.Tools.List() {
		if strings.HasPrefix(name, "mcp_plugin_") && !strings.Contains(name, "read_resource") {
			toolName = name
		}
	}
	if toolName == "" {
		t.Fatalf("plugin tool missing: %+v", al.PluginStatuses())
	}
	result := agent.Tools.Execute(ctx, toolName, map[string]any{})
	if result.IsError || !strings.Contains(result.ForLLM, "plugin says hello") {
		t.Fatalf("%+v", result)
	}
	if _, ok := agent.ContextBuilder.ResolveSkillName("test-plugin:greet"); !ok {
		t.Fatal("plugin skill missing")
	}
	if !strings.Contains(agent.ContextBuilder.buildSkillsSummary(nil), "test-plugin:greet") {
		t.Fatal("missing skill prompt")
	}
	cfg.Plugins.Enabled = false
	if err := al.ReloadProviderAndConfig(ctx, &mockProvider{}, cfg); err != nil {
		t.Fatal(err)
	}
	if agent.Tools.HasRegistered(toolName) {
		t.Fatal("old tool registration survived reload")
	}
	if _, ok := agent.ContextBuilder.ResolveSkillName("test-plugin:greet"); ok {
		t.Fatal("old skill survived reload")
	}
	if len(al.PluginStatuses()) != 0 {
		t.Fatal("runtime survived disable")
	}
}

func TestAgentPluginsAllowlist(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()
	agentPluginFixture(t, cfg, "")
	entry := cfg.Plugins.Entries["test-plugin"]
	entry.Agents = []string{}
	cfg.Plugins.Entries["test-plugin"] = entry
	al.waitPlugins(context.Background())
	if _, ok := al.GetRegistry().GetDefaultAgent().ContextBuilder.ResolveSkillName("test-plugin:greet"); ok {
		t.Fatal("deny-all agent allowlist ignored")
	}
}

func TestPluginHookScope(t *testing.T) {
	// A nil ProcessHook would panic if a denied event or stage reached it.
	h := &pluginHook{agents: []string{"allowed"}, stages: []string{"after_llm"}}
	request := &LLMHookRequest{
		Meta:     HookMeta{AgentID: "allowed"},
		Messages: []providers.Message{{Role: "user", Content: "hi"}},
	}
	if _, decision, err := h.BeforeLLM(
		context.Background(),
		request,
	); err != nil ||
		decision.Action != HookActionContinue {
		t.Fatalf("%+v %v", decision, err)
	}
	response := &LLMHookResponse{Meta: HookMeta{AgentID: "denied"}}
	if _, decision, err := h.AfterLLM(
		context.Background(),
		response,
	); err != nil ||
		decision.Action != HookActionContinue {
		t.Fatalf("%+v %v", decision, err)
	}
}

func TestAgentPluginsHookLifecycle(t *testing.T) {
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()
	root := agentPluginFixture(t, cfg, "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination, err := os.OpenFile(filepath.Join(root, "hook-helper.exe"), os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	extension := map[string]any{
		"hooks": []any{
			map[string]any{
				"name":      "rewrite",
				"command":   "./hook-helper.exe",
				"args":      []string{"-test.run=^TestProcessHook_HelperProcess$"},
				"env":       map[string]string{"PICOCLAW_HOOK_HELPER": "1", "PICOCLAW_HOOK_MODE": "rewrite"},
				"intercept": []string{"after_llm"},
			},
		},
	}
	data, err := json.Marshal(extension)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, plugins.ExtensionNamespace), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, plugins.ExtensionNamespace, "hooks.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	entry := cfg.Plugins.Entries["test-plugin"]
	entry.AllowHooks = true
	entry.Agents = []string{al.registry.GetDefaultAgent().ID}
	cfg.Plugins.Entries["test-plugin"] = entry
	al.waitPlugins(context.Background())
	request, _ := al.hooks.BeforeLLM(
		context.Background(),
		&LLMHookRequest{Meta: HookMeta{AgentID: al.registry.GetDefaultAgent().ID}, Model: "original"},
	)
	if request.Model != "original" {
		t.Fatal("undeclared hook stage was invoked")
	}
	denied, _ := al.hooks.AfterLLM(
		context.Background(),
		&LLMHookResponse{Meta: HookMeta{AgentID: "other-agent"}, Response: &providers.LLMResponse{Content: "hello"}},
	)
	if denied.Response.Content != "hello" {
		t.Fatal("hook agent scope was ignored")
	}
	response, decision := al.hooks.AfterLLM(
		context.Background(),
		&LLMHookResponse{
			Meta:     HookMeta{AgentID: al.registry.GetDefaultAgent().ID},
			Response: &providers.LLMResponse{Content: "hello"},
		},
	)
	if decision.Action != HookActionContinue || response.Response.Content != "hello|ipc" {
		t.Fatalf("response=%+v diagnostics=%+v", response, al.PluginStatuses())
	}
	al.closePlugins(false)
	response, _ = al.hooks.AfterLLM(
		context.Background(),
		&LLMHookResponse{Response: &providers.LLMResponse{Content: "hello"}},
	)
	if response.Response.Content != "hello" {
		t.Fatal("hook survived unload")
	}
}
