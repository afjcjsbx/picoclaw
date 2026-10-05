package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
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

func TestPluginToolDeferredOverride(t *testing.T) {
	deferred := true
	eager := false
	discovery := config.ToolDiscoveryConfig{Enabled: true, UseBM25: true}
	if !pluginToolDeferred(discovery, nil) || !pluginToolDeferred(discovery, &deferred) || pluginToolDeferred(discovery, &eager) {
		t.Fatal("plugin per-server deferred setting did not override global discovery")
	}
	if pluginToolDeferred(config.ToolDiscoveryConfig{Enabled: false, UseBM25: true}, &deferred) {
		t.Fatal("plugin tool was hidden while global discovery was disabled")
	}
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

func TestOfficialMem0PluginRemoteMCPAndNativeHooks(t *testing.T) {
	t.Setenv("MEM0_API_KEY", "test-mem0-token")
	type searchInput struct {
		Query   string         `json:"query"`
		Filters map[string]any `json:"filters"`
		Limit   int            `json:"limit"`
	}
	type addInput struct {
		Text     string         `json:"text"`
		UserID   string         `json:"user_id"`
		AppID    string         `json:"app_id"`
		Metadata map[string]any `json:"metadata"`
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "mem0-test", Version: "1"}, nil)
	var searches []searchInput
	var adds []addInput
	var mu sync.Mutex
	sdk.AddTool(server, &sdk.Tool{Name: "search_memories"}, func(_ context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, any, error) {
		mu.Lock()
		searches = append(searches, in)
		memories := []map[string]string{{"memory": "Prefers concise explanations"}}
		for _, saved := range adds {
			memories = append(memories, map[string]string{"memory": saved.Text})
		}
		mu.Unlock()
		data, _ := json.Marshal(memories)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(data)}}}, nil, nil
	})
	sdk.AddTool(server, &sdk.Tool{Name: "add_memory"}, func(_ context.Context, _ *sdk.CallToolRequest, in addInput) (*sdk.CallToolResult, any, error) {
		mu.Lock()
		adds = append(adds, in)
		mu.Unlock()
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: `{"event_id":"saved"}`}}}, nil, nil
	})
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-mem0-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer remote.Close()

	al, cfg, msgBus, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("plugin.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"mem0","repository":"https://github.com/mem0ai/mem0"}`)
	write("mcp.json", `{"$schema":"https://agent-plugins.org/schemas/1.0.0/mcp.schema.json","mcpServers":{"mem0":{"type":"stdio","command":"python3","args":["${PLUGIN_ROOT}/core/mcp_server.py"]}}}`)
	write("com.sipeed.picoclaw/hooks.json", `{"hooks":[
		{"name":"recall","intercept":["before_llm"],"mcp":{"server":"mem0","tool":"search_memories","arguments":{"query":"${user_message}","filters":{"AND":[{"user_id":"alice"},{"app_id":"project-a"}]},"limit":5},"result":"append_to_user_message"}},
		{"name":"capture","observe":["turn_completed"],"mcp":{"server":"mem0","tool":"add_memory","arguments":{"text":"User: ${user_message}\nAssistant: ${assistant_message}","user_id":"alice","app_id":"project-a","metadata":{"source":"picoclaw"}}}}
	]}`)
	write("skills/remember/SKILL.md", "---\nname: remember\ndescription: Remember a fact\n---\nRemember it.\n")
	write("skills/status/SKILL.md", "---\nname: status\ndescription: Local status\n---\nRun local Python.\n")
	cfg.Plugins = config.PluginsConfig{
		Enabled: true, DataDir: t.TempDir(), StartupWaitMS: 3000,
		Entries: map[string]config.PluginEntryConfig{"mem0": {
			Enabled: true, Path: root, AllowHooks: true,
			SkillNames:   []string{},
			MCPOverrides: map[string]json.RawMessage{"mem0": json.RawMessage(fmt.Sprintf(`{"type":"streamable-http","url":%q,"deferred":false,"headers":{"Authorization":"Bearer ${MEM0_API_KEY}"}}`, remote.URL+"/mcp"))},
		}},
	}
	cfg.Tools.MCP.Discovery = config.ToolDiscoveryConfig{Enabled: true, UseBM25: true}
	cfg.Agents.Defaults.ToolFeedback.Enabled = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	al.waitPlugins(ctx)
	if statuses := al.PluginStatuses(); len(statuses) != 1 || statuses[0].State != plugins.Ready {
		t.Fatalf("status = %+v", statuses)
	}
	var mem0ToolVisible bool
	for _, definition := range al.registry.GetDefaultAgent().Tools.ToProviderDefs() {
		if strings.HasPrefix(definition.Function.Name, "mcp_plugin_mem0_mem0_search_memories_") {
			mem0ToolVisible = true
			break
		}
	}
	if !mem0ToolVisible {
		t.Fatal("Mem0 tool with deferred=false was not directly exposed while discovery was enabled")
	}
	if _, ok := al.registry.GetDefaultAgent().ContextBuilder.ResolveSkillName("mem0:remember"); ok {
		t.Fatal("disabled package skill was loaded")
	}
	var recallHook, captureHook *pluginMCPHook
	for _, registration := range al.hooks.snapshotHooks() {
		hook, ok := registration.Hook.(*pluginMCPHook)
		if !ok {
			continue
		}
		switch hook.spec.Name {
		case "mem0:recall":
			recallHook = hook
		case "mem0:capture":
			captureHook = hook
		}
	}
	if recallHook == nil || captureHook == nil {
		t.Fatalf("MCP hooks missing: %+v", al.PluginStatuses())
	}
	if captureHook.observeAll || !captureHook.observeKinds[runtimeevents.KindAgentTurnEnd.String()] {
		t.Fatalf("capture hook has incorrect event filter: all=%v kinds=%v", captureHook.observeAll, captureHook.observeKinds)
	}
	agentID := al.registry.GetDefaultAgent().ID
	feedbackSession := "mem0-feedback-session"
	feedbackChat := "mem0-feedback-chat"
	feedbackTurnState := &turnState{
		agent:      al.registry.GetDefaultAgent(),
		channel:    "telegram",
		chatID:     feedbackChat,
		sessionKey: feedbackSession,
		opts: processOptions{Dispatch: DispatchRequest{
			SessionKey: feedbackSession,
			InboundContext: &bus.InboundContext{
				Channel: "telegram", ChatID: feedbackChat, Account: "primary", TopicID: "topic-1",
			},
		}},
	}
	feedbackCtx := WithAgentLoop(withTurnState(ctx, feedbackTurnState), al)
	turnID := "mem0-turn-1"
	before, _, err := recallHook.BeforeLLM(feedbackCtx, &LLMHookRequest{
		Meta:     HookMeta{AgentID: agentID, TurnID: turnID, Iteration: 1},
		Messages: []providers.Message{{Role: "user", Content: "What did we decide? token test-mem0-token"}},
	})
	if err != nil || !strings.Contains(before.Messages[0].Content, "Prefers concise explanations") {
		t.Fatalf("memory was not injected: %+v %v", before, err)
	}
	before, _, err = recallHook.BeforeLLM(feedbackCtx, &LLMHookRequest{
		Meta:     HookMeta{AgentID: agentID, TurnID: turnID, Iteration: 2},
		Messages: []providers.Message{{Role: "user", Content: "And what else?"}},
	})
	if err != nil || !strings.Contains(before.Messages[0].Content, "Prefers concise explanations") {
		t.Fatalf("cached memory was not injected on the next LLM iteration: %+v %v", before, err)
	}
	if err := recallHook.OnRuntimeEvent(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{AgentID: agentID, TurnID: turnID},
	}); err != nil {
		t.Fatal(err)
	}
	before, _, err = recallHook.BeforeLLM(feedbackCtx, &LLMHookRequest{
		Meta:     HookMeta{AgentID: agentID, TurnID: "mem0-turn-2", Iteration: 1},
		Messages: []providers.Message{{Role: "user", Content: "What about the next topic?"}},
	})
	if err != nil || !strings.Contains(before.Messages[0].Content, "Prefers concise explanations") {
		t.Fatalf("memory was not searched for the next turn: %+v %v", before, err)
	}
	if err := captureHook.OnRuntimeEvent(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentLLMRequest, Scope: runtimeevents.Scope{AgentID: agentID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := captureHook.OnRuntimeEvent(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{AgentID: agentID},
		Payload: TurnEndPayload{Status: TurnEndStatusCompleted},
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(adds) != 0 {
		mu.Unlock()
		t.Fatalf("capture ran for unrelated or empty event: %+v", adds)
	}
	mu.Unlock()
	if err := captureHook.OnRuntimeEvent(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{
			AgentID: agentID, SessionKey: feedbackSession, Channel: "telegram",
			Account: "primary", ChatID: feedbackChat, TopicID: "topic-1",
		},
		Payload: TurnEndPayload{Status: TurnEndStatusCompleted, UserMessage: "remember this", FinalContent: "saved response"},
	}); err != nil {
		t.Fatal(err)
	}
	var feedbacks []bus.OutboundMessage
drainFeedback:
	for {
		select {
		case msg := <-msgBus.OutboundChan():
			feedbacks = append(feedbacks, msg)
		default:
			break drainFeedback
		}
	}
	if len(feedbacks) != 3 {
		t.Fatalf("tool feedback count = %d, want one per MCP call: %+v", len(feedbacks), feedbacks)
	}
	for _, feedback := range feedbacks {
		if feedback.Context.Raw[metadataKeyMessageKind] != messageKindToolFeedback ||
			feedback.Channel != "telegram" || feedback.ChatID != feedbackChat ||
			feedback.Context.Account != "primary" || feedback.Context.TopicID != "topic-1" {
			t.Fatalf("unexpected plugin tool feedback message: %+v", feedback)
		}
		if strings.Contains(feedback.Content, "test-mem0-token") {
			t.Fatal("tool feedback exposed the MCP credential")
		}
	}
	if !strings.Contains(feedbacks[0].Content, "mem0:search_memories") ||
		!strings.Contains(feedbacks[1].Content, "mem0:search_memories") ||
		!strings.Contains(feedbacks[2].Content, "mem0:add_memory") {
		t.Fatalf("unexpected plugin tool feedback calls: %+v", feedbacks)
	}
	suppressedEvent := runtimeevents.Event{
		Scope: runtimeevents.Scope{Channel: "telegram", ChatID: feedbackChat},
		Attrs: runtimeAttrsFromHookMeta(HookMeta{suppressToolFeedback: true}),
	}
	captureHook.publishToolFeedback(ctx, map[string]any{}, map[string]any{}, &suppressedEvent)
	select {
	case msg := <-msgBus.OutboundChan():
		t.Fatalf("suppressed turn published tool feedback: %+v", msg)
	default:
	}
	mu.Lock()
	defer mu.Unlock()
	if len(searches) != 2 || len(adds) != 1 {
		t.Fatalf("searches=%+v adds=%+v", searches, adds)
	}
	if !strings.Contains(searches[0].Query, "What did we decide?") ||
		!strings.Contains(searches[1].Query, "What about the next topic?") ||
		!strings.Contains(fmt.Sprint(searches[0].Filters), "project-a") || searches[0].Limit != 5 {
		t.Fatalf("unscoped recall: %+v", searches[0])
	}
	if adds[0].UserID != "alice" || adds[0].AppID != "project-a" || !strings.Contains(adds[0].Text, "saved response") {
		t.Fatalf("unexpected capture: %+v", adds[0])
	}
	if strings.Contains(searches[0].Query, "test-mem0-token") || strings.Contains(adds[0].Text, "test-mem0-token") {
		t.Fatal("MCP credential was sent in memory content")
	}
}

func TestPluginMCPHooksWithDifferentProviderSchema(t *testing.T) {
	type searchInput struct {
		Query        string `json:"query"`
		ContainerTag string `json:"containerTag"`
	}
	type addInput struct {
		Content      string `json:"content"`
		Action       string `json:"action"`
		ContainerTag string `json:"containerTag"`
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "supermemory-test", Version: "1"}, nil)
	var searches []searchInput
	var adds []addInput
	sdk.AddTool(server, &sdk.Tool{Name: "search_memory"}, func(_ context.Context, _ *sdk.CallToolRequest, in searchInput) (*sdk.CallToolResult, any, error) {
		searches = append(searches, in)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "Prefers short answers"}}}, nil, nil
	})
	sdk.AddTool(server, &sdk.Tool{Name: "add_memory"}, func(_ context.Context, _ *sdk.CallToolRequest, in addInput) (*sdk.CallToolResult, any, error) {
		adds = append(adds, in)
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "saved"}}}, nil, nil
	})
	remote := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, nil))
	defer remote.Close()
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()
	root := agentPluginFixture(t, cfg, "")
	if err := os.MkdirAll(filepath.Join(root, "com.sipeed.picoclaw"), 0o700); err != nil {
		t.Fatal(err)
	}
	hooks := `{"hooks":[
		{"name":"recall","intercept":["before_llm"],"mcp":{"server":"supermemory","tool":"search_memory","arguments":{"query":"${user_message}","containerTag":"project-b"},"result":"append_to_user_message"}},
		{"name":"capture","observe":["turn_completed"],"mcp":{"server":"supermemory","tool":"add_memory","arguments":{"content":"User: ${user_message}\nAssistant: ${assistant_message}","action":"save","containerTag":"project-b"}}}
	]}`
	if err := os.WriteFile(filepath.Join(root, "com.sipeed.picoclaw", "hooks.json"), []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := cfg.Plugins.Entries["test-plugin"]
	entry.AllowHooks = true
	entry.SkillNames = []string{}
	entry.MCPOverrides = map[string]json.RawMessage{"supermemory": json.RawMessage(fmt.Sprintf(`{"type":"streamable-http","url":%q}`, remote.URL))}
	entry.Path = root
	cfg.Plugins.Entries["test-plugin"] = entry
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	al.waitPlugins(ctx)
	var recallHook, captureHook *pluginMCPHook
	for _, registration := range al.hooks.snapshotHooks() {
		hook, ok := registration.Hook.(*pluginMCPHook)
		if ok && hook.pluginID == "test-plugin" {
			if hook.spec.Name == "test-plugin:recall" {
				recallHook = hook
			} else if hook.spec.Name == "test-plugin:capture" {
				captureHook = hook
			}
		}
	}
	if recallHook == nil || captureHook == nil {
		t.Fatalf("MCP hooks missing: %+v", al.PluginStatuses())
	}
	agentID := al.registry.GetDefaultAgent().ID
	if _, _, err := recallHook.BeforeLLM(ctx, &LLMHookRequest{
		Meta:     HookMeta{AgentID: agentID, Iteration: 1},
		Messages: []providers.Message{{Role: "user", Content: "What preferences do I have?"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := captureHook.OnRuntimeEvent(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentTurnEnd, Scope: runtimeevents.Scope{AgentID: agentID},
		Payload: TurnEndPayload{Status: TurnEndStatusCompleted, UserMessage: "project needs", FinalContent: "concise answer"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(searches) != 1 || searches[0].ContainerTag != "project-b" || len(adds) != 1 ||
		adds[0].ContainerTag != "project-b" || adds[0].Action != "save" || !strings.Contains(adds[0].Content, "concise answer") {
		t.Fatalf("searches=%+v adds=%+v", searches, adds)
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
